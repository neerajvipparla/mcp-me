// MODULE: pkg/mcp/server.go
// PURPOSE: JSON-RPC 2.0 HTTP handler for /mcp/:crawl_id.
//          Implements the MCP protocol (initialize, tools/list, tools/call)
//          so Claude Code and Cursor can connect directly.
//          Auth: platform API key (SHA-256) — same key the user gets after GitHub OAuth.
//          Ownership verified: key → userID → must match user_crawls.user_id for this crawl_id.
//
// TO MODIFY BEHAVIOR:
//   - Add a new tool: add a case in callTool(), add its definition in toolDefinitions().
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/neerajvipparla/ion"
	"github.com/neerajvipparla/mcp-me/logging"
	"github.com/neerajvipparla/mcp-me/pkg/store"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
	ID      any       `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
}

type Server struct {
	tools  *Tools
	db     store.DB
	logger *ion.Ion
}

func NewServer(tools *Tools, db store.DB) *Server {
	return &Server{tools: tools, db: db, logger: logging.Get(logging.TopicMCP)}
}

func (s *Server) Handle(c *gin.Context) {
	crawlID := c.Param("crawl_id")

	auth := c.GetHeader("Authorization")
	key := strings.TrimPrefix(strings.TrimPrefix(auth, "Bearer "), "bearer ")
	if key == auth {
		key = "" // no bearer prefix found — don't use raw Authorization value
	}
	if key == "" {
		key = c.GetHeader("X-API-Key")
	}
	if key == "" {
		s.logger.Warn(c.Request.Context(), "auth failed",
			ion.String("file", "server.go"),
			ion.String("func", "Handle"),
			ion.String("type", "missing mcp_api_key"),
			ion.String("crawl_id", crawlID),
		)
		c.JSON(401, gin.H{"error": "missing mcp_api_key"})
		return
	}

	h := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(h[:])
	userID, err := s.db.FindUserByKeyHash(c.Request.Context(), keyHash)
	if err != nil || userID == "" {
		s.logger.Warn(c.Request.Context(), "auth failed",
			ion.String("file", "server.go"),
			ion.String("func", "Handle"),
			ion.String("type", "invalid api key"),
			ion.String("crawl_id", crawlID),
		)
		c.JSON(401, gin.H{"error": "invalid api key"})
		return
	}
	uc, err := s.db.GetUserCrawlByCrawlID(c.Request.Context(), crawlID)
	if err != nil || uc.UserID != userID {
		s.logger.Warn(c.Request.Context(), "auth failed",
			ion.String("file", "server.go"),
			ion.String("func", "Handle"),
			ion.String("type", "crawl not owned by user"),
			ion.String("crawl_id", crawlID),
		)
		c.JSON(401, gin.H{"error": "invalid api key"})
		return
	}

	var req rpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.logger.Warn(c.Request.Context(), "bad request",
			ion.String("file", "server.go"),
			ion.String("func", "Handle"),
			ion.String("type", "invalid json-rpc"),
			ion.String("crawl_id", crawlID),
		)
		c.JSON(400, gin.H{"error": "invalid json-rpc request"})
		return
	}

	ctx := c.Request.Context()
	var result any
	var rpcErr *rpcError

	switch req.Method {
	// ── MCP protocol ─────────────────────────────────────────────────────────
	case "initialize":
		result = gin.H{
			"protocolVersion": "2024-11-05",
			"capabilities":    gin.H{"tools": gin.H{}},
			"serverInfo":      gin.H{"name": "docsmcp", "version": "1.0.0"},
		}

	case "notifications/initialized":
		result = gin.H{}

	case "tools/list":
		result = gin.H{"tools": toolDefinitions()}

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		s.logger.Info(ctx, "tool called",
			ion.String("file", "server.go"),
			ion.String("func", "Handle"),
			ion.String("tool", p.Name),
			ion.String("crawl_id", crawlID),
		)
		result, rpcErr = s.callTool(ctx, crawlID, userID, p.Name, p.Arguments)

	// ── Legacy direct methods (curl-friendly) ─────────────────────────────────
	case "search_docs":
		var p struct {
			Query string `json:"query"`
			TopK  uint64 `json:"top_k"`
		}
		json.Unmarshal(req.Params, &p)
		if p.TopK == 0 {
			p.TopK = 5
		}
		res, err := s.tools.SearchDocs(ctx, crawlID, p.Query, p.TopK)
		if err != nil {
			rpcErr = &rpcError{Code: -32000, Message: err.Error()}
		} else {
			result = res
		}

	case "get_page":
		var p struct {
			URL string `json:"url"`
		}
		json.Unmarshal(req.Params, &p)
		res, err := s.tools.GetPage(ctx, crawlID, p.URL)
		if err != nil {
			rpcErr = &rpcError{Code: -32000, Message: err.Error()}
		} else {
			result = res
		}

	case "add_page":
		var p struct {
			URL string `json:"url"`
		}
		json.Unmarshal(req.Params, &p)
		n, err := s.tools.AddPage(ctx, crawlID, p.URL)
		if err != nil {
			rpcErr = &rpcError{Code: -32000, Message: err.Error()}
		} else {
			result = gin.H{"chunks_added": n}
		}

	default:
		s.logger.Warn(ctx, "method not found",
			ion.String("file", "server.go"),
			ion.String("func", "Handle"),
			ion.String("method", req.Method),
			ion.String("crawl_id", crawlID),
		)
		rpcErr = &rpcError{Code: -32601, Message: "method not found"}
	}

	c.JSON(http.StatusOK, rpcResponse{
		JSONRPC: "2.0",
		Result:  result,
		Error:   rpcErr,
		ID:      req.ID,
	})
}

// callTool dispatches tools/call to the correct tool implementation
// and wraps the result in MCP's content envelope.
func (s *Server) callTool(ctx context.Context, crawlID, userID, name string, args json.RawMessage) (any, *rpcError) {
	switch name {
	case "search_docs":
		var p struct {
			Query string `json:"query"`
			TopK  uint64 `json:"top_k"`
		}
		json.Unmarshal(args, &p)
		if p.TopK == 0 {
			p.TopK = 5
		}
		res, err := s.tools.SearchDocs(ctx, crawlID, p.Query, p.TopK)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil

	case "get_page":
		var p struct {
			URL string `json:"url"`
		}
		json.Unmarshal(args, &p)
		res, err := s.tools.GetPage(ctx, crawlID, p.URL)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil

	case "add_page":
		var p struct {
			URL string `json:"url"`
		}
		json.Unmarshal(args, &p)
		n, err := s.tools.AddPage(ctx, crawlID, p.URL)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		return toolResult{Content: []toolContent{{Type: "text", Text: fmt.Sprintf("Added %d chunks from %s", n, p.URL)}}}, nil

	case "create_crawl":
		var p struct {
			URL string `json:"url"`
		}
		json.Unmarshal(args, &p)
		res, err := s.tools.CreateCrawl(ctx, crawlID, p.URL)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil

	case "list_crawls":
		res, err := s.tools.ListCrawls(ctx, userID)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil

	case "get_status":
		var p struct {
			CrawlID string `json:"crawl_id"`
		}
		json.Unmarshal(args, &p)
		if p.CrawlID == "" {
			p.CrawlID = crawlID
		}
		res, err := s.tools.GetStatus(ctx, p.CrawlID)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil

	default:
		return nil, &rpcError{Code: -32601, Message: "tool not found: " + name}
	}
}

// toolDefinitions returns the MCP tool manifest for tools/list.
func toolDefinitions() []gin.H {
	return []gin.H{
		{
			"name":        "search_docs",
			"description": "Semantically search the crawled documentation. Returns the most relevant chunks for a query.",
			"inputSchema": gin.H{
				"type": "object",
				"properties": gin.H{
					"query": gin.H{"type": "string", "description": "The search query"},
					"top_k": gin.H{"type": "integer", "description": "Number of results to return (default 5)"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "get_page",
			"description": "Retrieve all chunks for a specific documentation page URL.",
			"inputSchema": gin.H{
				"type": "object",
				"properties": gin.H{
					"url": gin.H{"type": "string", "description": "The full page URL to retrieve"},
				},
				"required": []string{"url"},
			},
		},
		{
			"name":        "add_page",
			"description": "Fetch, chunk, embed and add a new page to the current documentation collection.",
			"inputSchema": gin.H{
				"type": "object",
				"properties": gin.H{
					"url": gin.H{"type": "string", "description": "The page URL to fetch and add"},
				},
				"required": []string{"url"},
			},
		},
		{
			"name":        "create_crawl",
			"description": "Create a new documentation collection for a different/unrelated URL. Use this when the URL topic is unrelated to the current collection (e.g. current collection is ClickHouse Go docs, new URL is Python PostgreSQL docs). Returns a new mcp_endpoint and mcp_api_key — save them to access the new collection.",
			"inputSchema": gin.H{
				"type": "object",
				"properties": gin.H{
					"url": gin.H{"type": "string", "description": "Root URL of the documentation to crawl"},
				},
				"required": []string{"url"},
			},
		},
		{
			"name":        "list_crawls",
			"description": "List all documentation collections indexed for your account. Call this at session start to discover available collections before using search_docs or get_page. Returns crawl_id, url, status, page_count, chunk_count, and mcp_endpoint for each collection.",
			"inputSchema": gin.H{
				"type":       "object",
				"properties": gin.H{},
			},
		},
		{
			"name":        "get_status",
			"description": "Poll the status of a crawl job. Use this after create_crawl returns status 'queued' to wait for it to become 'ready'. When status == 'ready', use the returned mcp_endpoint and your mcp_api_key to query that collection. Omit crawl_id to check the current session's crawl.",
			"inputSchema": gin.H{
				"type": "object",
				"properties": gin.H{
					"crawl_id": gin.H{"type": "string", "description": "Crawl ID to check. Defaults to the current session's crawl_id if omitted."},
				},
			},
		},
	}
}
