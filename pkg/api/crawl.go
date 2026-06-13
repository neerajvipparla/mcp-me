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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"github.com/neerajvipparla/mcp-me/pkg/worker"
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
	urlHash := store.HashURL(req.URL)
	embedderID := h.vs.EmbedderID()

	// Cache hit 1: exact URL was the root of a previous crawl.
	existing, _ := h.db.FindCrawlByHashAndEmbedder(ctx, urlHash, embedderID)
	if existing != nil {
		logger.Info(ctx, "cache hit",
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("type", "exact url match"),
			ion.String("url", req.URL),
			ion.String("crawl_id", existing.ID),
		)
		h.issueKey(c, existing.ID, "ready")
		return
	}

	// Cache hit 2: URL was discovered and scraped as a sub-page of another crawl.
	byPage, _ := h.db.FindCrawlByPageURL(ctx, req.URL)
	if byPage != nil {
		logger.Info(ctx, "cache hit",
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("type", "subpage match"),
			ion.String("url", req.URL),
			ion.String("crawl_id", byPage.ID),
		)
		h.issueKey(c, byPage.ID, "ready")
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
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "PostCrawl"),
			ion.String("op", "create crawl"),
			ion.String("url", req.URL),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	payload, _ := json.Marshal(worker.CrawlPayload{
		CrawlID:    crawlID,
		URL:        req.URL,
		EmbedderID: embedderID,
	})
	h.queue.Enqueue(asynq.NewTask(worker.TaskCrawlPipeline, payload,
		asynq.MaxRetry(3),
		asynq.Queue("default"),
	))
	logger.Info(ctx, "crawl queued",
		ion.String("file", "crawl.go"),
		ion.String("func", "PostCrawl"),
		ion.String("crawl_id", crawlID),
		ion.String("url", req.URL),
		ion.String("collection", collection),
	)

	h.issueKey(c, crawlID, "queued")
}

// issueKey creates a user_crawls row, bcrypt-hashes the mcp_api_key,
// and writes the response. mcp_api_key plaintext is never stored.
func (h *CrawlHandler) issueKey(c *gin.Context, crawlID, status string) {
	ctx := c.Request.Context()
	mcpKey := generateToken()
	keyHash, err := bcrypt.GenerateFromPassword([]byte(mcpKey), bcrypt.DefaultCost)
	if err != nil {
		logger.Error(ctx, "key generation failed", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "issueKey"),
			ion.String("op", "bcrypt"),
			ion.String("crawl_id", crawlID),
		)
		c.JSON(500, gin.H{"error": "key hash failed"})
		return
	}
	if err := h.db.CreateUserCrawl(ctx, &store.UserCrawlRecord{
		ID:            uuid.NewString(),
		UserID:        c.GetString("user_id"),
		CrawlID:       crawlID,
		MCPAPIKeyHash: string(keyHash),
	}); err != nil {
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "issueKey"),
			ion.String("op", "store mcp key"),
			ion.String("crawl_id", crawlID),
		)
		c.JSON(500, gin.H{"error": "failed to store mcp key"})
		return
	}

	code := 202
	if status == "ready" {
		code = 200
	}
	c.JSON(code, gin.H{
		"crawl_id":     crawlID,
		"mcp_endpoint": fmt.Sprintf("%s/v1/mcp/%s", h.host, crawlID),
		"mcp_api_key":  mcpKey, // shown once, never logged
		"status":       status,
	})
}

func (h *CrawlHandler) GetStatus(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	cr, err := h.db.GetCrawlByID(ctx, id)
	if err != nil {
		logger.Warn(ctx, "crawl not found",
			ion.String("file", "crawl.go"),
			ion.String("func", "GetStatus"),
			ion.String("crawl_id", id),
		)
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	logger.Info(ctx, "status polled",
		ion.String("file", "crawl.go"),
		ion.String("func", "GetStatus"),
		ion.String("crawl_id", cr.ID),
		ion.String("status", cr.Status),
	)
	c.JSON(200, gin.H{
		"crawl_id":    cr.ID,
		"status":      cr.Status,
		"page_count":  cr.PageCount,
		"chunk_count": cr.ChunkCount,
	})
}

func generateToken() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return hex.EncodeToString(raw)
}
