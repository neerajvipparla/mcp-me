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
	// UpsertUserByEmail creates the user on first GitHub login.
	// Does NOT overwrite an existing key hash — returns has_key=true if one already exists.
	// Returns (true, nil) when an existing key hash was found (key unchanged).
	// Returns (false, nil) when the user was new and the key hash was written.
	UpsertUserByEmail(ctx context.Context, r *UserRecord) (hasKey bool, err error)
	// RotateUserKey replaces the existing key hash for the given email unconditionally.
	RotateUserKey(ctx context.Context, email, keyHash string) error
}

// CrawlDB is the minimum interface for crawl lifecycle operations.
type CrawlDB interface {
	FindCrawlByHashAndEmbedder(ctx context.Context, urlHash, embedderID string) (*CrawlRecord, error)
	// FindCrawlByPageURL finds a ready crawl that already scraped this exact URL.
	// Used for sub-page cache hits: if url1.2 was crawled under url1.0's job,
	// a new request for url1.2 reuses url1.0's collection instead of re-crawling.
	FindCrawlByPageURL(ctx context.Context, url string) (*CrawlRecord, error)
	CreateCrawl(ctx context.Context, r *CrawlRecord) error
	UpdateCrawlStatus(ctx context.Context, id, status string) error
	UpdateCrawlReady(ctx context.Context, id string, pageCount, chunkCount int, lastModified *time.Time) error
	GetCrawlByID(ctx context.Context, id string) (*CrawlRecord, error)
	CreateUserCrawl(ctx context.Context, r *UserCrawlRecord) error
	GetUserCrawlByCrawlID(ctx context.Context, crawlID string) (*UserCrawlRecord, error)
	CreateCrawlPage(ctx context.Context, crawlID, url, title string, chunkCount int) error
	// ListUserCrawls returns all crawls belonging to userID, newest first.
	ListUserCrawls(ctx context.Context, userID string) ([]CrawlRecord, error)
}

// DB composes both interfaces. PostgresStore implements this.
// Only main.go should hold a DB — it narrows to UserDB or CrawlDB
// when constructing individual components.
type DB interface {
	UserDB
	CrawlDB
}
