package strategies

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/neerajvipparla/mcp-me/internal/crawler/types"
)

const minContentLength = 500

type PlainHTTPHandler struct {
	types.BaseHandler
	client *http.Client
}

func NewPlainHTTPHandler() types.Handler {
	return &PlainHTTPHandler{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (h *PlainHTTPHandler) Handle(ctx context.Context, url string) (*types.FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return h.TryNext(ctx, url)
	}
	req.Header.Set("User-Agent", "DocsMCP/1.0")

	resp, err := h.client.Do(req)
	if err != nil {
		return h.TryNext(ctx, url)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return h.TryNext(ctx, url)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return h.TryNext(ctx, url)
	}

	doc.Find("nav, footer, header, script, style, .sidebar, #sidebar").Remove()

	if len(strings.TrimSpace(doc.Text())) < minContentLength {
		return h.TryNext(ctx, url)
	}

	html, err := doc.Html()
	if err != nil {
		return h.TryNext(ctx, url)
	}

	return &types.FetchResult{
		URL:      url,
		Content:  html,
		Format:   types.FormatHTML,
		Strategy: "plainhttp",
	}, nil
}
