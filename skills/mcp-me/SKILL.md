---
name: mcp-me
description: Use when user asks about any library, SDK, API, or framework that has documentation online. Also use when user asks to "add docs", "index a URL", or "search docs". The agent handles everything via MCP — key lookup, collection discovery, crawling, polling, and searching. Human only needs to provide their API key once.
---

# mcp-me Agent Guide

mcp-me crawls documentation websites, embeds them into a vector store, and exposes MCP endpoints for semantic search. Everything is done through MCP calls — no curl, no REST, no manual steps.

**Never ask the human for a crawl_id, mcp_endpoint, or to run any command. Never use curl. Use MCP tools for everything.**

---

## Two MCP Endpoints

| Endpoint | Registered as | Tools available |
|---|---|---|
| `POST /v1/mcp` | `mcp-me` (permanent, one-time setup) | `list_crawls`, `create_crawl`, `get_status` |
| `POST /v1/mcp/<crawl_id>` | `mcp-me-<id>` (per collection) | `search_docs`, `get_page`, `add_page` |

The account endpoint (`mcp-me`) is registered once and never changes. Collection endpoints are registered automatically when a new crawl is ready.

---

## Step 0 — Resolve the API Key

Check in this order:
1. `.mcpme/collections.json` → `api_key` field
2. `CLAUDE.md` → any `Authorization: Bearer <key>` line
3. If not found → ask the human **once**: "What's your mcp-me API key?" Write it to `.mcpme/collections.json` immediately.

---

## Step 1 — Discover Existing Collections

Call `list_crawls` on the `mcp-me` account MCP (already registered):

```
mcp-me: list_crawls()
```

Returns all collections: `crawl_id`, `url`, `status`, `page_count`, `chunk_count`, `mcp_endpoint`.

Merge with `.mcpme/collections.json` silently:
- Server + local match → update `status`, counts, `mcp_endpoint`. Keep local `description`.
- Server only → add entry, infer `description` from URL.
- Local only → mark `status: "failed"`, exclude from matching.

Write merged state back to `.mcpme/collections.json`.

---

## Step 2 — Select or Create a Collection

### Description matching

For each `ready` collection, check if `description` covers the user's query:
- **One match** → go to Step 3
- **Multiple candidates** → call `search_docs` on each in parallel (top_k=1), pick highest score
- **No match** → create a new collection

### Inferring docs URLs

| User says | Root URL to crawl |
|---|---|
| "Next.js" | `https://nextjs.org/docs` |
| "Tailwind CSS" | `https://tailwindcss.com/docs` |
| "Better Auth" | `https://www.better-auth.com/docs` |
| "Gin framework" | `https://gin-gonic.com/docs` |
| "Qdrant" | `https://qdrant.tech/documentation` |
| Unknown | Use training knowledge for the official docs URL |

If URL is genuinely unknown → ask the human once.

### Creating a new collection

```
mcp-me: create_crawl(url="https://docs.example.com")
```

Response: `{ "crawl_id": "...", "mcp_endpoint": "...", "status": "queued" | "ready" }`

Write to `.mcpme/collections.json` immediately:
```json
{
  "version": "1",
  "api_key": "<api_key>",
  "collections": [
    {
      "id": "<kebab-slug>",
      "description": "<library name — methods, concepts, keywords>",
      "crawl_id": "<uuid>",
      "mcp_endpoint": "<url>",
      "status": "queued",
      "created_at": "<iso timestamp>"
    }
  ]
}
```

Tell the human: "Crawling <url> — checking back every 15s until ready." Then poll.

### Polling

```
mcp-me: get_status(crawl_id="<uuid>")
```

Poll every 15s. States: `queued → crawling → chunking → embedding → ready → failed`.

Once `ready`:
1. Update `.mcpme/collections.json`
2. Register the collection MCP:
   ```
   claude mcp add mcp-me-<id> --transport http <mcp_endpoint> --header "Authorization: Bearer <api_key>"
   ```
3. Proceed to Step 3.

If `failed` → retry `create_crawl` once. If fails again, tell the human.

---

## Step 3 — Search and Answer

Use the registered collection MCP directly:

```
mcp-me-<id>: search_docs(query="<user query>", top_k=5)
```

- Broad question → `top_k: 8`
- Specific method/param → `top_k: 3`
- Score < 0.5 → rephrase and retry once
- Still empty → `add_page` for a specific known URL, then search again

### All MCP tools

| Tool | Endpoint | When to use |
|---|---|---|
| `list_crawls` | `mcp-me` | Session start — discover collections |
| `create_crawl` | `mcp-me` | New docs URL with no existing collection |
| `get_status` | `mcp-me` | Poll after `create_crawl` returns `queued` |
| `search_docs` | `mcp-me-<id>` | Any library question |
| `get_page` | `mcp-me-<id>` | Retrieve full page by URL |
| `add_page` | `mcp-me-<id>` | Add a missing page to existing collection |

---

## Step 4 — Compose the Answer

- Cite every claim: "According to [title](<source_url>)..."
- Synthesize chunks — don't dump raw results
- State when using docs vs training knowledge
- Update `description` in `.mcpme/collections.json` with keywords learned from results

---

## `.mcpme/collections.json` Schema

```json
{
  "version": "1",
  "api_key": "<platform_api_key>",
  "collections": [
    {
      "id": "better-auth",
      "description": "Better Auth — betterAuth() config, adapters, signUp, signIn, getSession, GitHub OAuth, API keys, session management",
      "crawl_id": "b8e66685-2095-4f4a-a57b-0e4a130052ef",
      "mcp_endpoint": "https://mcp-me-production.up.railway.app/v1/mcp/b8e66685-2095-4f4a-a57b-0e4a130052ef",
      "status": "ready",
      "created_at": "2026-06-24T00:00:00Z"
    }
  ]
}
```

Always ensure `.gitignore` contains `.mcpme/` before writing this file.

---

## One-Time Setup (human does once, ever)

1. Go to **https://mcp-me-two.vercel.app** → sign in with GitHub → copy API key
2. Register the permanent account MCP:
   ```bash
   claude mcp add mcp-me \
     --transport http \
     https://mcp-me-production.up.railway.app/v1/mcp \
     --header "Authorization: Bearer <api_key>"
   ```
3. Create `.mcpme/collections.json` with the api_key

After that: just ask about any library. The agent handles everything.

---

## What the Human Does vs What You Do

| Task | Human | Agent |
|---|---|---|
| Sign in, get API key | ✓ once | — |
| Register `mcp-me` account MCP | ✓ once | — |
| Ask about a library | ✓ | — |
| Everything else | — | ✓ automatic via MCP |
