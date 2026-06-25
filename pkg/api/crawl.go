// MODULE: pkg/api/crawl.go
// PURPOSE: Handles POST /crawl, GET /crawl/:id, and GET /crawls.
package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"github.com/neerajvipparla/mcp-me/pkg/worker"
	"go.opentelemetry.io/otel/attribute"
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
		h.linkCrawl(c, existing, "ready", false)
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
		h.linkCrawl(c, byPage, "ready", false)
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

	h.linkCrawl(c, &store.CrawlRecord{ID: crawlID, URLRaw: req.URL}, "queued", true)
}

// linkCrawl creates a user_crawls row linking the authenticated user to this crawl,
// then returns the crawl_id and mcp_endpoint. Auth for the MCP endpoint uses the
// user's platform API key (SHA-256), so no per-crawl key is generated or stored.
func (h *CrawlHandler) linkCrawl(c *gin.Context, cr *store.CrawlRecord, status string, markFailedOnError bool) {
	ctx := c.Request.Context()

	markFailed := func() {
		if !markFailedOnError {
			return
		}
		if dbErr := h.db.UpdateCrawlStatus(ctx, cr.ID, "failed"); dbErr != nil {
			logger.Error(ctx, "failed to mark crawl as failed", dbErr,
				ion.String("file", "crawl.go"),
				ion.String("func", "linkCrawl"),
				ion.String("crawl_id", cr.ID),
			)
		}
	}

	if err := h.db.CreateUserCrawl(ctx, &store.UserCrawlRecord{
		ID:      uuid.NewString(),
		UserID:  c.GetString("user_id"),
		CrawlID: cr.ID,
	}); err != nil {
		markFailed()
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "linkCrawl"),
			ion.String("crawl_id", cr.ID),
		)
		c.JSON(500, gin.H{"error": "failed to link crawl"})
		return
	}

	endpoint := fmt.Sprintf("%s/v1/mcp/%s", h.host, cr.ID)
	resp := gin.H{
		"crawl_id":     cr.ID,
		"mcp_endpoint": endpoint,
		"status":       status,
	}
	if status == "ready" && cr.PageCount > 0 {
		resp["claude_md"] = claudeMDSnippet(cr, endpoint)
	}

	code := 202
	if status == "ready" {
		code = 200
	}
	c.JSON(code, resp)
}

// claudeMDSnippet returns a ready-to-paste CLAUDE.md block for this crawl.
func claudeMDSnippet(cr *store.CrawlRecord, endpoint string) string {
	host := cr.URLRaw
	if u, err := url.Parse(cr.URLRaw); err == nil {
		host = u.Host
	}
	crawledAt := ""
	if cr.ReadyAt != nil {
		crawledAt = " (" + cr.ReadyAt.UTC().Format("2006-01-02") + ")"
	}
	return fmt.Sprintf(
		"## mcp-me -- %s\nEndpoint: %s\nSource: %s (%d pages, %d chunks%s)\nRule: Always call search_docs before answering questions about this library.",
		host, endpoint, cr.URLRaw, cr.PageCount, cr.ChunkCount, crawledAt,
	)
}

func (h *CrawlHandler) ListCrawls(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString("user_id")

	const defaultLimit = 10
	const maxLimit = 50

	page := 1
	limit := defaultLimit
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= maxLimit {
		limit = l
	}
	offset := (page - 1) * limit

	crawls, hasMore, err := h.db.ListUserCrawls(ctx, userID, limit, offset)
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
	c.JSON(200, gin.H{
		"crawls":   result,
		"page":     page,
		"limit":    limit,
		"has_more": hasMore,
	})
}

func (h *CrawlHandler) GetPages(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	pages, err := h.db.GetCrawlPages(ctx, id)
	if err != nil {
		logger.Error(ctx, "db error", err,
			ion.String("file", "crawl.go"),
			ion.String("func", "GetPages"),
			ion.String("crawl_id", id),
		)
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	result := make([]gin.H, len(pages))
	for i, pg := range pages {
		result[i] = gin.H{
			"url":         pg.URL,
			"title":       pg.Title,
			"chunk_count": pg.ChunkCount,
			"crawled_at":  pg.CrawledAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	c.JSON(200, gin.H{
		"crawl_id": id,
		"count":    len(result),
		"pages":    result,
	})
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

