// MODULE: cmd/server/main.go
// PURPOSE: Wires all components and starts the HTTP + Asynq server.
//          Non-secret settings come from config.yaml; secrets come from env vars.
//          Narrows store.DB to UserDB / CrawlDB per component (ISP).
//
// CONFIG:  config.yaml  — port, qdrant host/port, postgres host/port/db/user,
//                         crawler/worker tuning.
// SECRETS: .env         — QDRANT_API_KEY, DATABASE_PASSWORD, FIRECRAWL_URL
//                         (see .env.example for the full list).
//
// API ROUTES (all prefixed /v1):
//   POST /v1/register          — create user, receive platform API key (no auth)
//   POST /v1/crawl             — submit crawl job           (platform key required)
//   GET  /v1/crawl/:id         — poll crawl status          (platform key required)
//   POST /v1/mcp/:crawl_id     — JSON-RPC MCP endpoint      (mcp key required)
//   GET  /health               — liveness probe             (no auth)
//
// TO MODIFY BEHAVIOR:
//   - Add a route: register it in the relevant group below.
//   - Switch Qdrant mode: set/unset QDRANT_API_KEY — qdrantcfg.From() handles it.
//   - Switch DB: implement store.DB, replace NewPostgresStore here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/mcp-me/pkg/api"
	"github.com/neerajvipparla/mcp-me/pkg/config"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/strategies"
	"github.com/neerajvipparla/mcp-me/pkg/mcp"
	"github.com/neerajvipparla/mcp-me/pkg/qdrantcfg"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"github.com/neerajvipparla/mcp-me/pkg/worker"
)

func main() {
	ctx := context.Background()

	// ── Config (non-secret) ──────────────────────────────────────────────────
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatal("config:", err)
	}

	// ── Secrets (env vars) ───────────────────────────────────────────────────
	qdrantAPIKey := os.Getenv("QDRANT_API_KEY")
	dbPassword := os.Getenv("DATABASE_PASSWORD")
	redisDSN := os.Getenv("REDIS_URL")
	if redisDSN == "" {
		redisDSN = "localhost:6379"
	}

	// ── Qdrant client ────────────────────────────────────────────────────────
	// From() selects CloudConfig (TLS + API key) or SelfHostedConfig (no TLS)
	// based solely on whether qdrantAPIKey is non-empty.
	qdrantClient, err := qdrantcfg.NewClient(
		qdrantcfg.From(cfg.Qdrant.ResolvedHost(), cfg.Qdrant.Port, qdrantAPIKey),
	)
	if err != nil {
		log.Fatal("qdrant:", err)
	}

	vs := store.NewDocumentStore(qdrantClient)

	// ── Postgres ─────────────────────────────────────────────────────────────
	pg, err := store.NewPostgresStore(ctx, cfg.Postgres.DSN(dbPassword))
	if err != nil {
		log.Fatal("postgres:", err)
	}

	// ── Shared chromedp allocator ────────────────────────────────────────────
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)
	defer cancelAlloc()
	chain := strategies.DefaultFetchChain(allocCtx)

	// ── Asynq worker ─────────────────────────────────────────────────────────
	asynqSrv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisDSN},
		asynq.Config{Concurrency: cfg.Worker.Concurrency},
	)
	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TaskCrawlPipeline, worker.NewPipelineHandler(pg, vs, chain).ProcessTask)
	go func() {
		if err := asynqSrv.Run(mux); err != nil {
			log.Fatal("asynq worker:", err)
		}
	}()

	queue := asynq.NewClient(asynq.RedisClientOpt{Addr: redisDSN})

	// ── HTTP server ───────────────────────────────────────────────────────────
	r := gin.Default()
	r.SetTrustedProxies(nil)

	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	v1 := r.Group("/v1")
	{
		// Public — no platform key required
		v1.POST("/register", api.NewRegisterHandler(pg).Register)

		// Protected — platform key required (pg narrowed to UserDB for auth,
		// CrawlDB for the handler)
		authed := v1.Group("/", api.PlatformKeyAuth(pg))
		crawlHandler := api.NewCrawlHandler(pg, vs, queue, cfg.Server.Host)
		authed.POST("/crawl", crawlHandler.PostCrawl)
		authed.GET("/crawl/:id", crawlHandler.GetStatus)

		// MCP endpoint — authenticates via mcp_api_key (bcrypt, per session)
		tools := mcp.NewTools(vs, pg, chain)
		v1.POST("/mcp/:crawl_id", mcp.NewServer(tools, pg).Handle)
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("listening %s | qdrant: %T | embedder: %s", addr, qdrantcfg.From(cfg.Qdrant.ResolvedHost(), cfg.Qdrant.Port, qdrantAPIKey), vs.EmbedderID())

	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("http server:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	asynqSrv.Shutdown()

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Fatal("forced shutdown:", err)
	}
	log.Println("stopped")
}
