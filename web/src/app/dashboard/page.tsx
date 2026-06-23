"use client"

import { useEffect, useState, useCallback, useRef } from "react"
import { authClient } from "@/lib/auth-client"
import { useRouter } from "next/navigation"

const API = process.env.NEXT_PUBLIC_API_URL ?? "https://mcp-me-production.up.railway.app"

type Status = "queued" | "crawling" | "chunking" | "embedding" | "ready" | "failed"

interface Collection {
  crawl_id: string
  url: string
  status: Status
  page_count: number
  chunk_count: number
  mcp_endpoint: string
  crawled_at: string
}

const STATUS_STYLES: Record<Status, string> = {
  queued:    "text-tx-muted border-border bg-surface",
  crawling:  "text-accent border-accent/40 bg-accent-dim",
  chunking:  "text-accent border-accent/40 bg-accent-dim",
  embedding: "text-accent border-accent/40 bg-accent-dim",
  ready:     "text-ready border-ready/40 bg-ready-dim",
  failed:    "text-red-400 border-red-400/30 bg-red-400/10",
}

const STATUS_DOT: Record<Status, string> = {
  queued:    "bg-tx-muted",
  crawling:  "bg-accent animate-pulse",
  chunking:  "bg-accent animate-pulse",
  embedding: "bg-accent animate-pulse",
  ready:     "bg-ready",
  failed:    "bg-red-400",
}

function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <button
      onClick={copy}
      className="text-xs px-2.5 py-1 rounded-md border border-border text-tx-muted hover:text-tx hover:border-accent/40 transition-all duration-150 font-mono"
    >
      {copied ? "✓ Copied" : label}
    </button>
  )
}

function CollectionCard({ col, onPoll }: { col: Collection; onPoll: () => void }) {
  const isActive = ["queued", "crawling", "chunking", "embedding"].includes(col.status)
  const endpointCmd = `claude mcp add docs-${new URL(col.url).hostname.replace(/\./g, "-")} --transport http ${col.mcp_endpoint} --header "Authorization: Bearer <mcp_api_key>"`

  // Poll while active
  useEffect(() => {
    if (!isActive) return
    const id = setInterval(onPoll, 8000)
    return () => clearInterval(id)
  }, [isActive, onPoll])

  return (
    <div className="rounded-xl border border-border bg-surface hover:border-border/80 transition-all duration-200 group overflow-hidden">
      {/* Header */}
      <div className="px-5 py-4 flex items-start justify-between gap-4 border-b border-border">
        <div className="min-w-0">
          <p className="text-sm font-mono text-tx truncate">{col.url}</p>
          <p className="text-xs text-tx-muted mt-0.5">{new Date(col.crawled_at).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })}</p>
        </div>
        <span className={`shrink-0 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-xs font-medium ${STATUS_STYLES[col.status]}`}>
          <span className={`w-1.5 h-1.5 rounded-full ${STATUS_DOT[col.status]}`} />
          {col.status}
        </span>
      </div>

      {/* Stats */}
      {col.status === "ready" && (
        <div className="px-5 py-3 flex items-center gap-6 border-b border-border">
          <div>
            <p className="text-xs text-tx-muted">Pages</p>
            <p className="text-base font-semibold text-tx font-mono">{col.page_count.toLocaleString()}</p>
          </div>
          <div>
            <p className="text-xs text-tx-muted">Chunks</p>
            <p className="text-base font-semibold text-tx font-mono">{col.chunk_count.toLocaleString()}</p>
          </div>
        </div>
      )}

      {/* Endpoint */}
      {col.status === "ready" && (
        <div className="px-5 py-4">
          <p className="text-xs text-tx-muted mb-2">Add to Claude Code</p>
          <div className="flex items-start gap-2">
            <div className="flex-1 min-w-0 rounded-lg bg-code border border-border px-3 py-2">
              <p className="text-xs font-mono text-tx-muted truncate">{endpointCmd}</p>
            </div>
            <CopyButton text={endpointCmd} label="Copy cmd" />
          </div>
        </div>
      )}

      {/* In-progress bar */}
      {isActive && (
        <div className="px-5 py-4">
          <div className="h-1 bg-border rounded-full overflow-hidden">
            <div className="h-full w-1/3 bg-accent rounded-full animate-crawl" />
          </div>
          <p className="text-xs text-tx-muted mt-2 font-mono animate-pulse capitalize">{col.status}…</p>
        </div>
      )}
    </div>
  )
}

function AddCollectionForm({
  apiKey,
  onSuccess,
}: {
  apiKey: string
  onSuccess: (col: Collection) => void
}) {
  const [url, setUrl] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!url.trim()) return
    setLoading(true)
    setError("")
    try {
      const res = await fetch(`${API}/v1/crawl`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-API-Key": apiKey },
        body: JSON.stringify({ url }),
      })
      const data = await res.json()
      if (!res.ok) throw new Error(data.error ?? "Request failed")
      onSuccess({
        crawl_id: data.crawl_id,
        url,
        status: data.status,
        page_count: 0,
        chunk_count: 0,
        mcp_endpoint: data.mcp_endpoint,
        crawled_at: new Date().toISOString(),
      })
      setUrl("")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong")
    } finally {
      setLoading(false)
    }
  }

  return (
    <form onSubmit={submit} className="rounded-xl border border-dashed border-border hover:border-accent/40 bg-surface transition-all duration-200 p-5">
      <p className="text-xs text-tx-muted mb-3 font-mono">New collection</p>
      <div className="flex gap-2">
        <input
          type="url"
          value={url}
          onChange={e => setUrl(e.target.value)}
          placeholder="https://docs.example.com"
          required
          className="flex-1 bg-code border border-border rounded-lg px-3 py-2.5 text-sm font-mono text-tx placeholder:text-tx-faint outline-none focus:border-accent transition-colors"
        />
        <button
          type="submit"
          disabled={loading || !url.trim()}
          className="px-4 py-2.5 rounded-lg bg-accent hover:bg-accent-light text-white text-sm font-medium transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed whitespace-nowrap"
        >
          {loading ? (
            <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin inline-block" />
          ) : (
            "Index docs →"
          )}
        </button>
      </div>
      {error && <p className="mt-2 text-xs text-red-400">{error}</p>}
    </form>
  )
}

export default function DashboardPage() {
  const router = useRouter()
  const [session, setSession] = useState<{ user: { email: string; name: string } } | null>(null)
  const [apiKey, setApiKey] = useState("")
  const [keyVisible, setKeyVisible] = useState(false)
  const [collections, setCollections] = useState<Collection[]>([])
  const [loading, setLoading] = useState(true)
  const fetchedKey = useRef(false)

  // Auth check
  useEffect(() => {
    authClient.getSession().then(({ data }) => {
      if (!data?.session) {
        router.push("/login")
        return
      }
      setSession(data as any)
    })
  }, [router])

  // Fetch API key from Go backend (after OAuth, exchange GitHub token for DocsMCP key)
  const fetchApiKey = useCallback(async (githubToken: string) => {
    if (fetchedKey.current) return
    fetchedKey.current = true
    try {
      const res = await fetch(`${API}/v1/auth/github`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ github_token: githubToken }),
      })
      const data = await res.json()
      if (data.api_key) setApiKey(data.api_key)
    } catch {
      /* silent — user may not have token yet */
    }
  }, [])

  // Load collections
  const loadCollections = useCallback(async (key: string) => {
    if (!key) return
    try {
      const res = await fetch(`${API}/v1/crawls`, {
        headers: { "X-API-Key": key },
      })
      const data = await res.json()
      if (Array.isArray(data)) setCollections(data)
    } catch { /* silent */ }
    finally { setLoading(false) }
  }, [])

  useEffect(() => {
    if (apiKey) loadCollections(apiKey)
  }, [apiKey, loadCollections])

  const handleNewCollection = (col: Collection) => {
    setCollections(prev => [col, ...prev])
  }

  if (!session) {
    return (
      <div className="min-h-screen bg-bg flex items-center justify-center">
        <span className="w-6 h-6 border-2 border-accent/30 border-t-accent rounded-full animate-spin" />
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-bg">
      {/* Nav */}
      <nav className="border-b border-border px-6 h-16 flex items-center justify-between">
        <span className="font-serif italic text-xl text-tx">DocsMCP</span>
        <div className="flex items-center gap-4">
          <span className="text-sm text-tx-muted">{session.user.email}</span>
          <button
            onClick={() => authClient.signOut().then(() => router.push("/"))}
            className="text-sm text-tx-muted hover:text-tx transition-colors"
          >
            Sign out
          </button>
        </div>
      </nav>

      <main className="max-w-4xl mx-auto px-6 py-12">
        {/* API Key */}
        <div className="rounded-xl border border-border bg-surface p-6 mb-8">
          <div className="flex items-center justify-between mb-3">
            <p className="text-sm font-medium text-tx">Platform API Key</p>
            <p className="text-xs text-tx-muted">Used for REST API calls</p>
          </div>
          {apiKey ? (
            <div className="flex items-center gap-3">
              <div className="flex-1 rounded-lg bg-code border border-border px-3 py-2.5">
                <p className="text-sm font-mono text-tx tracking-wider">
                  {keyVisible ? apiKey : `${apiKey.slice(0, 8)}${"•".repeat(24)}`}
                </p>
              </div>
              <button
                onClick={() => setKeyVisible(v => !v)}
                className="text-xs px-3 py-2.5 rounded-lg border border-border text-tx-muted hover:text-tx transition-colors"
              >
                {keyVisible ? "Hide" : "Show"}
              </button>
              <CopyButton text={apiKey} />
            </div>
          ) : (
            <div className="rounded-lg bg-code border border-border px-3 py-2.5">
              <p className="text-sm font-mono text-tx-muted">Loading API key…</p>
            </div>
          )}
        </div>

        {/* Collections */}
        <div className="flex items-center justify-between mb-6">
          <h2 className="text-lg font-semibold text-tx">Collections</h2>
          <span className="text-xs text-tx-muted font-mono">{collections.length} indexed</span>
        </div>

        <div className="space-y-4">
          {apiKey && (
            <AddCollectionForm apiKey={apiKey} onSuccess={handleNewCollection} />
          )}

          {loading ? (
            <div className="grid grid-cols-1 gap-4">
              {[1, 2].map(i => (
                <div key={i} className="h-32 rounded-xl bg-surface border border-border animate-pulse" />
              ))}
            </div>
          ) : collections.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border p-12 text-center">
              <p className="text-tx-muted text-sm mb-1">No collections yet</p>
              <p className="text-xs text-tx-muted">Paste a documentation URL above to get started</p>
            </div>
          ) : (
            collections.map(col => (
              <CollectionCard
                key={col.crawl_id}
                col={col}
                onPoll={() => loadCollections(apiKey)}
              />
            ))
          )}
        </div>

        {/* Usage hint */}
        {collections.some(c => c.status === "ready") && (
          <div className="mt-10 rounded-xl border border-border bg-surface-raised p-6">
            <p className="text-xs font-mono text-tx-muted mb-3">CLAUDE.md snippet</p>
            <div className="rounded-lg bg-code border border-border p-4 font-mono text-xs text-tx-muted space-y-1">
              <p className="text-accent">## DocsMCP</p>
              <p>Available MCP collections:</p>
              {collections.filter(c => c.status === "ready").map(c => (
                <p key={c.crawl_id}>- {new URL(c.url).hostname} → {c.mcp_endpoint}</p>
              ))}
              <p>Call search_docs before answering any library question.</p>
            </div>
            <div className="mt-3 flex justify-end">
              <CopyButton
                label="Copy CLAUDE.md snippet"
                text={`## DocsMCP\nAvailable MCP collections:\n${collections.filter(c => c.status === "ready").map(c => `- ${new URL(c.url).hostname} → ${c.mcp_endpoint}`).join("\n")}\nCall search_docs before answering any library question.`}
              />
            </div>
          </div>
        )}
      </main>
    </div>
  )
}
