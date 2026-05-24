// MODULE: pkg/worker/pipeline.go
// PURPOSE: Asynq task handler — drives the full crawl→chunk→embed→upsert pipeline.
//          Owns job state transitions written to Postgres throughout the pipeline.
//
// CORE DATA STRUCTURES:
//   - []string allTexts (slice, unbounded): chunk texts batched for Upsert.
//     Growth: proportional to total chunks across all pages. Cleared after upsert.
//   - []store.Point allPoints (slice, unbounded): parallel to allTexts, same lifecycle.
//   - batchSize (const=100): max chunks per store.Upsert call to bound memory.
//
// TO MODIFY BEHAVIOR:
//   - Change concurrency: edit the concurrency argument to types.NewCrawlPool.
//   - Change batch size: edit the batchSize constant.
//   - Add per-page DB tracking: insert into crawl_pages inside the results loop.
//
// DO NOT:
//   - Import *PostgresStore — depends on store.CrawlDB interface only.
//   - Embed texts here — store.Upsert owns embedding internally.
//   - Hold the shared chain across goroutines with mutable state — Handler is stateless.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/mcp-me/pkg/chunker"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/helper"
	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
	"github.com/neerajvipparla/mcp-me/pkg/discovery"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

const batchSize = 100

type PipelineHandler struct {
	db    store.CrawlDB
	vs    store.Store
	chain crawlertypes.Handler
}

func NewPipelineHandler(db store.CrawlDB, vs store.Store, chain crawlertypes.Handler) *PipelineHandler {
	return &PipelineHandler{db: db, vs: vs, chain: chain}
}

// Time: O(p·c) where p = pages crawled, c = avg chunks per page; Space: O(p·c)
// DS: two parallel slices (texts, points) accumulated then batch-upserted.
func (h *PipelineHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p CrawlPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	collection := store.CollectionName(store.HashURL(p.URL), h.vs.EmbedderID())
	if err := h.vs.EnsureCollection(ctx, collection); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	h.db.UpdateCrawlStatus(ctx, p.CrawlID, "crawling")

	d, err := discovery.NewDiscoverer(p.URL,
		discovery.WithMaxPages(500),
		discovery.WithHandler(h.chain),
	)
	if err != nil {
		return err
	}
	urls, err := d.Discover(ctx, p.URL)
	if err != nil {
		return err
	}

	pool := crawlertypes.NewCrawlPool(h.chain, 5)
	results := pool.FetchAll(ctx, urls)

	h.db.UpdateCrawlStatus(ctx, p.CrawlID, "chunking")

	var allTexts []string
	var allPoints []store.Point
	totalPages := 0

	for _, r := range results {
		if r.Err != nil {
			continue
		}
		md, err := helper.ToMarkdown(r.Result)
		if err != nil {
			continue
		}
		chunks, err := chunker.Split(md)
		if err != nil {
			continue
		}
		for _, c := range chunks {
			allTexts = append(allTexts, c.Text)
			allPoints = append(allPoints, store.Point{
				ChunkIndex:  c.ChunkIndex,
				Text:        c.Text,
				HeadingPath: c.HeadingPath,
				PageURL:     r.URL,
				PageTitle:   c.HeadingPath,
				CrawlID:     p.CrawlID,
			})
		}
		totalPages++
	}

	h.db.UpdateCrawlStatus(ctx, p.CrawlID, "embedding")

	for i := 0; i < len(allTexts); i += batchSize {
		end := i + batchSize
		if end > len(allTexts) {
			end = len(allTexts)
		}
		if err := h.vs.Upsert(ctx, collection, allTexts[i:end], allPoints[i:end]); err != nil {
			return fmt.Errorf("upsert batch %d: %w", i/batchSize, err)
		}
	}

	return h.db.UpdateCrawlReady(ctx, p.CrawlID, totalPages, len(allTexts), fetchLastModified(p.URL))
}

func fetchLastModified(rawURL string) *time.Time {
	resp, err := http.Head(rawURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer resp.Body.Close()
	lm := resp.Header.Get("Last-Modified")
	if lm == "" {
		return nil
	}
	t, err := http.ParseTime(lm)
	if err != nil {
		return nil
	}
	return &t
}
