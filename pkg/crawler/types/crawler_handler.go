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
