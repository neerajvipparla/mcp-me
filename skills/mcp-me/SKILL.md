---
name: mcp-me
description: Use when user asks about any library, SDK, API, or framework that has documentation online. Activates the full DocsMCP workflow — credential lookup, collection selection (description-first, subagent probe as fallback), MCP tool dispatch, and high-quality cited responses. Also use when user asks to "add docs", "scrape docs", "index a URL", or "create a new collection".
---

# DocsMCP Agent Guide

DocsMCP is a self-hosted documentation intelligence service. It crawls documentation websites, embeds the content into a vector store, and exposes an MCP endpoint you can query. Your job as an agent is to use this system so every answer you give about libraries and APIs is grounded in actual docs — not training data.

---

## What DocsMCP Solves

| Without DocsMCP | With DocsMCP |
|---|---|
| Hallucinated API signatures | Exact signatures from real docs |
| Stale training data | Docs as of last crawl |
| Vague "check the docs" answers | Cited chunks with source URLs |
| One blob of knowledge | Per-topic collections, scoped search |

**Use this skill whenever:** user asks how to use a library, what an API does, how to configure a framework, or asks you to read/index documentation.

---

## Credential Store — `.mcpme/`

All collection credentials live in `.mcpme/collections.json` in the project root. **Never committed to git.**

**Before writing this file, ensure `.gitignore` contains:**
```
.mcpme/
```

### `.mcpme/collections.json` schema

```json
{
  "version": "1",
  "collections": [
    {
      "id": "clickhouse-go",
      "description": "ClickHouse Go SDK — connecting, querying, batch inserts, compression, connection pooling",
      "crawl_id": "0d0381ef-7e59-48f6-8f0e-ea3d0b59e978",
      "mcp_endpoint": "https://your-server.example.com/v1/mcp/0d0381ef-7e59-48f6-8f0e-ea3d0b59e978",
      "bearer_token": "e0260c69...",
      "status": "ready",
      "created_at": "2026-05-24T11:30:00Z"
    }
  ]
}
```

| Field | Source | Notes |
|---|---|---|
| `id` | You choose | Short kebab-case slug |
| `description` | You write | **Be specific and keyword-rich** — this drives description matching |
| `crawl_id` | `/v1/crawl` response | UUID |
| `mcp_endpoint` | `/v1/crawl` response | Full URL |
| `bearer_token` | `/v1/crawl` → `mcp_api_key` | Same value, used as `Authorization: Bearer <token>` |
| `status` | response | `queued` → `crawling` → `chunking` → `embedding` → `ready` → `failed` |

> `bearer_token` = `mcp_api_key`. The crawl API calls it `mcp_api_key`; store it as `bearer_token` since that's how every HTTP call uses it.

---

## Collection Selection — Two-Phase Architecture

**This is the core decision loop.** Run this every time you need to query docs.

### Phase 1 — Description Matching (always runs first)

Read `.mcpme/collections.json`. For each collection, check: does the `description` clearly cover the user's query topic?

**Rules:**
- If **exactly one** collection's description matches → use it directly, skip Phase 2
- If **multiple** collections could match → move to Phase 2 with those candidates
- If **zero** collections match:
  - If the docs root URL is known or inferrable from the query → call `create_crawl` directly
  - If no URL is inferrable → ask the user: "I don't have docs for X. What's the root documentation URL?"

**Example:**
```
Query: "how to do batch inserts in ClickHouse with Go"
Collection descriptions:
  - "ClickHouse Go SDK — connecting, querying, batch inserts..."  ← clear match
  - "psycopg3 Python PostgreSQL adapter"                          ← no match

→ Use "clickhouse-go" directly. No subagent needed.
```

---

### Phase 2 — Subagent Probe (fallback only, runs when description matching is ambiguous)

**Trigger condition:** description matching returned 2+ candidate collections and you cannot confidently pick one.

Dispatch a subagent with the following prompt — fill in the variables:

```
You are a collection-probe subagent. Your only job is to find which documentation
collection is most relevant for a query.

Query: "{{USER_QUERY}}"

Collections to probe (call search_docs on each with top_k=1):
{{FOR EACH CANDIDATE:}}
- Collection ID: {{id}}
  MCP Endpoint: {{mcp_endpoint}}
  Bearer Token: {{bearer_token}}

Instructions:
1. Call search_docs(query="{{USER_QUERY}}", top_k=1) on EACH collection in PARALLEL.
2. Compare the top result score from each collection.
3. Return ONLY this JSON — nothing else:
{
  "winner": "<collection_id with highest score>",
  "score": <winning score as float>,
  "reason": "<one sentence>"
}
If all scores are below 0.4, set winner to "none".
```

**After subagent returns:**
- `winner` is a collection ID → use that collection, run `search_docs` with full `top_k`
- `winner` is `"none"` → no collection covers this topic → offer `create_crawl`

---

## Tools Reference

### `search_docs` — Semantic search

**Use for:** any question about library behavior, APIs, configuration, error messages.

```json
{ "query": "string", "top_k": 5 }
```

- Broad questions → `top_k: 8`
- Specific method/param → `top_k: 3`
- If results have score < 0.5 → rephrase once, retry
- If still empty → try `add_page` for a known URL, then search again
- If server returns "crawl not ready" error → collection is still processing; tell user and wait
- If server returns "crawl not found" error → collection failed or was deleted; offer `create_crawl`

---

### `get_page` — Full page retrieval

**Use for:** user pastes a specific URL, or you need full context of a page found via search.

```json
{ "url": "https://docs.example.com/specific/page" }
```

---

### `add_page` — Add a single URL to current collection

**Use for:** a specific page URL that isn't in the current collection but belongs to the same topic.

```json
{ "url": "https://docs.example.com/new-page" }
```

**Do NOT use when:** the URL is a different library or topic → use `create_crawl` instead.

---

### `create_crawl` — Create a new collection

**Use for:** a completely different topic that has no existing collection.

```json
{ "url": "https://docs.new-library.com" }
```

**Deciding between `add_page` and `create_crawl`:**
> Ask: "Would a developer searching for X in this collection be confused to find Y there?"
> Yes → `create_crawl`. No → `add_page`.

**After `create_crawl` — mandatory steps:**

1. Response contains: `crawl_id`, `mcp_endpoint`, `mcp_api_key`, `status`
2. Check `.gitignore` — ensure `.mcpme/` is listed. If not, add it first.
3. Write new entry to `.mcpme/collections.json` — create the file if it doesn't exist
4. Use a specific, keyword-rich `description` — this is what drives description matching in future sessions
5. Register the new MCP server:
   - **If running in Claude Code**: run `claude mcp add` automatically (see command below) — no need to ask the user
   - **Otherwise**: give the user the install command for their environment

```bash
# Claude Code
claude mcp add docsmcp-<id> \
  --transport http \
  <mcp_endpoint> \
  --header "Authorization: Bearer <bearer_token>"

# OpenCode (~/.opencode.toml)
[[mcp_servers]]
name = "docsmcp-<id>"
type = "http"
url = "<mcp_endpoint>"
[mcp_servers.headers]
Authorization = "Bearer <bearer_token>"

# Any agent (.mcp.json — also add to .gitignore)
{
  "mcpServers": {
    "docsmcp-<id>": {
      "type": "http",
      "url": "<mcp_endpoint>",
      "headers": { "Authorization": "Bearer <bearer_token>" }
    }
  }
}
```

6. If `status` is `queued`: tell user crawling is in progress, they can poll with `GET /v1/crawl/<crawl_id>`. States progress: `queued → crawling → chunking → embedding → ready`.
7. If `status` is `failed`: tell user the crawl failed and offer to retry by calling `create_crawl` again with the same URL.

---

## Full Decision Flow

```
User asks about library/API/framework
│
├─ Does .mcpme/collections.json exist?
│   └─ NO → Guide user through one-time setup (see below)
│
├─ Phase 1: Description matching
│   ├─ 1 clear match → search_docs on that collection
│   ├─ 0 matches → create_crawl(root_url) → write to collections.json
│   └─ 2+ candidates → Phase 2: dispatch subagent probe
│
├─ Phase 2: Subagent probe (parallel search_docs top_k=1 on each candidate)
│   ├─ winner found (score ≥ 0.4) → search_docs(full query) on winner
│   └─ no winner (all < 0.4) → create_crawl → write to collections.json
│
└─ Compose answer
    ├─ Cite page_url for every claim
    ├─ Synthesize multiple chunks — don't paste raw
    └─ State clearly when something is from docs vs training data
```

---

## Response Quality Rules

**Always:**
- Run `search_docs` before answering any library-specific question
- Cite sources: "According to [title](<page_url>)..."
- Synthesize chunks into a coherent answer — don't dump raw results
- State explicitly when you're using docs vs training knowledge

**Never:**
- Answer library API questions from training data when a collection exists
- Add unrelated content to an existing collection — scope matters
- Commit `.mcpme/` or `.mcp.json` to git
- Probe ALL collections on every query — description matching first, probe only on ambiguity

---

## One-Time Human Setup

Done once per server deployment by the human operator.

```bash
# 1. Register
curl -s -X POST http://localhost:8080/v1/register \
  -H "Content-Type: application/json" \
  -d '{"email": "you@example.com"}' | jq .
# → save api_key (shown once)

# 2. Crawl
curl -s -X POST http://localhost:8080/v1/crawl \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <api_key>" \
  -d '{"url": "https://docs.example.com"}' | jq .
# → save crawl_id and mcp_api_key (shown once)

# 3. Poll until ready
curl -s http://localhost:8080/v1/crawl/<crawl_id> \
  -H "X-API-Key: <api_key>" | jq .status
```

---

## Writing Good Descriptions

The `description` field is the single most important thing you control. Bad descriptions break Phase 1 matching and force unnecessary subagent probes.

| Bad | Good |
|---|---|
| "Go docs" | "ClickHouse Go SDK — NewConn, Query, Exec, PrepareBatch, AsyncInsert, connection options" |
| "Python database" | "psycopg3 Python PostgreSQL adapter — connect, execute, copy, async support, connection pool" |
| "API reference" | "Stripe Payments API — PaymentIntent, Checkout, Webhooks, Subscriptions, refunds, Go SDK" |

**Rule:** include the library name, language, and 5–8 specific concept/method keywords. Future-you (or another agent) must be able to match a user query to this description without calling any APIs.
