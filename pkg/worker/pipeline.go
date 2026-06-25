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
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	"github.com/neerajvipparla/mcp-me/pkg/chunker"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/helper"
	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
	"github.com/neerajvipparla/mcp-me/pkg/discovery"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"go.opentelemetry.io/otel/attribute"
)

const batchSize = 100

type PipelineHandler struct {
	db     store.CrawlDB
	vs     store.Store
	chain  crawlertypes.Handler
	logger *ion.Ion
}

func NewPipelineHandler(db store.CrawlDB, vs store.Store, chain crawlertypes.Handler) *PipelineHandler {
	return &PipelineHandler{db: db, vs: vs, chain: chain, logger: logging.Get(logging.TopicWorker)}
}

// Time: O(p·c) where p = pages crawled, c = avg chunks per page; Space: O(p·c)
// DS: two parallel slices (texts, points) accumulated then batch-upserted.
func (h *PipelineHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p CrawlPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}

	tracer := h.logger.Tracer("pipeline")

	// Root span — covers the entire job.
	ctx, rootSpan := tracer.Start(ctx, "pipeline")
	defer rootSpan.End()
	rootSpan.SetAttributes(
		attribute.String("crawl_id", p.CrawlID),
		attribute.String("url", p.URL),
		attribute.String("embedder_id", h.vs.EmbedderID()),
	)

	h.logger.Info(ctx, "pipeline started",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
		ion.String("url", p.URL),
	)

	collection := store.CollectionName(store.HashURL(p.URL), h.vs.EmbedderID())

	_, colSpan := tracer.Start(ctx, "pipeline.ensure_collection")
	colSpan.SetAttributes(attribute.String("collection", collection))
	if err := h.vs.EnsureCollection(ctx, collection); err != nil {
		colSpan.RecordError(err)
		colSpan.SetStatus(ion.StatusError, err.Error())
		colSpan.End()
		return fmt.Errorf("ensure collection: %w", err)
	}
	colSpan.End()

	if err := h.db.UpdateCrawlStatus(ctx, p.CrawlID, "crawling"); err != nil {
		h.logger.Error(ctx, "failed to update status: crawling", err,
			ion.String("file", "pipeline.go"),
			ion.String("func", "ProcessTask"),
			ion.String("crawl_id", p.CrawlID),
		)
	}
	h.logger.Info(ctx, "status: crawling",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
	)

	d, err := discovery.NewDiscoverer(p.URL,
		discovery.WithMaxPages(500),
		discovery.WithHandler(h.chain),
	)
	if err != nil {
		return err
	}

	discCtx, discSpan := tracer.Start(ctx, "pipeline.discovery")
	urls, err := d.Discover(discCtx, p.URL)
	if err != nil {
		discSpan.RecordError(err)
		discSpan.SetStatus(ion.StatusError, err.Error())
		discSpan.End()
		return err
	}
	discSpan.SetAttributes(attribute.Int("url_count", len(urls)))
	discSpan.End()

	h.logger.Info(ctx, "discovery complete",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
		ion.String("url_count", fmt.Sprintf("%d", len(urls))),
	)

	// Fetch phase — either via our pool (normal path) or Firecrawl bulk crawl (fallback).
	// Firecrawl is used when sitemap+BFS returned 0 URLs and the key is configured.
	// It returns pages with markdown already attached, so we skip our fetch pool entirely.
	var results []crawlertypes.PageResult

	_, fetchSpan := tracer.Start(ctx, "pipeline.fetch_pool")

	if len(urls) == 0 {
		firecrawlKey := os.Getenv("FIRECRAWL_URL")
		if firecrawlKey != "" {
			h.logger.Info(ctx, "discovery returned 0 pages: trying firecrawl bulk crawl",
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("url", p.URL),
			)
			fcResults, fcErr := discovery.FirecrawlBulkCrawl(ctx, p.URL, firecrawlKey, "")
			if fcErr != nil {
				h.logger.Warn(ctx, "firecrawl bulk crawl failed",
					ion.String("file", "pipeline.go"),
					ion.String("func", "ProcessTask"),
					ion.String("crawl_id", p.CrawlID),
					ion.String("error", fcErr.Error()),
				)
			} else {
				results = fcResults
			}
		}
		if len(results) == 0 {
			fetchSpan.SetAttributes(attribute.Int("total", 0))
			fetchSpan.End()
			if dbErr := h.db.UpdateCrawlStatus(ctx, p.CrawlID, "failed"); dbErr != nil {
				h.logger.Error(ctx, "failed to mark crawl as failed", dbErr,
					ion.String("file", "pipeline.go"),
					ion.String("func", "ProcessTask"),
					ion.String("crawl_id", p.CrawlID),
				)
			}
			h.logger.Warn(ctx, "no pages discovered: marking crawl failed",
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("url", p.URL),
			)
			return fmt.Errorf("no pages discovered for %s", p.URL)
		}
	} else {
		pool := crawlertypes.NewCrawlPool(h.chain, 5)
		results = pool.FetchAll(ctx, urls)
	}

	if err := h.db.UpdateCrawlStatus(ctx, p.CrawlID, "chunking"); err != nil {
		h.logger.Error(ctx, "failed to update status: chunking", err,
			ion.String("file", "pipeline.go"),
			ion.String("func", "ProcessTask"),
			ion.String("crawl_id", p.CrawlID),
		)
	}
	h.logger.Info(ctx, "status: chunking",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
	)

	var allTexts []string
	var allPoints []store.Point
	totalPages := 0
	skipped := 0

	for _, r := range results {
		if r.Err != nil {
			h.logger.Warn(ctx, "fetch failed: skipping page",
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("url", r.URL),
				ion.String("error", r.Err.Error()),
			)
			skipped++
			continue
		}
		md, err := helper.ToMarkdown(r.Result)
		if err != nil {
			h.logger.Warn(ctx, "markdown conversion failed: skipping page",
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("url", r.URL),
				ion.String("error", err.Error()),
			)
			skipped++
			continue
		}
		chunks, err := chunker.Split(ctx, r.URL, md)
		if err != nil {
			h.logger.Warn(ctx, "chunking failed: skipping page",
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("url", r.URL),
				ion.String("error", err.Error()),
			)
			skipped++
			continue
		}
		pageTitle := ""
		for _, c := range chunks {
			if pageTitle == "" {
				pageTitle = c.HeadingPath
			}
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
		// Record every scraped URL so sub-page cache hits work on future requests.
		if err := h.db.CreateCrawlPage(ctx, p.CrawlID, r.URL, pageTitle, len(chunks)); err != nil {
			h.logger.Error(ctx, "failed to record crawl page", err,
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("url", r.URL),
			)
		}
		totalPages++
	}
	fetchSpan.SetAttributes(
		attribute.Int("total", len(results)),
		attribute.Int("succeeded", totalPages),
		attribute.Int("skipped", skipped),
	)
	fetchSpan.End()

	_, chunkSpan := tracer.Start(ctx, "pipeline.chunking")
	chunkSpan.SetAttributes(
		attribute.Int("pages", totalPages),
		attribute.Int("chunks", len(allTexts)),
		attribute.Int("skipped", skipped),
	)
	chunkSpan.End()

	h.logger.Info(ctx, "chunking complete",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
		ion.String("pages", fmt.Sprintf("%d", totalPages)),
		ion.String("skipped", fmt.Sprintf("%d", skipped)),
		ion.String("chunks", fmt.Sprintf("%d", len(allTexts))),
	)

	// Guard: if every page failed to parse/chunk, mark failed instead of writing empty "ready".
	if totalPages == 0 {
		if dbErr := h.db.UpdateCrawlStatus(ctx, p.CrawlID, "failed"); dbErr != nil {
			h.logger.Error(ctx, "failed to mark crawl as failed", dbErr,
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
			)
		}
		return fmt.Errorf("no pages indexed for %s (fetched %d, all failed)", p.URL, len(results))
	}

	if err := h.db.UpdateCrawlStatus(ctx, p.CrawlID, "embedding"); err != nil {
		h.logger.Error(ctx, "failed to update status: embedding", err,
			ion.String("file", "pipeline.go"),
			ion.String("func", "ProcessTask"),
			ion.String("crawl_id", p.CrawlID),
		)
	}
	h.logger.Info(ctx, "status: embedding",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
	)

	_, embedSpan := tracer.Start(ctx, "pipeline.embedding")
	embedSpan.SetAttributes(
		attribute.Int("chunks", len(allTexts)),
		attribute.String("collection", collection),
	)
	batches := 0
	for i := 0; i < len(allTexts); i += batchSize {
		end := i + batchSize
		if end > len(allTexts) {
			end = len(allTexts)
		}
		if err := h.vs.Upsert(ctx, collection, allTexts[i:end], allPoints[i:end]); err != nil {
			h.logger.Error(ctx, "upsert failed", err,
				ion.String("file", "pipeline.go"),
				ion.String("func", "ProcessTask"),
				ion.String("crawl_id", p.CrawlID),
				ion.String("batch", fmt.Sprintf("%d", i/batchSize)),
			)
			embedSpan.RecordError(err)
			embedSpan.SetStatus(ion.StatusError, err.Error())
			embedSpan.End()
			return fmt.Errorf("upsert batch %d: %w", i/batchSize, err)
		}
		batches++
	}
	embedSpan.SetAttributes(attribute.Int("batches", batches))
	embedSpan.End()

	if err := h.db.UpdateCrawlReady(ctx, p.CrawlID, totalPages, len(allTexts), fetchLastModified(p.URL)); err != nil {
		return err
	}
	h.logger.Info(ctx, "pipeline done",
		ion.String("file", "pipeline.go"),
		ion.String("func", "ProcessTask"),
		ion.String("crawl_id", p.CrawlID),
		ion.String("pages", fmt.Sprintf("%d", totalPages)),
		ion.String("chunks", fmt.Sprintf("%d", len(allTexts))),
	)
	return nil
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
