// MODULE: pkg/mcp/server.go
// PURPOSE: JSON-RPC 2.0 HTTP handler for /mcp/:crawl_id.
//          Implements the MCP protocol (initialize, tools/list, tools/call)
//          so Claude Code and Cursor can connect directly.
//          Owns mcp_api_key verification (bcrypt) and request routing to Tools.
//
// CORE DATA STRUCTURES:
//   - rpcRequest / rpcResponse: per-request value structs, not retained.
//
// TO MODIFY BEHAVIOR:
//   - Add a new tool: add a case in callTool(), add its definition in toolDefinitions().
//   - Change auth scheme: edit the key extraction and bcrypt check block.
//
// DO NOT:
//   - Import *PostgresStore — depends on store.CrawlDB only.
//   - Use bcrypt for platform_api_key — that uses SHA-256 (see middleware.go).
//     bcrypt is correct here because mcp_api_key is verified per session, not DB-looked-up.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/neerajvipparla/mcp-me/pkg/store"
	"golang.org/x/crypto/bcrypt"
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
	tools *Tools
	db    store.CrawlDB
}

func NewServer(tools *Tools, db store.CrawlDB) *Server {
	return &Server{tools: tools, db: db}
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
		c.JSON(401, gin.H{"error": "missing mcp_api_key"})
		return
	}

	uc, err := s.db.GetUserCrawlByCrawlID(c.Request.Context(), crawlID)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(uc.MCPAPIKeyHash), []byte(key)) != nil {
		c.JSON(401, gin.H{"error": "invalid mcp_api_key"})
		return
	}

	var req rpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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
		result, rpcErr = s.callTool(ctx, crawlID, p.Name, p.Arguments)

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
func (s *Server) callTool(ctx context.Context, crawlID, name string, args json.RawMessage) (any, *rpcError) {
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
			"description": "Fetch, chunk, embed and add a new page to the documentation collection.",
			"inputSchema": gin.H{
				"type": "object",
				"properties": gin.H{
					"url": gin.H{"type": "string", "description": "The page URL to fetch and add"},
				},
				"required": []string{"url"},
			},
		},
	}
}
