package discovery

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var sitemapClient = &http.Client{Timeout: 15 * time.Second}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

type sitemapIndex struct {
	XMLName  xml.Name     `xml:"sitemapindex"`
	Sitemaps []sitemapLoc `xml:"sitemap"`
}

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	URLs    []sitemapLoc `xml:"url"`
}

// FetchSitemap fetches and parses sitemap.xml at rootURL/sitemap.xml.
// Returns nil, nil if no sitemap exists (404). Handles sitemap index files.
func FetchSitemap(ctx context.Context, rootURL string) ([]string, error) {
	target := strings.TrimRight(rootURL, "/") + "/sitemap.xml"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	resp, err := sitemapClient.Do(req)
	if err != nil {
		return nil, nil // treat network error as "no sitemap"
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sitemap fetch returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseSitemap(ctx, body)
}

func parseSitemap(ctx context.Context, data []byte) ([]string, error) {
	var idx sitemapIndex
	if xml.Unmarshal(data, &idx) == nil && len(idx.Sitemaps) > 0 {
		var all []string
		for _, s := range idx.Sitemaps {
			urls, err := fetchChildSitemap(ctx, s.Loc)
			if err != nil {
				continue // skip broken child sitemaps
			}
			all = append(all, urls...)
		}
		return all, nil
	}

	var us urlset
	if err := xml.Unmarshal(data, &us); err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(us.URLs))
	for _, u := range us.URLs {
		if u.Loc != "" {
			urls = append(urls, strings.TrimRight(u.Loc, "/"))
		}
	}
	return urls, nil
}

func fetchChildSitemap(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sitemapClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseSitemap(ctx, body)
}
