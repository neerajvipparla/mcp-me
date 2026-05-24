// MODULE: pkg/store — DB interfaces
// PURPOSE: Defines minimum-surface interfaces for DB access per consumer.
//          PostgresStore implements DB (= UserDB + CrawlDB).
//          Components receive only the sub-interface they actually need
//          so callers are not forced to depend on methods they never call.
//
// CONSUMER MAP:
//   UserDB   → PlatformKeyAuth middleware, RegisterHandler
//   CrawlDB  → CrawlHandler, PipelineHandler (worker), MCP Server/Tools
//   DB       → cmd/server/main.go wiring only
//
// EXTENSION POINT:
//   New DB backend (e.g. SQLite, DynamoDB): implement UserDB and/or CrawlDB.
//   No handler code changes needed.
package store

import (
	"context"
	"time"
)

// UserDB is the minimum interface for user management.
// Satisfy this to plug in any auth-compatible store.
type UserDB interface {
	CreateUser(ctx context.Context, r *UserRecord) error
	// FindUserByKeyHash looks up by SHA-256 hex of the platform API key.
	// Returns "", nil when not found.
	FindUserByKeyHash(ctx context.Context, keyHash string) (string, error)
}

// CrawlDB is the minimum interface for crawl lifecycle operations.
type CrawlDB interface {
	FindCrawlByHashAndEmbedder(ctx context.Context, urlHash, embedderID string) (*CrawlRecord, error)
	CreateCrawl(ctx context.Context, r *CrawlRecord) error
	UpdateCrawlStatus(ctx context.Context, id, status string) error
	UpdateCrawlReady(ctx context.Context, id string, pageCount, chunkCount int, lastModified *time.Time) error
	GetCrawlByID(ctx context.Context, id string) (*CrawlRecord, error)
	CreateUserCrawl(ctx context.Context, r *UserCrawlRecord) error
	GetUserCrawlByCrawlID(ctx context.Context, crawlID string) (*UserCrawlRecord, error)
}

// DB composes both interfaces. PostgresStore implements this.
// Only main.go should hold a DB — it narrows to UserDB or CrawlDB
// when constructing individual components.
type DB interface {
	UserDB
	CrawlDB
}
