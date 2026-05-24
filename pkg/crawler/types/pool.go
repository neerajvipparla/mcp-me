// MODULE: pkg/crawler/types/pool.go
// PURPOSE: Owns bounded-concurrency URL fetching. Dispatches a fixed worker
//          pool over a slice of URLs, returning one PageResult per URL in
//          input order. Isolates per-URL failures — one error does not abort others.
//
// CORE DATA STRUCTURES:
//   - CrawlPool: holds Handler chain + concurrency int. Stateless per FetchAll call.
//   - jobs (chan job, unbuffered): backpressure channel — context cancellation stops
//     feed loop, workers drain in-flight jobs, then wg.Wait() returns.
//   - results ([]PageResult, len=len(urls)): pre-allocated; written by index so
//     no mutex needed — each index is written by exactly one goroutine.
//
// TO MODIFY BEHAVIOR:
//   - Change default concurrency: edit constants.CRAWLER_DEFAULT_CONCURRENCY.
//   - Add per-URL retry: wrap p.chain.Handle in a retry loop inside the worker goroutine.
//
// DO NOT:
//   - Reuse a CrawlPool across jobs with different concurrency needs — create a new one.
//   - Replace the index-keyed results slice with a mutex-protected map without
//     profiling first — the current design has zero lock contention.
//
// EXTENSION POINT: pass a different Handler chain to NewCrawlPool to swap all
//                  strategies at once without touching pool logic.
package types

import (
	"context"
	"sync"

	"github.com/neerajvipparla/mcp-me/types/constants"
)

// CrawlPool fetches multiple URLs through the handler chain using a fixed
// pool of worker goroutines. One failure does not affect others.
type CrawlPool struct {
	chain       Handler
	concurrency int
}

// NewCrawlPool creates a pool. concurrency defaults to
// constants.CRAWLER_DEFAULT_CONCURRENCY when <= 0.
func NewCrawlPool(chain Handler, concurrency int) *CrawlPool {
	if concurrency <= 0 {
		concurrency = constants.CRAWLER_DEFAULT_CONCURRENCY
	}
	return &CrawlPool{chain: chain, concurrency: concurrency}
}

// FetchAll fetches all URLs using at most p.concurrency worker goroutines
// and returns one PageResult per URL in the same order as the input.
//
// Unlike a per-URL goroutine fan-out, this only spawns `min(concurrency, len(urls))`
// goroutines total, regardless of how many URLs are queued. Cancelling ctx stops
// dispatching new work; URLs that were never dispatched come back with ctx.Err()
// in PageResult.Err. In-flight fetches receive the same ctx and should return
// promptly on cancellation.
func (p *CrawlPool) FetchAll(ctx context.Context, urls []string) []PageResult {
	results := make([]PageResult, len(urls))
	if len(urls) == 0 {
		return results
	}

	type job struct {
		i   int
		url string
	}
	jobs := make(chan job)

	workers := p.concurrency
	if workers > len(urls) {
		workers = len(urls)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				result, err := p.chain.Handle(ctx, j.url)
				results[j.i] = PageResult{URL: j.url, Result: result, Err: err}
			}
		}()
	}

feed:
	for i, url := range urls {
		select {
		case <-ctx.Done():
			for k := i; k < len(urls); k++ {
				results[k] = PageResult{URL: urls[k], Err: ctx.Err()}
			}
			break feed
		case jobs <- job{i: i, url: url}:
		}
	}
	close(jobs)
	wg.Wait()
	return results
}
