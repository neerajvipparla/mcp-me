package strategies

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/neerajvipparla/mcp-me/pkg/crawler/types"
)

// APIKey returns the Firecrawl bearer token from the environment.
// FIRECRAWL_API_KEY is preferred; FIRECRAWL_URL is accepted as fallback.
func APIKey() string {
	if k := os.Getenv("FIRECRAWL_API_KEY"); k != "" {
		return k
	}
	return os.Getenv("FIRECRAWL_URL")
}

const defaultFirecrawlBaseURL = "https://api.firecrawl.dev"

type firecrawlRequest struct {
	URL     string   `json:"url"`
	Formats []string `json:"formats"`
}

type firecrawlResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Markdown string `json:"markdown"`
	} `json:"data"`
}

type FirecrawlHandler struct {
	types.BaseHandler
	apiKey  string
	baseURL string
	client  *http.Client
}

type FirecrawlOption func(*FirecrawlHandler)

func WithFirecrawlBaseURL(url string) FirecrawlOption {
	return func(h *FirecrawlHandler) { h.baseURL = url }
}

func NewFirecrawlHandler(apiKey string, opts ...FirecrawlOption) types.Handler {
	h := &FirecrawlHandler{
		apiKey:  apiKey,
		baseURL: defaultFirecrawlBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *FirecrawlHandler) Handle(ctx context.Context, url string) (*types.FetchResult, error) {
	body, _ := json.Marshal(firecrawlRequest{URL: url, Formats: []string{"markdown"}})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/scrape", bytes.NewReader(body))
	if err != nil {
		return h.TryNext(ctx, url)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.apiKey)

	resp, err := h.client.Do(req)
	if err != nil {
		return h.TryNext(ctx, url)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusUnauthorized:
		return h.TryNext(ctx, url)
	}
	if resp.StatusCode != http.StatusOK {
		return h.TryNext(ctx, url)
	}

	var result firecrawlResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return h.TryNext(ctx, url)
	}
	if !result.Success || result.Data.Markdown == "" {
		return h.TryNext(ctx, url)
	}

	return &types.FetchResult{
		URL:      url,
		Content:  result.Data.Markdown,
		Format:   types.FormatMarkdown,
		Strategy: "firecrawl",
	}, nil
}
