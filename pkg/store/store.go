// MODULE: pkg/store
// PURPOSE: Unified vector store interface. Implementations embed texts
//          internally — callers never touch vectors or model names directly.
//
// EXTENSION POINT:
//   Add new embedder: implement Store interface, wire in cmd/server/main.go
//   via newStore(). No pipeline or MCP code changes required.
//
// DO NOT:
//   Accept *qdrant.Client in Store methods — that belongs in constructors only.
//   Store connection config here — that lives in pkg/qdrantcfg.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// Store is the unified vector store interface.
// DocumentStore (MiniLM server-side) and VectorStore (OpenAI pre-computed)
// both implement this — pipeline and MCP tools use only this interface.
type Store interface {
	// EmbedderID returns the identifier written to crawls.embedder_id.
	EmbedderID() string
	// EnsureCollection creates the Qdrant collection if it does not exist.
	EnsureCollection(ctx context.Context, name string) error
	// Upsert embeds texts and writes the corresponding points.
	// len(texts) must equal len(points).
	Upsert(ctx context.Context, collection string, texts []string, points []Point) error
	// Search embeds query and returns the top-k closest points.
	Search(ctx context.Context, collection, query string, topK uint64) ([]SearchResult, error)
	// GetByURL returns all points for a given page URL without scoring.
	GetByURL(ctx context.Context, collection, pageURL string) ([]SearchResult, error)
}

// Point is one chunk to upsert into Qdrant.
type Point struct {
	ChunkIndex  int
	Text        string
	HeadingPath string
	PageURL     string
	PageTitle   string
	CrawlID     string
}

// SearchResult is one result from Search or GetByURL.
type SearchResult struct {
	Text        string  `json:"text"`
	HeadingPath string  `json:"heading_path"`
	PageURL     string  `json:"page_url"`
	PageTitle   string  `json:"page_title"`
	Score       float32 `json:"score,omitempty"`
}

// CollectionName returns the Qdrant collection name for a (url, embedder) pair.
// Format: docs_{sha256(normalizedURL)[:12]}_{embedderID}
// Example: docs_abc123def456_minilm
func CollectionName(urlHash, embedderID string) string {
	return fmt.Sprintf("docs_%s_%s", urlHash, embedderID)
}

// HashURL normalizes rawURL and returns the first 12 hex chars of its SHA-256.
func HashURL(rawURL string) string {
	h := sha256.Sum256([]byte(normalizeURL(rawURL)))
	return hex.EncodeToString(h[:])[:12]
}

func normalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	path := strings.TrimRight(u.Path, "/")
	return host + path
}
