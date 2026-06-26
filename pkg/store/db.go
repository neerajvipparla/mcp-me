// MODULE: pkg/store — DB interfaces
// PURPOSE: Defines minimum-surface interfaces for DB access per consumer.
//          PostgresStore implements DB (= UserDB + CrawlDB).
//          Components receive only the sub-interface they actually need
//          so callers are not forced to depend on methods they never call.
//
// CONSUMER MAP:
//   UserDB   → PlatformKeyAuth middleware, GitHubAuthHandler
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
	// FindUserByKeyHash looks up by SHA-256 hex of the platform API key.
	// Returns "", nil when not found.
	FindUserByKeyHash(ctx context.Context, keyHash string) (string, error)
	// FindUserByEmail looks up a user ID by email address.
	// Returns "", nil when not found.
	FindUserByEmail(ctx context.Context, email string) (string, error)
	// VerifyBetterAuthSession checks the Better Auth session table for a valid, non-expired token.
	// Returns the user's email on success, "" when the token is missing or expired.
	// Go and Next.js share the same Postgres, so Go can verify tokens directly — no shared secret needed.
	VerifyBetterAuthSession(ctx context.Context, token string) (string, error)
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
	// GetCrawlPages returns all indexed pages for a crawl, ordered by crawled_at.
	GetCrawlPages(ctx context.Context, crawlID string) ([]*CrawlPage, error)
	// ListUserCrawls returns a page of crawls belonging to userID, newest first.
	// Request limit+1 rows internally; hasMore=true means there are more pages.
	ListUserCrawls(ctx context.Context, userID string, limit, offset int) (crawls []CrawlRecord, hasMore bool, err error)
}

// DB composes both interfaces. PostgresStore implements this.
// Only main.go should hold a DB — it narrows to UserDB or CrawlDB
// when constructing individual components.
type DB interface {
	UserDB
	CrawlDB
}
