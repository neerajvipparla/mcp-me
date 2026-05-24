// MODULE: pkg/crawler/strategies/chromedp.go
// PURPOSE: Owns the headless-Chromium fetch strategy. Handles JS-heavy sites
//          (Docusaurus, VitePress, Next.js) where PlainHTTP returns near-empty HTML.
//
// CORE DATA STRUCTURES:
//   - ChromedpHandler: embeds BaseHandler (next-chain wiring); holds allocCtx
//     (shared across all calls — one ExecAllocator per server lifetime).
//
// TO MODIFY BEHAVIOR:
//   - Change page-wait behavior: edit chromedp.Run task list in Handle.
//   - Adjust timeout: change the 30*time.Second constant.
//   - Tune JS-render delay: edit chromedp.Sleep duration.
//
// DO NOT:
//   - Store per-request state on ChromedpHandler — it is shared across goroutines.
//   - Create a new ExecAllocator here; it must come from cmd/server/main.go startup.
//   - Raise concurrency without memory budget — each headless context ≈150MB.
//
// EXTENSION POINT: swap chromedp.Run task list to add custom CDP interactions
//                  (e.g. scroll-to-bottom for lazy-loading) without touching chain.go.
package strategies

import (
	"context"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/types"
)

type ChromedpHandler struct {
	types.BaseHandler
	allocCtx context.Context
}

func NewChromedpHandler(allocCtx context.Context) types.Handler {
	return &ChromedpHandler{allocCtx: allocCtx}
}

func (h *ChromedpHandler) Handle(ctx context.Context, url string) (*types.FetchResult, error) {
	taskCtx, taskCancel := chromedp.NewContext(h.allocCtx)
	defer taskCancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(taskCtx, 30*time.Second)
	defer timeoutCancel()

	var html string
	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(1*time.Second),
		chromedp.OuterHTML("html", &html),
	)
	if err != nil {
		return h.TryNext(ctx, url)
	}

	if len(strings.TrimSpace(html)) < minContentLength {
		return h.TryNext(ctx, url)
	}

	return &types.FetchResult{
		URL:      url,
		Content:  html,
		Format:   types.FormatHTML,
		Strategy: "chromedp",
	}, nil
}
