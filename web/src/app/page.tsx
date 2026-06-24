"use client"

import { useState, useEffect, useRef } from "react"
import Link from "next/link"

/* ── Pipeline stage types ── */
type Stage = "idle" | "crawling" | "chunking" | "embedding" | "ready"

const STAGE_LABELS: Record<Stage, string> = {
  idle: "",
  crawling: "Crawling pages",
  chunking: "Chunking content",
  embedding: "Embedding vectors",
  ready: "Ready",
}

const STAGE_DURATIONS: Record<string, number> = {
  crawling: 2200,
  chunking: 1600,
  embedding: 2000,
}

/* ── Terminal demo lines ── */
const TERMINAL_LINES = [
  { delay: 0,    type: "user",    text: "how do I batch insert in ClickHouse with Go?" },
  { delay: 800,  type: "system",  text: "Searching ClickHouse Go SDK docs..." },
  { delay: 1600, type: "tool",    text: '[search_docs] "batch insert go clickhouse" → 4 results' },
  { delay: 2600, type: "answer",  text: "Use `PrepareBatch()` on the connection, then `AppendStruct()` per row, and call `Send()` to flush. The driver handles batching automatically — no manual chunking needed." },
  { delay: 3800, type: "cite",    text: "↳ clickhouse.com/docs/en/integrations/go — Batch Insert" },
]

/* ── Nav ── */
function Nav() {
  const [scrolled, setScrolled] = useState(false)
  useEffect(() => {
    const h = () => setScrolled(window.scrollY > 20)
    window.addEventListener("scroll", h, { passive: true })
    return () => window.removeEventListener("scroll", h)
  }, [])

  return (
    <nav
      className={`fixed top-0 left-0 right-0 z-50 transition-all duration-300 ${
        scrolled ? "bg-bg/90 backdrop-blur-md border-b border-border" : ""
      }`}
    >
      <div className="max-w-5xl mx-auto px-6 h-16 flex items-center justify-between">
        <span className="font-serif italic text-xl text-tx">mcp-me</span>
        <div className="flex items-center gap-6">
          <a href="#how" className="text-sm text-tx-muted hover:text-tx transition-colors">
            How it works
          </a>
          <Link
            href="/login"
            className="text-sm px-4 py-2 rounded-md border border-accent text-accent hover:bg-accent hover:text-white transition-all duration-200"
          >
            Sign in
          </Link>
        </div>
      </div>
    </nav>
  )
}

/* ── Pipeline demo (hero interactive element) ── */
function PipelineDemo() {
  const [url, setUrl] = useState("")
  const [stage, setStage] = useState<Stage>("idle")
  const [progress, setProgress] = useState(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const clearTimers = () => {
    if (timerRef.current) clearTimeout(timerRef.current)
    if (intervalRef.current) clearInterval(intervalRef.current)
  }

  const runPipeline = () => {
    if (!url.trim() || stage !== "idle") return
    clearTimers()
    setProgress(0)
    setStage("crawling")

    const stages: Stage[] = ["crawling", "chunking", "embedding"]
    let i = 0

    const nextStage = () => {
      i++
      if (i < stages.length) {
        setProgress(0)
        setStage(stages[i])
        timerRef.current = setTimeout(nextStage, STAGE_DURATIONS[stages[i]])
      } else {
        setStage("ready")
      }
    }

    // Smooth progress bar
    const tick = () => {
      setProgress(p => Math.min(p + 1.5, 95))
    }
    intervalRef.current = setInterval(tick, 40)

    timerRef.current = setTimeout(() => {
      clearInterval(intervalRef.current!)
      nextStage()
    }, STAGE_DURATIONS["crawling"])
  }

  useEffect(() => () => clearTimers(), [])

  const handleKey = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") runPipeline()
  }

  const reset = () => {
    clearTimers()
    setStage("idle")
    setProgress(0)
    setUrl("")
  }

  return (
    <div className="w-full max-w-2xl mx-auto">
      {/* URL input */}
      <div className={`relative rounded-xl border transition-all duration-300 overflow-hidden ${
        stage === "idle"
          ? "border-border bg-surface focus-within:border-accent focus-within:shadow-accent-glow"
          : stage === "ready"
          ? "border-ready/40 bg-surface animate-ready-glow"
          : "border-accent/40 bg-surface animate-glow-pulse"
      }`}>
        <div className="flex items-center">
          <span className="pl-4 text-tx-muted font-mono text-sm select-none">https://</span>
          <input
            type="text"
            value={url.replace(/^https?:\/\//, "")}
            onChange={e => setUrl("https://" + e.target.value)}
            onKeyDown={handleKey}
            placeholder="docs.example.com"
            disabled={stage !== "idle"}
            className="flex-1 bg-transparent px-2 py-4 text-sm font-mono text-tx placeholder:text-tx-faint outline-none disabled:opacity-60"
            aria-label="Documentation URL"
          />
          {stage === "idle" ? (
            <button
              onClick={runPipeline}
              disabled={!url.trim()}
              className="mr-2 px-4 py-2 rounded-lg text-sm font-medium bg-accent text-white hover:bg-accent-light disabled:opacity-30 disabled:cursor-not-allowed transition-all duration-200 whitespace-nowrap"
            >
              Index docs →
            </button>
          ) : stage === "ready" ? (
            <button
              onClick={reset}
              className="mr-2 px-4 py-2 rounded-lg text-sm font-medium border border-border text-tx-muted hover:text-tx transition-all duration-200"
            >
              Reset
            </button>
          ) : (
            <span className="mr-4 text-xs text-tx-muted animate-pulse">Running…</span>
          )}
        </div>

        {/* Progress bar */}
        {stage !== "idle" && stage !== "ready" && (
          <div className="h-0.5 bg-border">
            <div
              className="h-full bg-accent transition-all duration-100 ease-linear"
              style={{ width: `${progress}%` }}
            />
          </div>
        )}
      </div>

      {/* Stage indicators */}
      <div className="mt-4 grid grid-cols-3 gap-2">
        {(["crawling", "chunking", "embedding"] as const).map((s, idx) => {
          const stageOrder: Stage[] = ["crawling", "chunking", "embedding", "ready"]
          const currentIdx = stageOrder.indexOf(stage)
          const thisIdx = stageOrder.indexOf(s)
          const isActive = stage === s
          const isDone = currentIdx > thisIdx || stage === "ready"

          return (
            <div
              key={s}
              className={`rounded-lg border px-3 py-2.5 transition-all duration-300 ${
                isActive
                  ? "border-accent/50 bg-accent-dim"
                  : isDone
                  ? "border-ready/30 bg-ready-dim"
                  : "border-border bg-surface"
              }`}
            >
              <div className="flex items-center gap-2">
                <span className={`text-xs ${isActive ? "text-accent" : isDone ? "text-ready" : "text-tx-faint"}`}>
                  {isDone ? "✓" : isActive ? (
                    <span className="inline-block w-3 h-3 relative">
                      <span className="absolute inset-0 rounded-full border border-accent animate-spin border-t-transparent" style={{ borderTopColor: "transparent" }} />
                    </span>
                  ) : String(idx + 1).padStart(2, "0")}
                </span>
                <span className={`text-xs font-medium ${isActive ? "text-tx" : isDone ? "text-ready" : "text-tx-faint"}`}>
                  {STAGE_LABELS[s]}
                </span>
              </div>
              {isActive && (
                <div className="mt-1.5 h-px bg-border overflow-hidden rounded">
                  <div className="h-full bg-accent/60 w-1/3 animate-crawl" />
                </div>
              )}
            </div>
          )
        })}
      </div>

      {/* Ready state */}
      {stage === "ready" && (
        <div className="mt-4 rounded-xl border border-ready/30 bg-ready-dim p-4 animate-float-in">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-ready animate-pulse" />
              <span className="text-xs font-medium text-ready">Ready · 312 pages · 2,840 chunks</span>
            </div>
            <span className="text-xs text-tx-muted">~90s</span>
          </div>
          <div className="rounded-lg bg-code border border-border p-3 font-mono text-xs">
            <p className="text-tx-muted mb-1"># Add to Claude Code</p>
            <p className="text-tx break-all">
              claude mcp add my-docs --transport http \
            </p>
            <p className="text-accent break-all pl-4">
              https://mcp-me-production.up.railway.app/v1/mcp/<span className="opacity-60">…</span>
            </p>
          </div>
          <p className="mt-3 text-xs text-tx-muted text-center">
            Sign in to get your real endpoint and API key →{" "}
            <Link href="/login" className="text-accent hover:underline">Get started free</Link>
          </p>
        </div>
      )}
    </div>
  )
}

/* ── How it works ── */
function HowItWorks() {
  const steps = [
    {
      icon: "⤵",
      title: "Submit any docs URL",
      body: "Paste the root URL of any public documentation site. mcp-me discovers every page via sitemap.xml or BFS crawl, bounded to the same domain.",
      detail: "Handles Hugo, MkDocs, Docusaurus, VitePress, Next.js — static and JS-rendered.",
    },
    {
      icon: "⋮",
      title: "Pages are chunked",
      body: "Each page is split on heading boundaries into 400–600 token chunks. Code blocks are never split mid-block. Every chunk carries its source URL and heading path.",
      detail: "Token counting via tiktoken cl100k_base.",
    },
    {
      icon: "◈",
      title: "Embedded server-side",
      body: "Chunks are embedded using Qdrant's server-side FastEmbed — MiniLM-L6 for dense vectors, BM25 for sparse. No OpenAI calls, no local model.",
      detail: "Hybrid search fuses both legs with Reciprocal Rank Fusion.",
    },
    {
      icon: "⌁",
      title: "Claude searches automatically",
      body: "Your MCP endpoint is ready. Add it to Claude Code, Claude Desktop, or Cursor. Claude calls `search_docs` on its own whenever a question touches that library.",
      detail: "JSON-RPC 2.0. No config needed in the conversation.",
    },
  ]

  return (
    <section id="how" className="py-32 px-6">
      <div className="max-w-5xl mx-auto">
        <p className="text-xs uppercase tracking-widest text-tx-muted mb-4 font-mono">The pipeline</p>
        <h2 className="font-serif italic text-4xl md:text-5xl text-tx mb-16 max-w-lg">
          From URL to<br />searchable index.
        </h2>

        <div className="grid md:grid-cols-2 gap-px bg-border rounded-2xl overflow-hidden">
          {steps.map((s, i) => (
            <div
              key={i}
              className="bg-bg p-8 group hover:bg-surface transition-colors duration-200"
            >
              <div className="flex items-start gap-4">
                <span className="text-2xl text-accent mt-0.5 w-8 shrink-0">{s.icon}</span>
                <div>
                  <h3 className="text-base font-semibold text-tx mb-2">{s.title}</h3>
                  <p className="text-sm text-tx-muted leading-relaxed mb-3">{s.body}</p>
                  <p className="text-xs text-tx-faint font-mono">{s.detail}</p>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

/* ── Terminal demo ── */
function TerminalDemo() {
  const [visibleLines, setVisibleLines] = useState<number>(0)
  const ref = useRef<HTMLDivElement>(null)
  const [started, setStarted] = useState(false)

  useEffect(() => {
    const obs = new IntersectionObserver(
      ([e]) => { if (e.isIntersecting && !started) setStarted(true) },
      { threshold: 0.4 }
    )
    if (ref.current) obs.observe(ref.current)
    return () => obs.disconnect()
  }, [started])

  useEffect(() => {
    if (!started) return
    TERMINAL_LINES.forEach((line, i) => {
      setTimeout(() => setVisibleLines(i + 1), line.delay)
    })
  }, [started])

  return (
    <section className="py-24 px-6 bg-surface border-y border-border">
      <div className="max-w-5xl mx-auto">
        <div className="grid md:grid-cols-2 gap-12 items-center">
          <div>
            <p className="text-xs uppercase tracking-widest text-tx-muted mb-4 font-mono">In action</p>
            <h2 className="font-serif italic text-4xl md:text-5xl text-tx mb-6">
              Real answers.<br />Cited sources.
            </h2>
            <p className="text-tx-muted text-base leading-relaxed">
              Claude reads the chunk, cites the page, and moves on. No hallucinated method signatures. No deprecated patterns. The docs are the ground truth.
            </p>
            <div className="mt-8 flex flex-col gap-3">
              {["search_docs — semantic + keyword hybrid search", "get_page — full page retrieval by URL", "add_page — index a page that wasn't crawled", "create_crawl — start a new collection mid-session"].map(t => (
                <div key={t} className="flex items-start gap-3">
                  <span className="text-accent mt-0.5 text-xs font-mono">→</span>
                  <span className="text-sm text-tx-muted font-mono">{t}</span>
                </div>
              ))}
            </div>
          </div>

          <div ref={ref} className="rounded-xl border border-border bg-code overflow-hidden">
            {/* Terminal chrome */}
            <div className="flex items-center gap-2 px-4 py-3 border-b border-border">
              <span className="w-3 h-3 rounded-full bg-[#FF5F57]" />
              <span className="w-3 h-3 rounded-full bg-[#FFBD2E]" />
              <span className="w-3 h-3 rounded-full bg-[#28C840]" />
              <span className="ml-3 text-xs text-tx-muted font-mono">Claude Code</span>
            </div>
            <div className="p-5 font-mono text-xs space-y-3 min-h-64">
              {TERMINAL_LINES.slice(0, visibleLines).map((line, i) => (
                <div key={i} className="animate-float-in">
                  {line.type === "user" && (
                    <p className="text-tx">
                      <span className="text-tx-muted mr-2">{">"}</span>
                      {line.text}
                    </p>
                  )}
                  {line.type === "system" && (
                    <p className="text-tx-muted italic">{line.text}</p>
                  )}
                  {line.type === "tool" && (
                    <p className="text-accent/80 bg-accent-dim rounded px-2 py-1">{line.text}</p>
                  )}
                  {line.type === "answer" && (
                    <p className="text-tx leading-relaxed">{line.text}</p>
                  )}
                  {line.type === "cite" && (
                    <p className="text-ready text-[11px]">{line.text}</p>
                  )}
                </div>
              ))}
              {visibleLines > 0 && visibleLines < TERMINAL_LINES.length && (
                <span className="text-tx-muted cursor-blink" />
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}

/* ── Stats bar ── */
function Stats() {
  const items = [
    { value: "500", label: "pages per crawl" },
    { value: "400–600", label: "tokens per chunk" },
    { value: "384d", label: "MiniLM-L6 dense vectors" },
    { value: "RRF", label: "hybrid fusion" },
  ]
  return (
    <div className="border-b border-border py-12 px-6">
      <div className="max-w-5xl mx-auto grid grid-cols-2 md:grid-cols-4 gap-8">
        {items.map(({ value, label }) => (
          <div key={label} className="text-center">
            <p className="font-serif text-3xl text-tx mb-1">{value}</p>
            <p className="text-xs text-tx-muted font-mono">{label}</p>
          </div>
        ))}
      </div>
    </div>
  )
}

/* ── CTA ── */
function CTA() {
  return (
    <section className="py-32 px-6 relative overflow-hidden">
      <div className="absolute inset-0 bg-accent-radial pointer-events-none" />
      <div className="relative max-w-2xl mx-auto text-center">
        <h2 className="font-serif italic text-5xl md:text-6xl text-tx mb-6 leading-tight">
          Claude should read<br />the docs.
        </h2>
        <p className="text-tx-muted text-lg mb-10 leading-relaxed">
          Stop fact-checking Claude against documentation you already have open.
          Index it once and let Claude search it on every question.
        </p>
        <Link
          href="/login"
          className="inline-flex items-center gap-3 px-8 py-4 rounded-xl bg-accent hover:bg-accent-light text-white font-medium text-base transition-all duration-200 hover:shadow-accent-glow"
        >
          <svg className="w-5 h-5" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0 0 24 12c0-6.63-5.37-12-12-12z" />
          </svg>
          Sign in with GitHub — it's free
        </Link>
        <p className="mt-5 text-xs text-tx-muted">
          No credit card. No usage limits on crawls during beta.
        </p>
      </div>
    </section>
  )
}

/* ── Footer ── */
function Footer() {
  return (
    <footer className="border-t border-border py-8 px-6">
      <div className="max-w-5xl mx-auto flex flex-col md:flex-row items-center justify-between gap-4">
        <span className="font-serif italic text-lg text-tx-muted">mcp-me</span>
        <div className="flex items-center gap-6 text-xs text-tx-muted">
          <a href="https://mcp-me-production.up.railway.app/health" target="_blank" rel="noreferrer" className="hover:text-tx transition-colors">
            API status
          </a>
          <a href="https://github.com/neerajvipparla/mcp-me" target="_blank" rel="noreferrer" className="hover:text-tx transition-colors">
            GitHub
          </a>
          <span>Built with Qdrant FastEmbed · MiniLM-L6 · BM25</span>
        </div>
      </div>
    </footer>
  )
}

/* ── Page ── */
export default function HomePage() {
  return (
    <div className="bg-bg min-h-screen bg-grid-neutral bg-grid">
      <Nav />

      {/* Hero */}
      <section className="relative pt-40 pb-16 px-6 overflow-hidden">
        <div className="absolute inset-0 bg-accent-radial pointer-events-none" />

        <div className="relative max-w-5xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-accent/30 bg-accent-dim text-accent text-xs font-mono mb-8">
            <span className="w-1.5 h-1.5 rounded-full bg-accent animate-pulse" />
            MCP · Hybrid semantic search · No OpenAI
          </div>

          <h1 className="font-serif italic text-6xl md:text-7xl lg:text-8xl text-tx leading-[1.05] mb-6">
            Give Claude<br />
            <span className="text-accent">the actual docs.</span>
          </h1>

          <p className="text-tx-muted text-lg md:text-xl mb-12 max-w-xl mx-auto leading-relaxed">
            Paste a documentation URL. mcp-me crawls it, embeds it,
            and hands Claude a private search endpoint. Real answers. Cited sources.
          </p>

          <PipelineDemo />

          <p className="mt-6 text-xs text-tx-muted">
            Try it — paste any docs URL above. No login needed for the demo.
          </p>
        </div>
      </section>

      <Stats />
      <HowItWorks />
      <TerminalDemo />
      <CTA />
      <Footer />
    </div>
  )
}
