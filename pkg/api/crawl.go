// MODULE: pkg/api/crawl.go
// PURPOSE: Handles POST /crawl and GET /crawl/:id.
//          Owns cache-hit detection, crawl record creation, job enqueuing,
//          and mcp_api_key generation (bcrypt-hashed, returned once).
//
// CORE DATA STRUCTURES: none — stateless handler.
//
// TO MODIFY BEHAVIOR:
//   - Change bcrypt cost: edit bcrypt.GenerateFromPassword cost constant.
//   - Change mcp_api_key length: update generateToken() byte count.
//
// DO NOT:
//   - Log mcp_api_key — it is returned exactly once and must never appear in logs.
//   - Accept embedder_id from the request — embedder is a server-wide config via store.EmbedderID().
//   - Import *PostgresStore — depends only on store.CrawlDB.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"github.com/neerajvipparla/mcp-me/pkg/worker"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/crypto/bcrypt"
)

type CrawlHandler struct {
	db    store.CrawlDB
	vs    store.Store
	queue *asynq.Client
	host  string
}

func NewCrawlHandler(db store.CrawlDB, vs store.Store, queue *asynq.Client, host string) *CrawlHandler {
	return &CrawlHandler{db: db, vs: vs, queue: queue, host: host}
}

func (h *CrawlHandler) PostCrawl(c *gin.Context) {
	var req struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	tracer := logger.Tracer("api")
	ctx, span := tracer.Start(ctx, "api.post_crawl")
	defer span.End()
	span.SetAttributes(attribute.String("url", req.URL))

	urlHash := store.HashURL(req.URL)
	embedderID := h.vs.EmbedderID()

	// Cache hit 1: exact URL was the root of a previous crawl.
	existing, _ := h.db.FindCrawlByHashAndEmbedder(ctx, urlHash, embedderID)
	if existing != nil {
		span.SetAttributes(
			attribute.String("result", "cache_hit_exact"),
			attribute.String("crawl_id", existing.ID),
		)
		logger.Info(ctx, "cache hit",
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("type", "exact url match"),
			ion.String("url", req.URL),
			ion.String("crawl_id", existing.ID),
		)
		h.issueKey(c, existing, "ready", false)
		return
	}

	// Cache hit 2: URL was discovered and scraped as a sub-page of another crawl.
	byPage, _ := h.db.FindCrawlByPageURL(ctx, req.URL)
	if byPage != nil {
		span.SetAttributes(
			attribute.String("result", "cache_hit_subpage"),
			attribute.String("crawl_id", byPage.ID),
		)
		logger.Info(ctx, "cache hit",
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("type", "subpage match"),
			ion.String("url", req.URL),
			ion.String("crawl_id", byPage.ID),
		)
		h.issueKey(c, byPage, "ready", false)
		return
	}

	crawlID := uuid.NewString()
	collection := store.CollectionName(urlHash, embedderID)
	if err := h.db.CreateCrawl(ctx, &store.CrawlRecord{
		ID:               crawlID,
		URLRaw:           req.URL,
		URLNormalized:    req.URL,
		URLHash:          urlHash,
		Status:           "queued",
		EmbedderID:       embedderID,
		QdrantCollection: collection,
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("op", "create crawl"),
			ion.String("url", req.URL),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	span.SetAttributes(
		attribute.String("result", "queued"),
		attribute.String("crawl_id", crawlID),
	)
	payload, _ := json.Marshal(worker.CrawlPayload{
		CrawlID:    crawlID,
		URL:        req.URL,
		EmbedderID: embedderID,
	})
	if _, err := h.queue.Enqueue(asynq.NewTask(worker.TaskCrawlPipeline, payload,
		asynq.MaxRetry(3),
		asynq.Queue("default"),
	)); err != nil {
		if dbErr := h.db.UpdateCrawlStatus(ctx, crawlID, "failed"); dbErr != nil {
			logger.Error(ctx, "failed to mark crawl as failed", dbErr,
				ion.String("file", "crawl.go"),
				ion.String("func", "PostCrawl"),
				ion.String("crawl_id", crawlID),
			)
		}
		span.RecordError(err)
		span.SetStatus(ion.StatusError, err.Error())
		logger.Error(ctx, "enqueue failed", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("crawl_id", crawlID),
			ion.String("url", req.URL),
		)
		c.JSON(500, gin.H{"error": "failed to queue crawl job"})
		return
	}
	logger.Info(ctx, "crawl queued",
		ion.String("file", "crawl.go"),
		ion.String("func", "PostCrawl"),
		ion.String("crawl_id", crawlID),
		ion.String("url", req.URL),
		ion.String("collection", collection),
	)

	h.issueKey(c, &store.CrawlRecord{ID: crawlID, URLRaw: req.URL}, "queued", true)
}

// issueKey creates a user_crawls row, bcrypt-hashes the mcp_api_key,
// and writes the response. mcp_api_key plaintext is never stored or logged.
// markFailedOnError: when true (fresh crawl), marks the crawl as "failed" if
// key issuance fails so the job's successful run isn't permanently inaccessible.
func (h *CrawlHandler) issueKey(c *gin.Context, cr *store.CrawlRecord, status string, markFailedOnError bool) {
	ctx := c.Request.Context()

	markFailed := func() {
		if !markFailedOnError {
			return
		}
		if dbErr := h.db.UpdateCrawlStatus(ctx, cr.ID, "failed"); dbErr != nil {
			logger.Error(ctx, "failed to mark crawl as failed", dbErr,
				ion.String("file", "crawl.go"),
				ion.String("func", "issueKey"),
				ion.String("crawl_id", cr.ID),
			)
		}
	}

	mcpKey := generateToken()
	keyHash, err := bcrypt.GenerateFromPassword([]byte(mcpKey), bcrypt.DefaultCost)
	if err != nil {
		markFailed()
		logger.Error(ctx, "key generation failed", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "issueKey"),
			ion.String("op", "bcrypt"),
			ion.String("crawl_id", cr.ID),
		)
		c.JSON(500, gin.H{"error": "key hash failed"})
		return
	}
	if err := h.db.CreateUserCrawl(ctx, &store.UserCrawlRecord{
		ID:            uuid.NewString(),
		UserID:        c.GetString("user_id"),
		CrawlID:       cr.ID,
		MCPAPIKeyHash: string(keyHash),
	}); err != nil {
		markFailed()
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "issueKey"),
			ion.String("op", "store mcp key"),
			ion.String("crawl_id", cr.ID),
		)
		c.JSON(500, gin.H{"error": "failed to store mcp key"})
		return
	}

	endpoint := fmt.Sprintf("%s/v1/mcp/%s", h.host, cr.ID)
	resp := gin.H{
		"crawl_id":     cr.ID,
		"mcp_endpoint": endpoint,
		"mcp_api_key":  mcpKey, // shown once, never logged
		"status":       status,
	}
	if status == "ready" && cr.PageCount > 0 {
		resp["claude_md"] = claudeMDSnippet(cr, endpoint, mcpKey)
	}

	code := 202
	if status == "ready" {
		code = 200
	}
	c.JSON(code, resp)
}

// claudeMDSnippet returns a ready-to-paste CLAUDE.md block for this crawl.
func claudeMDSnippet(cr *store.CrawlRecord, endpoint, mcpKey string) string {
	host := cr.URLRaw
	if u, err := url.Parse(cr.URLRaw); err == nil {
		host = u.Host
	}
	crawledAt := ""
	if cr.ReadyAt != nil {
		crawledAt = " · " + cr.ReadyAt.UTC().Format("2006-01-02")
	}
	return fmt.Sprintf(
		"## DocsMCP — %s\nEndpoint: %s\nKey: %s\nSource: %s (%d pages · %d chunks%s)\nRule: Always call search_docs before answering questions about this library. Use list_crawls to see all indexed collections.",
		host, endpoint, mcpKey, cr.URLRaw, cr.PageCount, cr.ChunkCount, crawledAt,
	)
}

func (h *CrawlHandler) ListCrawls(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString("user_id")

	crawls, err := h.db.ListUserCrawls(ctx, userID)
	if err != nil {
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "ListCrawls"),
			ion.String("user_id", userID),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	result := make([]gin.H, len(crawls))
	for i, cr := range crawls {
		crawledAt := ""
		if cr.ReadyAt != nil {
			crawledAt = cr.ReadyAt.UTC().Format("2006-01-02")
		}
		result[i] = gin.H{
			"crawl_id":     cr.ID,
			"url":          cr.URLRaw,
			"status":       cr.Status,
			"page_count":   cr.PageCount,
			"chunk_count":  cr.ChunkCount,
			"crawled_at":   crawledAt,
			"mcp_endpoint": fmt.Sprintf("%s/v1/mcp/%s", h.host, cr.ID),
		}
	}
	c.JSON(200, result)
}

func (h *CrawlHandler) GetStatus(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	tracer := logger.Tracer("api")
	ctx, span := tracer.Start(ctx, "api.get_status")
	defer span.End()
	span.SetAttributes(attribute.String("crawl_id", id))

	cr, err := h.db.GetCrawlByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(ion.StatusError, "crawl not found")
		logger.Warn(ctx, "crawl not found",
			ion.String("file", "crawl.go"),
			ion.String("func", "GetStatus"),
			ion.String("crawl_id", id),
		)
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	span.SetAttributes(attribute.String("status", cr.Status))
	logger.Info(ctx, "status polled",
		ion.String("file", "crawl.go"),
		ion.String("func", "GetStatus"),
		ion.String("crawl_id", cr.ID),
		ion.String("status", cr.Status),
	)
	c.JSON(200, gin.H{
		"crawl_id":     cr.ID,
		"status":       cr.Status,
		"page_count":   cr.PageCount,
		"chunk_count":  cr.ChunkCount,
		"mcp_endpoint": fmt.Sprintf("%s/v1/mcp/%s", h.host, cr.ID),
	})
}

func generateToken() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return hex.EncodeToString(raw)
}
