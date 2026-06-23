# DocsMCP

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

DocsMCP turns any documentation URL into a private MCP endpoint in a few minutes. Submit the URL, wait for the crawl to complete, drop the endpoint into Claude Desktop or Cursor. From that point, when Claude answers questions about that library, it's reading the actual docs — not guessing.

The flow is simple:

```
POST /v1/crawl  →  crawl runs in background  →  ready
                                                   ↓
                                         configure Claude / Cursor
                                                   ↓
                                     Claude calls search_docs automatically
                                     when it needs to answer about that library
```

---

## Why This Improves Your Workflow

**Stop verifying Claude's answers against the docs yourself.** When Claude has the actual docs available as a tool, it cites real function signatures, real configuration keys, real error messages. You spend time building, not fact-checking.

**Works for anything with a URL.** Internal APIs your team built last month. A Rust crate with 300 GitHub stars. A SaaS platform's API reference that changed in the last release. If there's a public URL, DocsMCP indexes it.

**No context window management.** You don't paste doc excerpts into the chat. The MCP tool does a semantic search against the full embedded documentation and returns only the relevant chunks. The context window stays clean.

**Persists across conversations.** Crawl once. Use the same endpoint in every conversation, every project, in Claude Desktop and Cursor simultaneously. The index stays live until you delete it.

**Handles docs Claude genuinely can't know.** Your internal design system, your team's database schema docs, your company's platform engineering runbook — none of that is in any model's training data. DocsMCP bridges that gap.

### What changes day-to-day

| Before | After |
|---|---|
| Claude guesses the API; you verify in the docs | Claude searches the docs; you code |
| You paste doc excerpts to ground Claude's answers | MCP tool fetches the relevant sections automatically |
| Claude confidently uses a deprecated method | Claude reads the current version of the docs |
| Your internal tooling is invisible to Claude | Internal docs are indexed and queryable |
| You switch tabs constantly to cross-reference | You stay in the editor |

---

## Using DocsMCP

### 1. Register

```bash
curl -X POST http://localhost:8080/v1/register \
  -H "Content-Type: application/json" \
  -d '{"email": "you@example.com"}'
```

```json
{
  "platform_api_key": "a3f9..."
}
```

Save the `platform_api_key`. You'll use it on every crawl request.

---

### 2. Submit a crawl

Point DocsMCP at any documentation root URL:

```bash
curl -X POST http://localhost:8080/v1/crawl \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <platform_api_key>" \
  -d '{"url": "https://docs.astro.build"}'
```

```json
{
  "crawl_id": "206f5137-...",
  "mcp_endpoint": "http://localhost:8080/v1/mcp/206f5137-...",
  "mcp_api_key": "b7e2...",
  "status": "queued"
}
```

**Save `mcp_endpoint` and `mcp_api_key`** — the API key is shown once and never stored in plaintext.

If the URL was already crawled by anyone else on this instance, you get back the existing index instantly (cache hit). No redundant re-embedding.

---

### 3. Wait for ready

```bash
curl http://localhost:8080/v1/crawl/<crawl_id> \
  -H "X-API-Key: <platform_api_key>"
```

```json
{ "status": "ready", "page_count": 312, "chunk_count": 2840 }
```

Status transitions: `queued → crawling → chunking → embedding → ready`

Typical crawl times: small docs (~50 pages) under 2 minutes; large docs (~500 pages) 10–20 minutes.

---

### 4. Configure Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "astro-docs": {
      "url": "http://localhost:8080/v1/mcp/<crawl_id>",
      "headers": {
        "Authorization": "Bearer <mcp_api_key>"
      }
    }
  }
}
```

Restart Claude Desktop. The `search_docs` and `get_page` tools appear automatically.

---

### 5. Configure Cursor

In Cursor settings → MCP → Add server:

- **URL**: `http://localhost:8080/v1/mcp/<crawl_id>`
- **Header**: `Authorization: Bearer <mcp_api_key>`

---

### Multiple documentation sets

Run a separate crawl for each documentation site. Add each as its own MCP server with a descriptive name:

```json
{
  "mcpServers": {
    "astro-docs":      { "url": "http://localhost:8080/v1/mcp/<id1>", ... },
    "drizzle-docs":    { "url": "http://localhost:8080/v1/mcp/<id2>", ... },
    "internal-api":    { "url": "http://localhost:8080/v1/mcp/<id3>", ... }
  }
}
```

Claude picks the right tool based on context.

---

## MCP Tools Reference

Claude calls these automatically. You can also call them directly via JSON-RPC.

### `search_docs`

Semantic search over the crawled documentation. Uses hybrid dense + sparse retrieval (MiniLM + BM25) fused with Reciprocal Rank Fusion for better recall than pure vector search.

```json
{ "query": "how to configure middleware in Astro", "top_k": 5 }
```

Returns the most relevant chunks with their page URL, heading path, and score.

### `get_page`

Retrieve every chunk from a specific documentation page. Use when you need the full content of one page rather than cross-page search results.

```json
{ "url": "https://docs.astro.build/en/guides/middleware/" }
```

### `add_page`

Fetch, chunk, embed, and add a page that wasn't in the original crawl. Useful for pages published after the crawl ran.

```json
{ "url": "https://docs.astro.build/en/guides/new-feature/" }
```

### `create_crawl`

Start a new collection for an entirely different documentation set. The agent calls this when it needs to look up something outside the current collection's scope, then gets back a new endpoint and key to reconfigure itself.

```json
{ "url": "https://orm.drizzle.team/docs/overview" }
```

---

## Setup

### Prerequisites

- Go 1.21+
- Docker (for Postgres + Redis + local Qdrant)
- A [Qdrant Cloud](https://cloud.qdrant.io) cluster (free tier works) — or use the local Docker instance
- A [ClickHouse Cloud](https://clickhouse.cloud) instance (optional, for structured logs and traces)

### 1. Clone and configure

```bash
git clone https://github.com/neerajvipparla/mcp-me
cd mcp-me
cp .env.example .env
```

Edit `.env`:

```env
QDRANT_HOST=<your-cluster>.cloud.qdrant.io
QDRANT_API_KEY=<your-qdrant-api-key>

DATABASE_URL=postgres://user:password@host:5432/db?sslmode=require

# Optional — structured logs and traces flow to ClickHouse
CLICKHOUSE_DSN=https://default:<password>@<host>:8443/default?secure=true

# Optional — enables Firecrawl as last-resort crawler for JS-heavy sites
FIRECRAWL_URL=fc-<your-key>
```

### 2. Start dependencies

```bash
docker compose up -d     # Postgres (5432) + Redis (6379)
```

### 3. Run migrations

```bash
# requires golang-migrate CLI
migrate -path migrations -database "$DATABASE_URL" up
```

### 4. Run the server

```bash
make dev          # reads .env automatically
# or
go run ./cmd/server/main.go
```

Server starts on `:8080`. Check with:

```bash
curl http://localhost:8080/health
# → {"ok":true}
```

---

## How It Works

DocsMCP uses a fallback crawler chain so it can handle both static and JavaScript-rendered documentation sites:

1. **Plain HTTP + goquery** — fastest; handles static sites (Hugo, MkDocs, Jekyll)
2. **Headless Chromium (chromedp)** — fallback for JS-heavy sites (Docusaurus, VitePress, Next.js); triggered when the plain-HTTP response has too little text
3. **Firecrawl API** — paid last resort for sites that block or require complex JS interaction

Discovery tries `sitemap.xml` first (fast, exhaustive), falls back to BFS link extraction capped at 500 pages.

Each crawled page is split into 400–600 token chunks aligned to heading boundaries, then embedded using Qdrant's server-side FastEmbed (MiniLM-L6 for dense vectors, BM25 for sparse). Search fuses both legs with Reciprocal Rank Fusion — better retrieval than pure vector similarity alone.

```
URL
 └─ Discover (sitemap → BFS)
     └─ Fetch pool (5 concurrent)
         ├─ plainhttp
         ├─ chromedp       ← fallback
         └─ firecrawl      ← last resort
     └─ Chunk (heading-aware, 400–600 tokens)
     └─ Embed + Upsert (Qdrant FastEmbed, MiniLM + BM25)
     └─ Ready
```

All operations are traced via OpenTelemetry spans — flushed to ClickHouse alongside structured logs. Every crawl job, every fetch, every chunk operation is observable.

---

## Deployment

The server is stateless. Postgres and Redis are the only stateful components; Qdrant holds the vectors.

Set the following environment variables on your host:

| Variable | Required | Purpose |
|---|---|---|
| `QDRANT_API_KEY` | yes (cloud) | Qdrant Cloud auth + TLS |
| `DATABASE_URL` | yes | Postgres connection string |
| `REDIS_URL` | yes | Redis URI (`redis://` or `rediss://` for TLS) |
| `SERVER_HOST` | yes | Public base URL baked into every `mcp_endpoint` (e.g. `https://your-app.railway.app`) |
| `CLICKHOUSE_DSN` | optional | ClickHouse Cloud DSN for structured logs |
| `FIRECRAWL_URL` | optional | Firecrawl API key — last-resort JS-heavy crawler |

Point Claude Desktop at `https://<your-host>/v1/mcp/<crawl_id>` instead of localhost.
