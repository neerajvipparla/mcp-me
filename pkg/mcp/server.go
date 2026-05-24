// MODULE: pkg/mcp/server.go
// PURPOSE: JSON-RPC 2.0 HTTP handler for /mcp/:crawl_id.
//          Owns mcp_api_key verification (bcrypt) and request routing to Tools.
//
// CORE DATA STRUCTURES:
//   - rpcRequest / rpcResponse: per-request value structs, not retained.
//
// TO MODIFY BEHAVIOR:
//   - Add a new tool: add a case in the switch, implement the method in tools.go.
//   - Change auth scheme: edit the key extraction and bcrypt check block.
//
// DO NOT:
//   - Import *PostgresStore — depends on store.CrawlDB only.
//   - Use bcrypt for platform_api_key — that uses SHA-256 (see middleware.go).
//     bcrypt is correct here because mcp_api_key is verified per session, not DB-looked-up.
package mcp

import (
	"encoding/json"
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

type Server struct {
	tools *Tools
	db    store.CrawlDB
}

func NewServer(tools *Tools, db store.CrawlDB) *Server {
	return &Server{tools: tools, db: db}
}

func (s *Server) Handle(c *gin.Context) {
	crawlID := c.Param("crawl_id")

	key := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
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
