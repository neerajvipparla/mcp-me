// MODULE: pkg/mcp/tools.go
// PURPOSE: Implements the three MCP tool operations: search_docs, get_page, add_page.
//          Owns the fetch-chunk-upsert path for add_page (synchronous, no queue).
//
// CORE DATA STRUCTURES: none retained — all inputs/outputs are per-call slices.
//
// TO MODIFY BEHAVIOR:
//   - Add a new tool: add a method here, wire it in server.go's switch.
//   - Change search top-k default: edit the default in server.go (Tools.SearchDocs accepts topK).
//
// DO NOT:
//   - Import *PostgresStore — depends on store.CrawlDB only.
//   - Import *DocumentStore — depends on store.Store only.
//   - Own connection or embedding logic — store.Store and the handler chain own those.
package mcp

import (
	"context"
	"fmt"

	"github.com/neerajvipparla/mcp-me/pkg/chunker"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/helper"
	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

type Tools struct {
	vs    store.Store
	db    store.CrawlDB
	chain crawlertypes.Handler
}

func NewTools(vs store.Store, db store.CrawlDB, chain crawlertypes.Handler) *Tools {
	return &Tools{vs: vs, db: db, chain: chain}
}

// Time: O(k) where k = topK; dominated by Qdrant network round-trip.
func (t *Tools) SearchDocs(ctx context.Context, crawlID, query string, topK uint64) ([]store.SearchResult, error) {
	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		return nil, fmt.Errorf("crawl not found")
	}
	if cr.Status != "ready" {
		return nil, fmt.Errorf("crawl not ready: %s", cr.Status)
	}
	return t.vs.Search(ctx, cr.QdrantCollection, query, topK)
}

// Time: O(n) where n = chunks stored for pageURL; dominated by Qdrant scroll.
func (t *Tools) GetPage(ctx context.Context, crawlID, pageURL string) ([]store.SearchResult, error) {
	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		return nil, fmt.Errorf("crawl not found")
	}
	return t.vs.GetByURL(ctx, cr.QdrantCollection, pageURL)
}

// Time: O(c) where c = chunks in the fetched page; dominated by fetch + Qdrant upsert.
func (t *Tools) AddPage(ctx context.Context, crawlID, pageURL string) (int, error) {
	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		return 0, fmt.Errorf("crawl not found")
	}

	result, err := t.chain.Handle(ctx, pageURL)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	md, err := helper.ToMarkdown(result)
	if err != nil {
		return 0, err
	}
	chunks, err := chunker.Split(md)
	if err != nil {
		return 0, err
	}

	texts := make([]string, len(chunks))
	points := make([]store.Point, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
		points[i] = store.Point{
			ChunkIndex:  c.ChunkIndex,
			Text:        c.Text,
			HeadingPath: c.HeadingPath,
			PageURL:     pageURL,
			PageTitle:   c.HeadingPath,
			CrawlID:     crawlID,
		}
	}
	if err := t.vs.Upsert(ctx, cr.QdrantCollection, texts, points); err != nil {
		return 0, err
	}
	return len(chunks), nil
}
