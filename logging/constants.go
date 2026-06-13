package logging

// Topics for named loggers in this repo. The topic appears as the "logger"
// field in every log row (queryable in ClickHouse: WHERE logger = 'server').
// Add a constant here per subsystem — never pass ad-hoc topic strings.
const (
	TopicServer    = "server"    // startup, shutdown, signal handling
	TopicAPI       = "api"       // HTTP handlers, auth middleware
	TopicWorker    = "worker"    // Asynq pipeline, job status transitions
	TopicCrawler   = "crawler"   // fetch strategies (plainhttp/chromedp/firecrawl), pool
	TopicDiscovery = "discovery" // sitemap fetch, BFS link extraction
	TopicChunker   = "chunker"   // heading-aware text splitting
	TopicStore     = "store"     // Qdrant ops, Postgres ops
	TopicMCP       = "mcp"       // MCP server, tool dispatch
)
