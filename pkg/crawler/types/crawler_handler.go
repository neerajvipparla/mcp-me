// MODULE: pkg/crawler/types/crawler_handler.go
// PURPOSE: Defines the Handler interface and BaseHandler mixin that implement
//          the Chain of Responsibility pattern for fetch strategies.
//          Every strategy embeds BaseHandler to get SetNext/TryNext for free.
//
// CORE DATA STRUCTURES:
//   - Handler (interface): SetNext(Handler) Handler + Handle(ctx, url) (*FetchResult, error)
//   - BaseHandler (struct): holds `next Handler` — embedded by all strategies.
//     Slice of handlers in Chain: O(n) wiring, O(1) per-call dispatch.
//
// TO MODIFY BEHAVIOR:
//   - Add a strategy: implement Handler, embed BaseHandler, call b.TryNext when
//     the strategy cannot handle the URL. Register in strategies/chain.go.
//   - Change exhaustion error: edit the fmt.Errorf in TryNext.
//
// DO NOT:
//   - Import any strategy package from here (would create a cycle).
//   - Add request state to BaseHandler — it is shared across goroutines.
//
// EXTENSION POINT: new strategies implement Handler + embed BaseHandler;
//                  Chain wires them without touching this file.
package types

import (
	"context"
	"fmt"
)

// Handler is one strategy in the chain.
type Handler interface {
	SetNext(Handler) Handler
	Handle(ctx context.Context, url string) (*FetchResult, error)
}

// BaseHandler is embedded by every strategy to get SetNext and TryNext for free.
// Strategies call b.TryNext(ctx, url) when they cannot handle a URL.
type BaseHandler struct {
	next Handler
}

// SetNext wires the next handler. Returns h for fluent chaining:
//
//	a.SetNext(b).SetNext(c)
func (b *BaseHandler) SetNext(h Handler) Handler {
	b.next = h
	return h
}

// TryNext delegates to the next handler. If no next handler exists,
// returns an exhaustion error.
func (b *BaseHandler) TryNext(ctx context.Context, url string) (*FetchResult, error) {
	if b.next != nil {
		return b.next.Handle(ctx, url)
	}
	return nil, fmt.Errorf("crawler: all strategies exhausted for %s", url)
}

// Chain wires handlers left-to-right and returns the head.
// Returns nil if no handlers are provided.
func Chain(handlers ...Handler) Handler {
	if len(handlers) == 0 {
		return nil
	}
	for i := 0; i < len(handlers)-1; i++ {
		handlers[i].SetNext(handlers[i+1])
	}
	return handlers[0]
}
