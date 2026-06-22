// MODULE: pkg/crawler/strategies/plainhttp.go
// PURPOSE: Owns the first-attempt fetch strategy using plain HTTP + goquery.
//          Strips navigation/chrome noise before returning HTML. Fast and cheap —
//          succeeds on static sites (Hugo, Jekyll, MkDocs).
//
// CORE DATA STRUCTURES:
//   - PlainHTTPHandler: embeds BaseHandler; holds *http.Client (stateless, shared).
//
// TO MODIFY BEHAVIOR:
//   - Add selector stripping: extend the doc.Find() call in Handle.
//   - Change minimum content threshold: edit minContentLength const.
//   - Adjust timeout: edit the 15*time.Second in NewPlainHTTPHandler.
//
// DO NOT:
//   - Store per-request state on PlainHTTPHandler — shared across goroutines.
//   - Increase minContentLength above what Chromedp would also trigger — the
//     threshold is the handoff point between strategies.
//
// EXTENSION POINT: add additional HTML stripping selectors to the doc.Find()
//                  call without touching any other strategy.
package strategies

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/types"
	"go.opentelemetry.io/otel/attribute"
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
	tracer := crawlerLogger.Tracer("fetch.plainhttp")
	ctx, span := tracer.Start(ctx, "fetch.plainhttp")
	defer span.End()
	span.SetAttributes(attribute.String("url", url))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		crawlerLogger.Warn(ctx, "plainhttp: build request failed: falling back",
			ion.String("file", "plainhttp.go"),
			ion.String("func", "Handle"),
			ion.String("url", url),
			ion.String("error", err.Error()),
		)
		return h.TryNext(ctx, url)
	}
	req.Header.Set("User-Agent", "DocsMCP/1.0")

	resp, err := h.client.Do(req)
	if err != nil {
		crawlerLogger.Warn(ctx, "plainhttp: request failed: falling back",
			ion.String("file", "plainhttp.go"),
			ion.String("func", "Handle"),
			ion.String("url", url),
			ion.String("error", err.Error()),
		)
		return h.TryNext(ctx, url)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		span.SetAttributes(attribute.Int("status_code", resp.StatusCode))
		crawlerLogger.Warn(ctx, "plainhttp: non-200 response: falling back",
			ion.String("file", "plainhttp.go"),
			ion.String("func", "Handle"),
			ion.String("url", url),
			ion.String("status_code", fmt.Sprintf("%d", resp.StatusCode)),
		)
		return h.TryNext(ctx, url)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		crawlerLogger.Warn(ctx, "plainhttp: html parse failed: falling back",
			ion.String("file", "plainhttp.go"),
			ion.String("func", "Handle"),
			ion.String("url", url),
			ion.String("error", err.Error()),
		)
		return h.TryNext(ctx, url)
	}

	doc.Find("nav, footer, header, script, style, .sidebar, #sidebar").Remove()

	if len(strings.TrimSpace(doc.Text())) < minContentLength {
		crawlerLogger.Warn(ctx, "plainhttp: content below threshold: falling back",
			ion.String("file", "plainhttp.go"),
			ion.String("func", "Handle"),
			ion.String("url", url),
		)
		return h.TryNext(ctx, url)
	}

	html, err := doc.Html()
	if err != nil {
		crawlerLogger.Warn(ctx, "plainhttp: html serialization failed: falling back",
			ion.String("file", "plainhttp.go"),
			ion.String("func", "Handle"),
			ion.String("url", url),
			ion.String("error", err.Error()),
		)
		return h.TryNext(ctx, url)
	}

	span.SetAttributes(
		attribute.Int("status_code", resp.StatusCode),
		attribute.Int("content_bytes", len(html)),
	)
	crawlerLogger.Info(ctx, "fetch success",
		ion.String("file", "plainhttp.go"),
		ion.String("func", "Handle"),
		ion.String("strategy", "plainhttp"),
		ion.String("url", url),
	)
	return &types.FetchResult{
		URL:      url,
		Content:  html,
		Format:   types.FormatHTML,
		Strategy: "plainhttp",
	}, nil
}
