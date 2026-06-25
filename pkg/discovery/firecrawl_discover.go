// MODULE: pkg/discovery/firecrawl_discover.go
// PURPOSE: Firecrawl /v1/crawl bulk discovery fallback.
//          Used when sitemap+BFS returns 0 pages. Submits a crawl job to
//          Firecrawl, polls for completion, and returns all pages as PageResults
//          with markdown content already attached — skipping our fetch pool.
//
// EXTENSION POINT: call FirecrawlBulkCrawl from pipeline.go after normal
//                  discovery returns 0 pages and FIRECRAWL_URL is set.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
)

const firecrawlDefaultBase = "https://api.firecrawl.dev"

var discoveryLogger = logging.Get(logging.TopicDiscovery)

type firecrawlCrawlRequest struct {
	URL           string                 `json:"url"`
	Limit         int                    `json:"limit"`
	ScrapeOptions map[string]interface{} `json:"scrapeOptions"`
}

type firecrawlCrawlStart struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
}

type firecrawlCrawlStatus struct {
	Status string `json:"status"`
	Data   []struct {
		Markdown string `json:"markdown"`
		Metadata struct {
			Title     string `json:"title"`
			SourceURL string `json:"sourceURL"`
		} `json:"metadata"`
	} `json:"data"`
}

// FirecrawlBulkCrawl submits a bulk crawl to Firecrawl's /v1/crawl endpoint,
// polls until complete, and returns all pages as PageResults with markdown content.
// baseURL defaults to https://api.firecrawl.dev when empty.
func FirecrawlBulkCrawl(ctx context.Context, rootURL, apiKey, baseURL string) ([]crawlertypes.PageResult, error) {
	if baseURL == "" {
		baseURL = firecrawlDefaultBase
	}
	client := &http.Client{Timeout: 30 * time.Second}

	body, _ := json.Marshal(firecrawlCrawlRequest{
		URL:   rootURL,
		Limit: 500,
		ScrapeOptions: map[string]interface{}{
			"formats": []string{"markdown"},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/crawl", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl submit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("firecrawl submit: status %d", resp.StatusCode)
	}

	var start firecrawlCrawlStart
	if err := json.NewDecoder(resp.Body).Decode(&start); err != nil {
		return nil, fmt.Errorf("firecrawl submit decode: %w", err)
	}
	if !start.Success || start.ID == "" {
		return nil, fmt.Errorf("firecrawl submit: no job id returned")
	}

	discoveryLogger.Info(ctx, "firecrawl bulk crawl submitted",
		ion.String("file", "firecrawl_discover.go"),
		ion.String("func", "FirecrawlBulkCrawl"),
		ion.String("job_id", start.ID),
		ion.String("url", rootURL),
	)

	pollURL := baseURL + "/v1/crawl/" + start.ID
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		pollReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			continue
		}
		pollReq.Header.Set("Authorization", "Bearer "+apiKey)

		pollResp, err := client.Do(pollReq)
		if err != nil {
			continue
		}

		var status firecrawlCrawlStatus
		decodeErr := json.NewDecoder(pollResp.Body).Decode(&status)
		pollResp.Body.Close()
		if decodeErr != nil {
			continue
		}

		discoveryLogger.Info(ctx, "firecrawl poll",
			ion.String("file", "firecrawl_discover.go"),
			ion.String("func", "FirecrawlBulkCrawl"),
			ion.String("job_id", start.ID),
			ion.String("status", status.Status),
		)

		switch status.Status {
		case "failed", "cancelled":
			return nil, fmt.Errorf("firecrawl job %s: %s", start.ID, status.Status)
		case "completed":
			var results []crawlertypes.PageResult
			for _, page := range status.Data {
				if page.Markdown == "" || page.Metadata.SourceURL == "" {
					continue
				}
				results = append(results, crawlertypes.PageResult{
					URL: page.Metadata.SourceURL,
					Result: &crawlertypes.FetchResult{
						URL:      page.Metadata.SourceURL,
						Content:  page.Markdown,
						Format:   crawlertypes.FormatMarkdown,
						Strategy: "firecrawl_crawl",
					},
				})
			}
			discoveryLogger.Info(ctx, "firecrawl bulk crawl complete",
				ion.String("file", "firecrawl_discover.go"),
				ion.String("func", "FirecrawlBulkCrawl"),
				ion.String("url", rootURL),
				ion.String("pages", fmt.Sprintf("%d", len(results))),
			)
			return results, nil
		}
	}
}
