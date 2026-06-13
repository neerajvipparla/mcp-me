// MODULE: pkg/mcp/tools.go
// PURPOSE: Implements MCP tool operations: search_docs, get_page, add_page, create_crawl.
//          create_crawl lets the agent start a new collection for unrelated URLs
//          without human intervention — it enqueues a full crawl job and returns
//          the new mcp_endpoint + mcp_api_key for the agent to reconfigure itself.
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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	"github.com/neerajvipparla/mcp-me/pkg/chunker"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/helper"
	crawlertypes "github.com/neerajvipparla/mcp-me/pkg/crawler/types"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"github.com/neerajvipparla/mcp-me/pkg/worker"
	"golang.org/x/crypto/bcrypt"
)

type CreateCrawlResult struct {
	CrawlID     string `json:"crawl_id"`
	MCPEndpoint string `json:"mcp_endpoint"`
	MCPAPIKey   string `json:"mcp_api_key"`
	Status      string `json:"status"`
}

type Tools struct {
	vs     store.Store
	db     store.CrawlDB
	chain  crawlertypes.Handler
	queue  *asynq.Client
	host   string
	logger *ion.Ion
}

func NewTools(vs store.Store, db store.CrawlDB, chain crawlertypes.Handler, queue *asynq.Client, host string) *Tools {
	return &Tools{vs: vs, db: db, chain: chain, queue: queue, host: host, logger: logging.Get(logging.TopicMCP)}
}

// CreateCrawl starts a new crawl collection for a root URL.
// Checks cache first — if URL was already crawled, returns existing collection instantly.
// Otherwise enqueues a full crawl job and returns status "queued".
// The returned mcp_api_key is shown once — agent must store it to access the new collection.
func (t *Tools) CreateCrawl(ctx context.Context, currentCrawlID, rootURL string) (*CreateCrawlResult, error) {
	embedderID := t.vs.EmbedderID()
	urlHash := store.HashURL(rootURL)

	// Cache hit 1: exact root URL already crawled.
	if existing, _ := t.db.FindCrawlByHashAndEmbedder(ctx, urlHash, embedderID); existing != nil {
		t.logger.Info(ctx, "cache hit",
			ion.String("file", "tools.go"),
			ion.String("func", "CreateCrawl"),
			ion.String("type", "exact url match"),
			ion.String("url", rootURL),
			ion.String("crawl_id", existing.ID),
		)
		return t.issueKey(ctx, currentCrawlID, existing.ID, rootURL, "ready")
	}

	// Cache hit 2: URL already scraped as a sub-page of another crawl.
	if byPage, _ := t.db.FindCrawlByPageURL(ctx, rootURL); byPage != nil {
		t.logger.Info(ctx, "cache hit",
			ion.String("file", "tools.go"),
			ion.String("func", "CreateCrawl"),
			ion.String("type", "subpage match"),
			ion.String("url", rootURL),
			ion.String("crawl_id", byPage.ID),
		)
		return t.issueKey(ctx, currentCrawlID, byPage.ID, rootURL, "ready")
	}

	crawlID := uuid.NewString()
	collection := store.CollectionName(urlHash, embedderID)
	if err := t.db.CreateCrawl(ctx, &store.CrawlRecord{
		ID:               crawlID,
		URLRaw:           rootURL,
		URLNormalized:    rootURL,
		URLHash:          urlHash,
		Status:           "queued",
		EmbedderID:       embedderID,
		QdrantCollection: collection,
	}); err != nil {
		t.logger.Error(ctx, "db error", err,
			ion.String("file", "tools.go"),
			ion.String("func", "CreateCrawl"),
			ion.String("op", "create crawl"),
			ion.String("url", rootURL),
		)
		return nil, fmt.Errorf("create crawl: %w", err)
	}

	payload, _ := json.Marshal(worker.CrawlPayload{
		CrawlID:    crawlID,
		URL:        rootURL,
		EmbedderID: embedderID,
	})
	t.queue.Enqueue(asynq.NewTask(worker.TaskCrawlPipeline, payload,
		asynq.MaxRetry(3),
		asynq.Queue("default"),
	))
	t.logger.Info(ctx, "crawl queued",
		ion.String("file", "tools.go"),
		ion.String("func", "CreateCrawl"),
		ion.String("crawl_id", crawlID),
		ion.String("url", rootURL),
	)

	return t.issueKey(ctx, currentCrawlID, crawlID, rootURL, "queued")
}

// issueKey creates a user_crawls row for the new crawl, reusing the user from
// the current session's crawl_id. Returns the new mcp_endpoint + plaintext key.
func (t *Tools) issueKey(ctx context.Context, currentCrawlID, newCrawlID, rootURL, status string) (*CreateCrawlResult, error) {
	uc, err := t.db.GetUserCrawlByCrawlID(ctx, currentCrawlID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	raw := make([]byte, 32)
	rand.Read(raw)
	mcpKey := hex.EncodeToString(raw)

	keyHash, err := bcrypt.GenerateFromPassword([]byte(mcpKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash key: %w", err)
	}

	if err := t.db.CreateUserCrawl(ctx, &store.UserCrawlRecord{
		ID:            uuid.NewString(),
		UserID:        uc.UserID,
		CrawlID:       newCrawlID,
		MCPAPIKeyHash: string(keyHash),
	}); err != nil {
		return nil, fmt.Errorf("store key: %w", err)
	}

	return &CreateCrawlResult{
		CrawlID:     newCrawlID,
		MCPEndpoint: fmt.Sprintf("%s/v1/mcp/%s", t.host, newCrawlID),
		MCPAPIKey:   mcpKey,
		Status:      status,
	}, nil
}

// minSearchScore is the minimum RRF fusion score required to include a result.
// RRF scores are not cosine similarities — they reflect rank fusion position.
// 0.01 is a practical floor that filters near-zero ranked results while keeping
// anything that ranked in the top-30 of either the dense or sparse leg.
const minSearchScore = float32(0.01)

// Time: O(k) where k = topK; dominated by Qdrant network round-trip.
func (t *Tools) SearchDocs(ctx context.Context, crawlID, query string, topK uint64) ([]store.SearchResult, error) {
	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		return nil, fmt.Errorf("crawl not found")
	}
	if cr.Status != "ready" {
		return nil, fmt.Errorf("crawl not ready: %s", cr.Status)
	}
	results, err := t.vs.Search(ctx, cr.QdrantCollection, query, topK)
	if err != nil {
		return nil, err
	}
	filtered := results[:0]
	for _, r := range results {
		if r.Score >= minSearchScore {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		t.logger.Warn(ctx, "search: no results above threshold",
			ion.String("file", "tools.go"),
			ion.String("func", "SearchDocs"),
			ion.String("crawl_id", crawlID),
			ion.String("query", query),
		)
		return nil, fmt.Errorf("no relevant documentation found for this query")
	}
	t.logger.Info(ctx, "search complete",
		ion.String("file", "tools.go"),
		ion.String("func", "SearchDocs"),
		ion.String("crawl_id", crawlID),
		ion.String("query", query),
		ion.String("results", fmt.Sprintf("%d", len(filtered))),
	)
	return filtered, nil
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
