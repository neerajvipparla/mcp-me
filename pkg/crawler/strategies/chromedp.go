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
