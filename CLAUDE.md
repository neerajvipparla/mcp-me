# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

**DocsMCP** — a Go service that crawls any public documentation URL, embeds it into Qdrant using server-side FastEmbed (MiniLM + BM25), and exposes a personal MCP endpoint for Claude/Cursor to query with hybrid semantic search.

Flow: `URL → Crawl → Chunk → Embed (Qdrant FastEmbed) → MCP Endpoint → Claude / Cursor`

## Commands

```bash
go build ./...                          # build
go run ./cmd/server/main.go             # run server (port 8080)
make dev                                # run server with .env loaded
go test ./...                           # all tests
go vet ./...                            # lint
docker compose up -d                    # Postgres (5432) + Redis (6379)
```

Migrations use `golang-migrate`. Run after `docker compose up`.

## Directory Structure

```
cmd/server/main.go          # entrypoint — wires all components, HTTP on :8080
pkg/
  api/                      # HTTP handlers: crawl.go, middleware.go, register.go
  chunker/chunker.go        # heading-aware text splitter (tiktoken, 400–600 tokens)
  config/                   # config.yaml loader
  crawler/
    strategies/             # chain.go, plainhttp.go, chromedp.go, firecrawl.go
    types/                  # Handler interface, CrawlPool, PageResult
    helper/                 # HTML → Markdown conversion
  discovery/discovery.go    # sitemap.xml + BFS link extraction
  mcp/                      # server.go (JSON-RPC), tools.go (search_docs, get_page, add_page, create_crawl, list_crawls, get_status)
  qdrantcfg/                # Qdrant client factory (cloud vs self-hosted)
  store/                    # document_store.go (Qdrant), postgres_store.go
  worker/pipeline.go        # Asynq task handler — orchestrates the full crawl pipeline
logging/
  constants.go              # topic constants: TopicServer, TopicAPI, TopicWorker, …
  ion_Builder.go            # singleton ion logger + named child loggers
  otelsetup/setup.go        # ion config (ClickHouse sink, console, file)
  config/                   # ClickHouse DSN constants
migrations/                 # SQL files (golang-migrate)
docs/
  tracing-plan.md           # OTel span inventory by module
```

## Architecture

### Request Flow
1. `POST /v1/crawl` → validate URL → check Postgres cache → enqueue Asynq job → return `{crawl_id, mcp_api_key, mcp_endpoint[, claude_md]}`
2. Worker: discover URLs → fetch pool (5 concurrent) → chunk → embed via Qdrant FastEmbed → upsert → update Postgres status
3. `GET /v1/crawl/:id` → poll status → returns `{status, page_count, chunk_count, mcp_endpoint}`
4. `GET /v1/crawls` → list all crawls for user → returns array with `mcp_endpoint` per entry
5. `POST /v1/mcp/:crawl_id` → auth Bearer token (bcrypt) → JSON-RPC dispatch → `search_docs` / `get_page` / `add_page` / `create_crawl` / `list_crawls` / `get_status`

### Crawler — Fallback Chain (in order)
1. **Plain HTTP + goquery** — static sites (Hugo, Jekyll, MkDocs); fast, no browser overhead
2. **Headless Chromium (chromedp)** — JS-heavy sites (Docusaurus, VitePress, Next.js); triggered when plain-HTTP text is below `minContentLength`
3. **Firecrawl API** — paid last resort; only active when `FIRECRAWL_URL` env var is set

Discovery checks `sitemap.xml` first; falls back to BFS link extraction. Same-domain only. Hard cap: 500 pages. Max 5 concurrent fetches per job.

### Chunker
- Split by heading hierarchy (h1 → h2 → h3); never split mid-code-block
- Target 400–600 tokens (~50 token overlap); up to 800 for code-heavy sections
- Token counting via `tiktoken-go` (cl100k_base encoding)
- Each chunk: `{source_url, heading_path, crawl_id, chunk_index}`
- Fallback to size-based splitting for flat pages with no headings

### Vector Store — Qdrant
- **Embeddings**: Qdrant Cloud server-side FastEmbed — no local model, no OpenAI calls
  - Dense: `sentence-transformers/all-minilm-l6-v2` (384d, cosine)
  - Sparse: `Qdrant/bm25` (IDF-weighted, server-side)
- **Search**: Two Prefetch legs (dense + sparse) fused with Reciprocal Rank Fusion (RRF)
- **Collection naming**: `docs_{url_hash[:12]}_{embedder_id}` — shared across users on the same instance
- Cache hit → new `user_crawls` row pointing to existing collection, no re-embedding

### Job Queue — Asynq + Redis
- States: `queued → crawling → chunking → embedding → ready → failed`
- Status written to Postgres; Asynq uses Redis internally
- Max 3 retries with backoff; worker concurrency configured in `config.yaml`

### MCP Server (JSON-RPC 2.0)
- Route: `POST /v1/mcp/:crawl_id`
- Auth: `Authorization: Bearer <mcp_api_key>` verified with bcrypt on every request
- Tools: `search_docs(query, top_k=5)`, `get_page(url)`, `add_page(url)`, `create_crawl(url)`, `list_crawls()`, `get_status(crawl_id?)`
- Also handles MCP protocol: `initialize`, `notifications/initialized`, `tools/list`, `tools/call`
- `list_crawls` — returns all indexed collections for the account; call at session start to discover available docs
- `get_status` — polls any crawl_id for status; use after `create_crawl` returns `queued` to wait for `ready`

### Postgres Schema
```sql
users(id, email, platform_api_key, created_at)                               -- platform_api_key SHA-256 hashed
crawls(id, url_raw, url_normalized, url_hash, status, embedder_id,
       page_count, chunk_count, qdrant_collection, created_at, ready_at)
user_crawls(id, user_id, crawl_id, mcp_api_key, created_at)                  -- mcp_api_key bcrypt hashed
crawl_pages(id, crawl_id, url, title, chunk_count, crawled_at)
```

### Logging & Tracing — ion → ClickHouse
- **Library**: `github.com/neerajvipparla/ion` — OTel-based structured logger with ClickHouse sink
- **Singleton**: `logging.NewAsyncLogger()` via `sync.Once`; child loggers via `logging.Get(topic)`
- **Topics**: `TopicServer`, `TopicAPI`, `TopicWorker`, `TopicCrawler`, `TopicDiscovery`, `TopicChunker`, `TopicStore`, `TopicMCP`
- **Log format**: `logger.Info(ctx, "message", ion.String("key", "value"), ...)` — always include `file` and `func` as separate `ion.String` attributes
- **Errors**: `logger.Error(ctx, "descriptive message", err, ion.String(...))` — `err` is the 3rd positional arg, not a field
- **Tracing**: `logger.Tracer("scope")` → `tracer.Start(ctx, "span.name")` → OTel spans flushed to ClickHouse alongside logs
- **Shutdown**: `logging.NewAsyncLogger().Shutdown()` at process exit (5s timeout); drains ClickHouse batch buffer

Span coverage per tier — see `docs/tracing-plan.md` for the full inventory:
- **Tier 1**: `pipeline`, `store.upsert`, `store.search`, `fetch.plainhttp`, `fetch.chromedp`, `fetch.firecrawl`
- **Tier 2**: `discovery`, `mcp.search_docs`, `mcp.get_page`, `mcp.add_page`, `mcp.create_crawl`, `chunker.split`
- **Tier 3**: `api.post_crawl`, `api.get_status`, `pool.fetch_all`

## Environment Variables

All secrets and deployment-specific overrides live in `.env`. Non-secret settings live in `config.yaml`.

| Variable | Required in prod | Purpose |
|---|---|---|
| `QDRANT_API_KEY` | yes (cloud) | Enables TLS + auth for Qdrant Cloud; omit for self-hosted |
| `DATABASE_PASSWORD` | yes | Postgres password (used when `DATABASE_URL` is not set) |
| `DATABASE_URL` | optional | Full Postgres DSN — overrides `config.yaml` host/port/db/user |
| `QDRANT_HOST` | optional | Overrides `config.yaml` qdrant.host (e.g. Qdrant Cloud hostname) |
| `SERVER_HOST` | **yes in prod** | Public base URL (e.g. `https://api.example.com`) — baked into every `mcp_endpoint` and `claude_md` field. Defaults to `config.yaml` server.host (`http://localhost:8080`). Server logs a startup warning if the resolved value contains `localhost`. |
| `REDIS_URL` | yes in prod | Full Redis URI — `redis://user:pass@host:port` or `rediss://` for TLS. Parsed via `asynq.ParseRedisURI`. Defaults to `localhost:6379` bare address if unset. |
| `FIRECRAWL_URL` | optional | Firecrawl API key — enables the paid last-resort crawler strategy |

## Known Pitfalls

- **`SERVER_HOST` not set in production** — all `mcp_endpoint` and `claude_md` URLs will point to `localhost:8080`. Server emits a `Warn` log at startup if the host looks like localhost. Fix: set `SERVER_HOST=https://your-domain.com` in `.env`.
- **Crawl scope explosion** — `Filter` in `pkg/discovery/` enforces same-domain + path prefix. Don't weaken it.
- **chromedp memory** — each headless context ~150MB; max 2 concurrent headless crawls; `taskCancel()` is called immediately after each page fetch
- **URL hash stability** — normalize before hashing: lowercase domain, strip `www`, remove trailing slash, drop query params. Done in `store.HashURL()`.
- **MCP API key** — bcrypt-hashed in Postgres; plaintext returned exactly once; never log it. Platform API key is SHA-256 (deterministic, lookupable).
- **`ion.Tracer()` returns noop when tracing disabled** — safe to call always; logs a one-time warning
- **`list_crawls` user lookup** — `userID` is resolved once during MCP auth (`GetUserCrawlByCrawlID`) and threaded into `callTool` → `ListCrawls`. Do not re-fetch it inside `ListCrawls`; the parameter is `userID string`, not `crawlID string`.

## Coding Rules (from prior feedback)

- **Never run git operations** unless explicitly asked
- **Never guard ion logger calls with `if logger != nil`** — call directly
- **Log message format**: short category as message (`"cache hit"`), detail as named attrs (`ion.String("type", "subpage match")`)
- **Errors**: descriptive message (`"auth failed: missing api key"`), not just the error string
- **No backward-compat shims** — clear data and start fresh instead of migration hacks
- **No `NewAsyncLogger()` outside `logging/`** — use `logging.Get(topic)` everywhere else

## MVP Scope (Do Not Add)
Re-crawl scheduling, multi-version docs, auth-walled sites, webhooks, frontend UI, custom embedding model selection per crawl.

## Tech Stack
| Component | Library |
|---|---|
| HTTP framework | `gin-gonic/gin` |
| MCP protocol | JSON-RPC 2.0 over HTTP (stdlib) |
| Job queue | `hibiken/asynq` + Redis |
| Crawler S1 | `PuerkitoBio/goquery` |
| Crawler S2 | `chromedp/chromedp` (headless Chromium) |
| Crawler S3 | Firecrawl REST API |
| HTML→Markdown | `JohannesKaufmann/html-to-markdown` |
| Token count | `pkoukk/tiktoken-go` |
| Embeddings | Qdrant server-side FastEmbed (MiniLM + BM25) |
| Vector DB | `qdrant/go-client` (gRPC) |
| Metadata DB | `jackc/pgx/v5` → Postgres |
| Migrations | `golang-migrate` |
| Logging + Tracing | `neerajvipparla/ion` → ClickHouse Cloud |
| Deploy | Fly.io |
