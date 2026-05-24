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

	// Cache hit: reuse existing ready collection, issue a fresh mcp_api_key.
	existing, _ := h.db.FindCrawlByHashAndEmbedder(ctx, urlHash, embedderID)
	if existing != nil {
		h.issueKey(c, existing.ID, "ready")
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

	h.issueKey(c, crawlID, "queued")
}

// issueKey creates a user_crawls row, bcrypt-hashes the mcp_api_key,
// and writes the response. mcp_api_key plaintext is never stored.
func (h *CrawlHandler) issueKey(c *gin.Context, crawlID, status string) {
	mcpKey := generateToken()
	keyHash, err := bcrypt.GenerateFromPassword([]byte(mcpKey), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(500, gin.H{"error": "key hash failed"})
		return
	}
	h.db.CreateUserCrawl(c.Request.Context(), &store.UserCrawlRecord{
		ID:            uuid.NewString(),
		UserID:        c.GetString("user_id"),
		CrawlID:       crawlID,
		MCPAPIKeyHash: string(keyHash),
	})

	code := 202
	if status == "ready" {
		code = 200
	}
	c.JSON(code, gin.H{
		"crawl_id":     crawlID,
		"mcp_endpoint": fmt.Sprintf("%s/mcp/%s", h.host, crawlID),
		"mcp_api_key":  mcpKey, // shown once, never logged
		"status":       status,
	})
}

func (h *CrawlHandler) GetStatus(c *gin.Context) {
	cr, err := h.db.GetCrawlByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
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
