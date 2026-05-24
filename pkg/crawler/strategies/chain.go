// MODULE: pkg/crawler/strategies/chain.go
// PURPOSE: Assembles the canonical three-strategy fetch chain for this service.
//          This is the single place that knows the default strategy order and
//          which strategies are optional (Firecrawl).
//
// TO MODIFY BEHAVIOR:
//   - Add a strategy: instantiate it here and append to handlers before calling
//     types.Chain. No other file needs to change.
//   - Remove Firecrawl: delete the APIKey() guard block.
//   - Change strategy order: reorder the handlers slice.
//
// DO NOT:
//   - Duplicate chain assembly elsewhere — callers use DefaultFetchChain only.
//   - Read secrets here (APIKey() delegates to os.Getenv in firecrawl.go).
//
// EXTENSION POINT: add new strategies to handlers slice without modifying
//                  types.Chain or any handler implementation.
package strategies

import (
	"context"

	"github.com/neerajvipparla/mcp-me/pkg/crawler/types"
)

// DefaultFetchChain wires PlainHTTP → Chromedp → Firecrawl (when key is set).
// allocCtx must come from chromedp.NewExecAllocator at server startup —
// ChromedpHandler creates one browser context per Handle call and cancels it
// immediately after the page is fetched, so no cancel func is needed per chain.
func DefaultFetchChain(allocCtx context.Context) types.Handler {
	handlers := []types.Handler{
		NewPlainHTTPHandler(),
		NewChromedpHandler(allocCtx),
	}
	if key := APIKey(); key != "" {
		handlers = append(handlers, NewFirecrawlHandler(key))
	}
	return types.Chain(handlers...)
}
