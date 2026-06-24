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
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	"github.com/neerajvipparla/mcp-me/pkg/api"
	"github.com/neerajvipparla/mcp-me/pkg/config"
	"github.com/neerajvipparla/mcp-me/pkg/crawler/strategies"
	"github.com/neerajvipparla/mcp-me/pkg/mcp"
	"github.com/neerajvipparla/mcp-me/pkg/qdrantcfg"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"github.com/neerajvipparla/mcp-me/pkg/worker"
)

// fatal logs the error via ion, flushes ClickHouse, then exits.
// Use instead of log.Fatal so buffered logs are never lost on startup failures.
func fatal(msg string, err error) {
	ctx := context.Background()
	logging.Get(logging.TopicServer).Error(ctx, msg, err,
		ion.String("file", "main.go"),
		ion.String("func", "main"),
	)
	logging.NewAsyncLogger().Shutdown()
	os.Exit(1)
}

func main() {
	ctx := context.Background()
	logger := logging.Get(logging.TopicServer)

	// ── Config (non-secret) ──────────────────────────────────────────────────
	cfg, err := config.Load("config.yaml")
	if err != nil {
		fatal("config load failed", err)
	}
	logger.Info(ctx, "config loaded",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
		ion.String("port", fmt.Sprintf("%d", cfg.Server.Port)),
		ion.String("host", cfg.Server.ResolvedHost()),
	)
	if h := cfg.Server.ResolvedHost(); strings.Contains(h, "localhost") || strings.Contains(h, "127.0.0.1") {
		logger.Warn(ctx, "host is localhost — mcp_endpoint and claude_md URLs will be wrong in production; set SERVER_HOST env var",
			ion.String("file", "main.go"),
			ion.String("func", "main"),
			ion.String("host", h),
		)
	}

	// ── Secrets (env vars) ───────────────────────────────────────────────────
	qdrantAPIKey := os.Getenv("QDRANT_API_KEY")
	dbPassword := os.Getenv("DATABASE_PASSWORD")
	redisOpt, err := redisConnOpt()
	if err != nil {
		fatal("redis config invalid", err)
	}

	// ── Qdrant client ────────────────────────────────────────────────────────
	// From() selects CloudConfig (TLS + API key) or SelfHostedConfig (no TLS)
	// based solely on whether qdrantAPIKey is non-empty.
	qdrantCfg := qdrantcfg.From(cfg.Qdrant.ResolvedHost(), cfg.Qdrant.Port, qdrantAPIKey)
	qdrantClient, err := qdrantcfg.NewClient(qdrantCfg)
	if err != nil {
		fatal("qdrant connect failed", err)
	}
	logger.Info(ctx, "qdrant connected",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
		ion.String("mode", fmt.Sprintf("%T", qdrantCfg)),
	)

	vs := store.NewDocumentStore(qdrantClient)

	// ── Postgres ─────────────────────────────────────────────────────────────
	pg, err := store.NewPostgresStore(ctx, cfg.Postgres.DSN(dbPassword))
	if err != nil {
		fatal("postgres connect failed", err)
	}
	logger.Info(ctx, "postgres connected",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
	)

	// ── Shared chromedp allocator ────────────────────────────────────────────
	// --no-sandbox: required when the process runs as root (Docker default).
	// --disable-dev-shm-usage: Docker limits /dev/shm to 64MB by default; this
	//   makes Chrome use /tmp instead, preventing crashes on memory-heavy pages.
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	chain := strategies.DefaultFetchChain(allocCtx)

	// ── Asynq worker ─────────────────────────────────────────────────────────
	asynqSrv := asynq.NewServer(
		redisOpt,
		asynq.Config{Concurrency: cfg.Worker.Concurrency},
	)
	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TaskCrawlPipeline, worker.NewPipelineHandler(pg, vs, chain).ProcessTask)
	go func() {
		if err := asynqSrv.Run(mux); err != nil {
			fatal("asynq worker crashed", err)
		}
	}()
	logger.Info(ctx, "worker started",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
		ion.String("concurrency", fmt.Sprintf("%d", cfg.Worker.Concurrency)),
	)

	queue := asynq.NewClient(redisOpt)

	// ── HTTP server ───────────────────────────────────────────────────────────
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:3000"
	}
	r.Use(api.CORS(frontendURL), api.IonRecovery(), api.IonLogger())
	r.SetTrustedProxies(nil)

	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	v1 := r.Group("/v1")
	{
		// Public — no platform key required
		v1.POST("/register", api.NewRegisterHandler(pg).Register)
		ghAuth := api.NewGitHubAuthHandler(pg)
		v1.POST("/auth/github", ghAuth.GitHubLogin)
		v1.POST("/auth/github/rotate", ghAuth.RotateKey)

		// Protected — platform key required (pg narrowed to UserDB for auth,
		// CrawlDB for the handler)
		authed := v1.Group("/", api.PlatformKeyAuth(pg))
		crawlHandler := api.NewCrawlHandler(pg, vs, queue, cfg.Server.ResolvedHost())
		authed.POST("/crawl", crawlHandler.PostCrawl)
		authed.GET("/crawl/:id", crawlHandler.GetStatus)
		authed.GET("/crawls", crawlHandler.ListCrawls)

		// MCP endpoint — authenticates via mcp_api_key (bcrypt, per session)
		tools := mcp.NewTools(vs, pg, chain, queue, cfg.Server.ResolvedHost())
		v1.POST("/mcp/:crawl_id", mcp.NewServer(tools, pg).Handle)
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info(ctx, "server listening",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
		ion.String("addr", addr),
		ion.String("embedder", vs.EmbedderID()),
	)

	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("http server crashed", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info(ctx, "shutdown signal received",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
	)
	asynqSrv.Shutdown()

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Error(ctx, "forced shutdown", err,
			ion.String("file", "main.go"),
			ion.String("func", "main"),
		)
	}
	logger.Info(ctx, "server stopped",
		ion.String("file", "main.go"),
		ion.String("func", "main"),
	)
	logging.NewAsyncLogger().Shutdown()
}

// redisConnOpt builds an Asynq Redis connection option from REDIS_URL.
// Supports full URIs (redis://user:pass@host:port, rediss:// for TLS)
// as well as bare host:port for local dev.
func redisConnOpt() (asynq.RedisConnOpt, error) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		return asynq.RedisClientOpt{Addr: "localhost:6379"}, nil
	}
	return asynq.ParseRedisURI(url)
}
