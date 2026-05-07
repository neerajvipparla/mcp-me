# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

**DocsMCP** — a hosted Go service that crawls any public documentation URL, embeds it into a vector store, and exposes a personal MCP endpoint for Claude/Cursor to query.

Flow: `URL → Crawl → Chunk → Embed → Qdrant → MCP Endpoint → Claude / Cursor`

## Commands

```bash
go build ./...                          # build
go run ./cmd/server/main.go             # run server (port 8080)
go test ./...                           # all tests
go test ./internal/crawler/... -run TestName   # single test
go vet ./...                            # lint
docker compose up                       # start Postgres (5432) + Redis (6379) + Qdrant (6333)
docker compose up -d                    # detached
```

Migrations use `golang-migrate`. Run after `docker compose up`.

## Directory Structure

```
cmd/server/main.go     # entrypoint, HTTP on :8080, /health → {ok:true}
internal/
  api/                 # HTTP handlers (REST endpoints)
  crawler/             # fallback chain orchestrator
  chunker/             # heading-aware text splitter
  embedder/            # OpenAI API calls
  store/               # Qdrant + Postgres clients
  mcp/                 # MCP server handler
  worker/              # Asynq job handlers
migrations/            # SQL files (golang-migrate)
docker-compose.yml
```

## Architecture

### Request Flow
1. `POST /crawl` → validate URL → check Redis cache → enqueue Asynq job → return `{job_id, api_key, mcp_endpoint}`
2. Worker: crawl → chunk → embed → upsert Qdrant → update Postgres + Redis status
3. `GET /mcp/:crawl_id` → auth Bearer token → `search_docs` / `get_page` tools → Qdrant query

### Crawler — Fallback Chain (in order)
1. Plain HTTP + `goquery` — static sites (Hugo, Jekyll)
2. `go-rod` headless Chromium — JS-heavy sites (Docusaurus, VitePress, Next.js); detect via low text/script ratio
3. Jina Reader API (`r.jina.ai/<url>`) — free, clean markdown
4. Firecrawl API — paid last resort

Crawler rules: check `sitemap.xml` first; stay same-domain; prioritize `/docs/`, `/api/`, `/guide/`, `/reference/`; hard cap 500 pages; max 5 concurrent requests per job; respect `robots.txt`; skip PDFs/changelogs/blogs.

### Chunker
- Split by heading hierarchy (h1 → h2 → h3); never split mid-code-block
- Target 400–600 tokens, ~50 token overlap; up to 800 for code-heavy pages
- Use `tiktoken-go` for token counting
- Each chunk: `{source_url, page_title, heading_path, crawl_id, chunk_index}`
- Fallback to size-based splitting for flat pages (no h2/h3)

### Embedder
- `text-embedding-3-small` (OpenAI), batch 100 chunks per call

### Vector Store — Qdrant
- One collection per normalized URL hash: `docs_{md5(normalized_url)[:12]}`
- Vector: 1536d, Cosine distance
- Payload: `{crawl_id, page_url, page_title, heading_path, text, chunk_index}`
- Cache hit → new `user_crawls` row pointing to existing collection, no re-embedding

### Job Queue — Asynq + Redis
- States: `queued → crawling → chunking → embedding → ready → failed`
- Status written to both Postgres and Redis; polling reads Redis first
- Max 3 retries with backoff

### MCP Server
- Route: `/mcp/:crawl_id`
- Auth: `Authorization: Bearer <mcp_api_key>` on every call
- Tools: `search_docs(query, top_k=5)` and `get_page(url)`

### Postgres Schema
```sql
users(id, email, platform_api_key, created_at)
crawls(id, url_raw, url_normalized, url_hash, status, page_count, chunk_count, qdrant_collection, created_at, ready_at)
user_crawls(id, user_id, crawl_id, mcp_api_key, created_at)  -- mcp_api_key stored bcrypt-hashed
crawl_pages(id, crawl_id, url, title, chunk_count, crawled_at)
```

`crawls` keyed on `url_hash` — shared across users. `user_crawls` holds each user's unique `mcp_api_key`.

## Known Pitfalls

- **Crawl scope explosion** — enforce same-domain + path prefix lock from day one or a single crawl will try to index the internet
- **go-rod memory** — each headless instance ~150MB; max 2 concurrent headless crawls; kill browser immediately after each page fetch
- **URL hash stability** — normalize before hashing: lowercase domain, strip `www`, remove trailing slash, drop query params
- **MCP API key** — store bcrypt-hashed in Postgres; send plaintext only once at creation; never log it

## MVP Scope (Do Not Add)
Re-crawl scheduling, multi-version docs, auth-walled sites, webhooks, frontend UI, custom embedding model.

## Tech Stack
| Component | Library |
|---|---|
| HTTP + MCP | `net/http` |
| Job queue | Asynq + Redis |
| Crawler S1 | `goquery` |
| Crawler S2 | `go-rod` |
| Content extract | `go-readability` |
| Token count | `tiktoken-go` |
| Embeddings | OpenAI `text-embedding-3-small` |
| Vector DB | Qdrant (self-hosted, port 6333) |
| Metadata DB | Postgres |
| Migrations | `golang-migrate` |
| Deploy | Fly.io |

## Build Order Rule
Do not write the MCP server until the crawler + embedder pipeline produces real, searchable results. Validate data quality first.
