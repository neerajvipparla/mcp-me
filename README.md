# mcp-me

> Crawl any documentation URL. Get a private MCP endpoint. Let Claude and Cursor answer questions directly from those docs — not from training data guesses.

---

## The Problem

Claude is a strong coding assistant, but its knowledge has a cutoff and gaps. When you're working with a library it barely knows — a new version, a niche framework, your company's internal API — it fills the gap with plausible-sounding guesses. You end up cross-referencing docs yourself, pasting long excerpts into the chat, or just catching hallucinated method signatures at runtime.

This happens constantly:

- The library released a major version after the training cutoff
- It's popular in a specific domain but not broadly represented in training data
- It's internal to your company and has never been on the public internet
- The API changed enough that older knowledge is actively wrong

## The Solution

mcp-me turns any documentation URL into a private MCP endpoint in a few minutes. Sign in with GitHub, paste a URL, wait for indexing, then drop the endpoint into Claude Code, Claude Desktop, or Cursor. From that point, when Claude answers questions about that library, it reads the actual docs — not guesses.

```
URL → discover → crawl → chunk → embed → MCP endpoint → Claude / Cursor
```

---

## Quick Start

### 1. Sign in

Go to the dashboard and sign in with GitHub. On first login, a **Platform API Key** is generated and shown once — copy it and keep it somewhere safe. You'll use it to authenticate MCP endpoints and direct API calls.

### 2. Index a documentation site

Paste any documentation root URL into the dashboard and click **Index docs →**. The crawl runs in the background through these status stages:

```
queued → crawling → chunking → embedding → ready
```

Typical times: small sites (~50 pages) under 2 minutes; large sites (~500 pages) 10–20 minutes.

If the URL was already crawled by another user on the same instance, you get back the existing index instantly — no re-embedding.

### 3. Add to Claude Code

When the crawl is ready, the dashboard shows the exact command to run:

```bash
claude mcp add docs-<hostname> \
  --transport http \
  https://mcp-me-production.up.railway.app/v1/mcp/<crawl_id> \
  --header "Authorization: Bearer <platform_api_key>"
```

### 4. Add to Claude Desktop

In `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "astro-docs": {
      "url": "https://mcp-me-production.up.railway.app/v1/mcp/<crawl_id>",
      "headers": {
        "Authorization": "Bearer <platform_api_key>"
      }
    }
  }
}
```

Restart Claude Desktop. The `search_docs`, `get_page`, and `add_page` tools appear automatically.

### 5. Add to Cursor

In Cursor Settings → MCP → Add server:

- **URL**: `https://mcp-me-production.up.railway.app/v1/mcp/<crawl_id>`
- **Header**: `Authorization: Bearer <platform_api_key>`

---

## Why This Improves Your Workflow

**Stop verifying Claude's answers against the docs yourself.** When Claude has the actual docs available as a tool, it cites real function signatures, real configuration keys, real error messages. You spend time building, not fact-checking.

**Works for anything with a URL.** Internal APIs your team built last month. A Rust crate with 300 GitHub stars. A SaaS platform's API reference that changed in the last release. If there's a public URL, mcp-me indexes it.

**No context window management.** You don't paste doc excerpts into the chat. The MCP tool does a hybrid semantic search over the full embedded documentation and returns only the relevant chunks. The context window stays clean.

**Persists across conversations.** Crawl once. Use the same endpoint in every conversation, every project, in Claude Desktop and Cursor simultaneously. The index stays live until you delete it.

**Handles docs Claude genuinely can't know.** Your internal design system, your team's database schema, your company's platform runbook — none of that is in any model's training data. mcp-me bridges that gap.

| Before | After |
|---|---|
| Claude guesses the API; you verify in the docs | Claude searches the docs; you code |
| You paste doc excerpts to ground Claude's answers | MCP tool fetches the relevant sections automatically |
| Claude confidently uses a deprecated method | Claude reads the current version of the docs |
| Your internal tooling is invisible to Claude | Internal docs are indexed and queryable |
| You switch tabs constantly to cross-reference | You stay in the editor |

---

## MCP Tools Reference

Claude calls these automatically. You can also call them directly via JSON-RPC if you want to inspect results or build tooling on top.

### `search_docs`

Hybrid semantic + keyword search over the crawled documentation. Uses MiniLM dense vectors and BM25 sparse retrieval fused with Reciprocal Rank Fusion — better recall than pure vector similarity.

```json
{ "query": "how to configure middleware in Astro", "top_k": 5 }
```

Returns the most relevant chunks with their page URL, heading path, and relevance score.

### `get_page`

Retrieve every indexed chunk from a specific documentation page. Use when you need the full content of one page rather than cross-page search results.

```json
{ "url": "https://docs.astro.build/en/guides/middleware/" }
```

### `add_page`

Fetch, chunk, embed, and add a page that wasn't in the original crawl. Useful for pages published after the crawl ran, or pages the crawler missed.

```json
{ "url": "https://docs.astro.build/en/guides/new-feature/" }
```

### `create_crawl`

Start a new collection for an entirely different documentation set from within an MCP session. The agent calls this autonomously when it needs to look up something outside the current collection's scope, then discovers the new endpoint via `list_crawls`.

```json
{ "url": "https://orm.drizzle.team/docs/overview" }
```

### `list_crawls`

Return all indexed collections for the account. Agents call this at session start to discover which documentation sets are available.

### `get_status`

Poll the status of any crawl. Use after `create_crawl` returns `queued` to know when it's ready.

```json
{ "crawl_id": "206f5137-..." }
```

---

## REST API

All endpoints are at `https://mcp-me-production.up.railway.app`. Authenticate with your platform API key:

```
X-API-Key: <platform_api_key>
# or
Authorization: Bearer <platform_api_key>
```

### Register (programmatic / CLI use)

If you're not using the web dashboard, you can register directly:

```bash
curl -X POST https://mcp-me-production.up.railway.app/v1/register \
  -H "Content-Type: application/json" \
  -d '{"email": "you@example.com"}'
```

```json
{ "api_key": "a3f9...", "email": "you@example.com" }
```

The `api_key` is shown once. Save it — it cannot be recovered.

### Submit a crawl

```bash
curl -X POST https://mcp-me-production.up.railway.app/v1/crawl \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <platform_api_key>" \
  -d '{"url": "https://docs.astro.build"}'
```

```json
{
  "crawl_id": "206f5137-...",
  "mcp_endpoint": "https://mcp-me-production.up.railway.app/v1/mcp/206f5137-...",
  "status": "queued"
}
```

### Poll status

```bash
curl https://mcp-me-production.up.railway.app/v1/crawl/206f5137-... \
  -H "X-API-Key: <platform_api_key>"
```

```json
{ "status": "ready", "page_count": 312, "chunk_count": 2840 }
```

### List your crawls

```bash
curl "https://mcp-me-production.up.railway.app/v1/crawls?page=1&limit=10" \
  -H "X-API-Key: <platform_api_key>"
```

```json
{
  "crawls": [{ "crawl_id": "...", "url": "...", "status": "ready", ... }],
  "page": 1,
  "limit": 10,
  "has_more": false
}
```

### List indexed pages for a crawl

```bash
curl https://mcp-me-production.up.railway.app/v1/crawl/206f5137-.../pages \
  -H "X-API-Key: <platform_api_key>"
```

---

## How It Works

### Discovery

mcp-me tries four strategies in order, stopping as soon as it finds pages:

1. **`llms.txt`** — checks `<scheme>://<host>/llms.txt` first; an emerging AI-crawler standard that lists a site's key pages as markdown links
2. **Sitemap** — tries `sitemap.xml` at common paths, follows recursive `<sitemapindex>` nesting, and additionally parses `Sitemap:` headers from `robots.txt`
3. **BFS link extraction** — same-domain breadth-first crawl capped at 500 pages
4. **Firecrawl bulk crawl** — last resort; only active when `FIRECRAWL_URL` is set; handles sites that block or require complex JavaScript interaction

### Fetching

Each discovered URL is fetched through a fallback chain, stopping at the first strategy that returns sufficient content:

1. **Plain HTTP + goquery** — fastest; handles static sites (Hugo, MkDocs, Jekyll, plain HTML)
2. **Headless Chromium (chromedp)** — triggered when the plain-HTTP response has too little text; handles JavaScript-rendered sites (Docusaurus, VitePress, Next.js)
3. **Firecrawl API** — paid last resort for sites that block headless browsers; only active when `FIRECRAWL_URL` is set

Up to 5 pages are fetched concurrently per crawl job.

### Chunking

Each page is split into 400–600 token chunks aligned to heading boundaries (h1 → h2 → h3), never breaking mid-code-block. Code-heavy sections allow up to 800 tokens. Token counting uses `tiktoken-go` (cl100k_base). Each chunk stores its source URL, heading path, crawl ID, and chunk index.

### Embedding and Search

Embeddings are generated server-side by Qdrant Cloud FastEmbed — no local model, no OpenAI calls:

- **Dense**: `sentence-transformers/all-minilm-l6-v2` (384 dimensions, cosine similarity)
- **Sparse**: `Qdrant/bm25` (IDF-weighted keyword retrieval)

Search runs two prefetch legs (dense + sparse) fused with Reciprocal Rank Fusion — consistently better recall than either leg alone.

### Job Queue

Crawl jobs run as Asynq tasks on Redis. Status is written to Postgres. Max 3 retries with backoff. Worker concurrency is configurable. A crawl that discovers zero indexable pages is marked `failed`.

```
POST /v1/crawl
  └─ Asynq task enqueued
      └─ Discover (llms.txt → sitemap → BFS → Firecrawl)
          └─ Fetch pool (5 concurrent)
              ├─ plainhttp
              ├─ chromedp    ← fallback
              └─ firecrawl   ← last resort
          └─ Chunk (heading-aware, 400–600 tokens)
          └─ Embed + Upsert (Qdrant FastEmbed)
          └─ status = ready
```

---

## Self-Hosting

The server is stateless. Postgres, Redis, and Qdrant hold all state.

### Prerequisites

- Go 1.21+
- Docker (for Postgres + Redis)
- [Qdrant Cloud](https://cloud.qdrant.io) cluster (free tier works) or self-hosted Qdrant
- A GitHub OAuth app (for the web dashboard)
- Node.js 18+ (for the Next.js frontend)

### 1. Clone and configure

```bash
git clone https://github.com/neerajvipparla/mcp-me
cd mcp-me
cp .env.example .env
```

### 2. Start dependencies

```bash
docker compose up -d     # Postgres (5432) + Redis (6379)
```

### 3. Run migrations

```bash
migrate -path migrations -database "$DATABASE_URL" up
```

### 4. Configure the Go backend

| Variable | Required | Purpose |
|---|---|---|
| `DATABASE_URL` | yes | Postgres connection string |
| `REDIS_URL` | yes | Redis URI (`redis://` or `rediss://` for TLS) |
| `SERVER_HOST` | yes | Public base URL baked into every `mcp_endpoint` (e.g. `https://your-api.railway.app`) |
| `QDRANT_HOST` | yes | Qdrant hostname (e.g. `abc123.cloud.qdrant.io`) |
| `QDRANT_API_KEY` | yes (cloud) | Qdrant Cloud API key; omit for self-hosted |
| `FRONTEND_URL` | yes | Next.js app URL for CORS (e.g. `https://your-app.vercel.app`) |
| `FIRECRAWL_URL` | optional | Firecrawl API key — enables last-resort JS crawler |
| `CLICKHOUSE_DSN` | optional | ClickHouse Cloud DSN for structured logs and OTel traces |

```bash
make dev    # reads .env, starts on :8080
```

```bash
curl http://localhost:8080/health
# → {"ok":true}
```

### 5. Configure the Next.js frontend

```bash
cd web
cp .env.example .env.local
```

| Variable | Required | Purpose |
|---|---|---|
| `NEXT_PUBLIC_API_URL` | yes | Go backend URL (e.g. `https://your-api.railway.app`) |
| `NEXT_PUBLIC_APP_URL` | yes | This app's public URL (e.g. `https://your-app.vercel.app`) |
| `BETTER_AUTH_URL` | yes | Same as `NEXT_PUBLIC_APP_URL` — used by Better Auth for redirect URLs |
| `BETTER_AUTH_SECRET` | yes | Random secret string for session signing |
| `GITHUB_CLIENT_ID` | yes | From your GitHub OAuth app |
| `GITHUB_CLIENT_SECRET` | yes | From your GitHub OAuth app |
| `DATABASE_URL` | yes | Same Postgres as the Go backend — Better Auth writes session/user tables here |

Create a GitHub OAuth app at `github.com/settings/developers`:
- **Homepage URL**: your app URL
- **Authorization callback URL**: `<NEXT_PUBLIC_APP_URL>/api/auth/callback/github`

```bash
npm install
npm run dev    # starts on :3000
```

---

## Tech Stack

| Component | Library |
|---|---|
| HTTP framework | `gin-gonic/gin` |
| MCP protocol | JSON-RPC 2.0 over HTTP |
| Job queue | `hibiken/asynq` + Redis |
| Auth | `better-auth` (GitHub OAuth) |
| Crawler S1 | `PuerkitoBio/goquery` |
| Crawler S2 | `chromedp/chromedp` (headless Chromium) |
| Crawler S3 | Firecrawl REST API |
| HTML → Markdown | `JohannesKaufmann/html-to-markdown` |
| Token counting | `pkoukk/tiktoken-go` |
| Embeddings | Qdrant server-side FastEmbed (MiniLM + BM25) |
| Vector DB | `qdrant/go-client` (gRPC) |
| Metadata DB | `jackc/pgx/v5` → Postgres |
| Migrations | `golang-migrate` |
| Logging + Tracing | `neerajvipparla/ion` → ClickHouse Cloud |
| Frontend | Next.js 14 (App Router) + Tailwind CSS |
| Deploy | Railway (Go) + Vercel (Next.js) |
