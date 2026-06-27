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
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/crypto/bcrypt"
)

type CreateCrawlResult struct {
	CrawlID     string `json:"crawl_id"`
	MCPEndpoint string `json:"mcp_endpoint"`
	MCPAPIKey   string `json:"mcp_api_key"`
	Status      string `json:"status"`
}

type ListCrawlEntry struct {
	CrawlID     string `json:"crawl_id"`
	URL         string `json:"url"`
	Status      string `json:"status"`
	PageCount   int    `json:"page_count"`
	ChunkCount  int    `json:"chunk_count"`
	CrawledAt   string `json:"crawled_at"`
	MCPEndpoint string `json:"mcp_endpoint"`
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
func (t *Tools) CreateCrawl(ctx context.Context, userID, rootURL string) (*CreateCrawlResult, error) {
	tracer := t.logger.Tracer("mcp")
	ctx, span := tracer.Start(ctx, "mcp.create_crawl")
	defer span.End()
	span.SetAttributes(
		attribute.String("url", rootURL),
		attribute.String("user_id", userID),
	)

	embedderID := t.vs.EmbedderID()
	urlHash := store.HashURL(rootURL)

	// Cache hit 1: exact root URL already crawled.
	if existing, _ := t.db.FindCrawlByHashAndEmbedder(ctx, urlHash, embedderID); existing != nil {
		span.SetAttributes(
			attribute.Bool("cache_hit", true),
			attribute.String("crawl_id", existing.ID),
		)
		t.logger.Info(ctx, "cache hit",
			ion.String("file", "tools.go"),
			ion.String("func", "CreateCrawl"),
			ion.String("type", "exact url match"),
			ion.String("url", rootURL),
			ion.String("crawl_id", existing.ID),
		)
		return t.issueKey(ctx, userID, existing.ID, rootURL, "ready")
	}

	// Cache hit 2: URL already scraped as a sub-page of another crawl.
	if byPage, _ := t.db.FindCrawlByPageURL(ctx, rootURL); byPage != nil {
		span.SetAttributes(
			attribute.Bool("cache_hit", true),
			attribute.String("crawl_id", byPage.ID),
		)
		t.logger.Info(ctx, "cache hit",
			ion.String("file", "tools.go"),
			ion.String("func", "CreateCrawl"),
			ion.String("type", "subpage match"),
			ion.String("url", rootURL),
			ion.String("crawl_id", byPage.ID),
		)
		return t.issueKey(ctx, userID, byPage.ID, rootURL, "ready")
	}

	crawlID := uuid.NewString()
	span.SetAttributes(
		attribute.Bool("cache_hit", false),
		attribute.String("crawl_id", crawlID),
	)
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
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
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
	if _, err := t.queue.Enqueue(asynq.NewTask(worker.TaskCrawlPipeline, payload,
		asynq.MaxRetry(3),
		asynq.Queue("default"),
	)); err != nil {
		if dbErr := t.db.UpdateCrawlStatus(ctx, crawlID, "failed"); dbErr != nil {
			t.logger.Error(ctx, "failed to mark crawl as failed", dbErr,
				ion.String("file", "tools.go"),
				ion.String("func", "CreateCrawl"),
				ion.String("crawl_id", crawlID),
			)
		}
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		t.logger.Error(ctx, "enqueue failed", err,
			ion.String("file", "tools.go"),
			ion.String("func", "CreateCrawl"),
			ion.String("crawl_id", crawlID),
			ion.String("url", rootURL),
		)
		return nil, fmt.Errorf("failed to queue crawl job: %w", err)
	}
	t.logger.Info(ctx, "crawl queued",
		ion.String("file", "tools.go"),
		ion.String("func", "CreateCrawl"),
		ion.String("crawl_id", crawlID),
		ion.String("url", rootURL),
	)

	return t.issueKey(ctx, userID, crawlID, rootURL, "queued")
}

// issueKey creates a user_crawls row linking newCrawlID to userID, and returns
// the new mcp_endpoint + plaintext key. userID is resolved once during MCP auth
// (both endpoints already have it) and threaded in — never re-derived from a
// crawl_id, which the account endpoint does not have.
func (t *Tools) issueKey(ctx context.Context, userID, newCrawlID, rootURL, status string) (*CreateCrawlResult, error) {
	if userID == "" {
		return nil, fmt.Errorf("issue key: empty user id")
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
		UserID:        userID,
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

// ListCrawls returns all crawls belonging to userID.
// userID is resolved once during MCP auth in server.go and threaded in — no extra DB round-trip.
func (t *Tools) ListCrawls(ctx context.Context, userID string) ([]ListCrawlEntry, error) {
	tracer := t.logger.Tracer("mcp")
	ctx, span := tracer.Start(ctx, "mcp.list_crawls")
	defer span.End()
	span.SetAttributes(attribute.String("user_id", userID))

	crawls, _, err := t.db.ListUserCrawls(ctx, userID, 10, 0)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return nil, fmt.Errorf("list crawls: %w", err)
	}

	entries := make([]ListCrawlEntry, len(crawls))
	for i, cr := range crawls {
		crawledAt := ""
		if cr.ReadyAt != nil {
			crawledAt = cr.ReadyAt.UTC().Format("2006-01-02")
		}
		entries[i] = ListCrawlEntry{
			CrawlID:     cr.ID,
			URL:         cr.URLRaw,
			Status:      cr.Status,
			PageCount:   cr.PageCount,
			ChunkCount:  cr.ChunkCount,
			CrawledAt:   crawledAt,
			MCPEndpoint: fmt.Sprintf("%s/v1/mcp/%s", t.host, cr.ID),
		}
	}
	span.SetAttributes(attribute.Int("count", len(entries)))
	t.logger.Info(ctx, "list crawls",
		ion.String("file", "tools.go"),
		ion.String("func", "ListCrawls"),
		ion.String("user_id", userID),
		ion.String("count", fmt.Sprintf("%d", len(entries))),
	)
	return entries, nil
}

type CrawlStatus struct {
	CrawlID     string `json:"crawl_id"`
	Status      string `json:"status"`
	PageCount   int    `json:"page_count"`
	ChunkCount  int    `json:"chunk_count"`
	MCPEndpoint string `json:"mcp_endpoint"`
	ReadyAt     string `json:"ready_at,omitempty"`
}

// GetStatus returns the current status of any crawl_id.
// Useful after create_crawl returns "queued" — poll until status == "ready",
// then switch to search_docs on the returned mcp_endpoint.
func (t *Tools) GetStatus(ctx context.Context, queryCrawlID string) (*CrawlStatus, error) {
	tracer := t.logger.Tracer("mcp")
	ctx, span := tracer.Start(ctx, "mcp.get_status")
	defer span.End()
	span.SetAttributes(attribute.String("query_crawl_id", queryCrawlID))

	cr, err := t.db.GetCrawlByID(ctx, queryCrawlID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, "crawl not found")
		return nil, fmt.Errorf("crawl not found: %s", queryCrawlID)
	}

	readyAt := ""
	if cr.ReadyAt != nil {
		readyAt = cr.ReadyAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	span.SetAttributes(attribute.String("status", cr.Status))
	return &CrawlStatus{
		CrawlID:     cr.ID,
		Status:      cr.Status,
		PageCount:   cr.PageCount,
		ChunkCount:  cr.ChunkCount,
		MCPEndpoint: fmt.Sprintf("%s/v1/mcp/%s", t.host, cr.ID),
		ReadyAt:     readyAt,
	}, nil
}

// minSearchScore is the minimum RRF fusion score required to include a result.
// RRF scores are not cosine similarities — they reflect rank fusion position.
// 0.01 is a practical floor that filters near-zero ranked results while keeping
// anything that ranked in the top-30 of either the dense or sparse leg.
const minSearchScore = float32(0.01)

// Time: O(k) where k = topK; dominated by Qdrant network round-trip.
func (t *Tools) SearchDocs(ctx context.Context, crawlID, query string, topK uint64) ([]store.SearchResult, error) {
	tracer := t.logger.Tracer("mcp")
	ctx, span := tracer.Start(ctx, "mcp.search_docs")
	defer span.End()
	span.SetAttributes(
		attribute.String("crawl_id", crawlID),
		attribute.String("query", query),
		attribute.Int64("top_k", int64(topK)),
	)

	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, "crawl not found")
		return nil, fmt.Errorf("crawl not found")
	}
	if cr.Status != "ready" {
		err := fmt.Errorf("crawl not ready: %s", cr.Status)
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return nil, err
	}
	results, err := t.vs.Search(ctx, cr.QdrantCollection, query, topK)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return nil, err
	}
	filtered := results[:0]
	for _, r := range results {
		if r.Score >= minSearchScore {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		span.SetAttributes(attribute.Int("results", 0))
		t.logger.Warn(ctx, "search: no results above threshold",
			ion.String("file", "tools.go"),
			ion.String("func", "SearchDocs"),
			ion.String("crawl_id", crawlID),
			ion.String("query", query),
		)
		return nil, fmt.Errorf("no relevant documentation found for this query")
	}
	span.SetAttributes(attribute.Int("results", len(filtered)))
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
	tracer := t.logger.Tracer("mcp")
	ctx, span := tracer.Start(ctx, "mcp.get_page")
	defer span.End()
	span.SetAttributes(
		attribute.String("crawl_id", crawlID),
		attribute.String("page_url", pageURL),
	)

	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, "crawl not found")
		return nil, fmt.Errorf("crawl not found")
	}
	results, err := t.vs.GetByURL(ctx, cr.QdrantCollection, pageURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Int("chunks", len(results)))
	return results, nil
}

// Time: O(c) where c = chunks in the fetched page; dominated by fetch + Qdrant upsert.
func (t *Tools) AddPage(ctx context.Context, crawlID, pageURL string) (int, error) {
	tracer := t.logger.Tracer("mcp")
	ctx, span := tracer.Start(ctx, "mcp.add_page")
	defer span.End()
	span.SetAttributes(
		attribute.String("crawl_id", crawlID),
		attribute.String("page_url", pageURL),
	)

	cr, err := t.db.GetCrawlByID(ctx, crawlID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, "crawl not found")
		return 0, fmt.Errorf("crawl not found")
	}

	result, err := t.chain.Handle(ctx, pageURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return 0, fmt.Errorf("fetch: %w", err)
	}
	md, err := helper.ToMarkdown(result)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return 0, err
	}
	chunks, err := chunker.Split(ctx, pageURL, md)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
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
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		return 0, err
	}
	span.SetAttributes(attribute.Int("chunks_added", len(chunks)))
	return len(chunks), nil
}
