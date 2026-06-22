// MODULE: pkg/store/postgres.go
// PURPOSE: Implements DB (UserDB + CrawlDB) using PostgreSQL via pgxpool.
//          Owns all SQL for user registration, crawl lifecycle, and MCP key management.
//
// CORE DATA STRUCTURES:
//   - *pgxpool.Pool: connection pool, shared across all requests. Owned by main.go.
//     Growth: fixed pool size (pgxpool default: 4 per CPU core).
//   - UserRecord, CrawlRecord, UserCrawlRecord: plain value structs, not cached.
//
// TO MODIFY BEHAVIOR:
//   - Add a query: add a method on PostgresStore, declare it in UserDB or CrawlDB in db.go.
//   - Change schema: update migrations/001_init.up.sql; existing methods may need updating.
//
// DO NOT:
//   - Store bcrypt hashes for platform_api_key — SHA-256 is used (deterministic, DB-lookupable).
//   - Log mcp_api_key hashes or plaintext — they leave this package only as opaque strings.
//
// EXTENSION POINT: swap to a different DB backend by implementing store.DB in a new file.
//   No handler or worker code needs to change.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time proof that PostgresStore satisfies the full DB contract.
// If any method in UserDB or CrawlDB is missing, this line fails to compile.
var _ DB = (*PostgresStore)(nil)

type UserRecord struct {
	ID                 string
	Email              string
	PlatformAPIKeyHash string
}

type CrawlRecord struct {
	ID               string
	URLRaw           string
	URLNormalized    string
	URLHash          string
	Status           string
	EmbedderID       string
	PageCount        int
	ChunkCount       int
	QdrantCollection string
	LastModified     *time.Time
	CreatedAt        time.Time
	ReadyAt          *time.Time
}

type UserCrawlRecord struct {
	ID            string
	UserID        string
	CrawlID       string
	MCPAPIKeyHash string
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, dsn string) (DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) CreateUser(ctx context.Context, r *UserRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, email, platform_api_key_hash, created_at)
		 VALUES ($1, $2, $3, now())`,
		r.ID, r.Email, r.PlatformAPIKeyHash,
	)
	return err
}

// FindUserByKeyHash looks up a user by SHA-256 hex of their platform API key.
// Returns "", nil when not found — callers treat empty string as unauthenticated.
func (s *PostgresStore) FindUserByKeyHash(ctx context.Context, keyHash string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE platform_api_key_hash = $1`, keyHash,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return id, err
}

// FindCrawlByHashAndEmbedder returns a ready crawl for cache-hit reuse.
// Returns nil, nil when no match.
func (s *PostgresStore) FindCrawlByHashAndEmbedder(ctx context.Context, urlHash, embedderID string) (*CrawlRecord, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, url_raw, url_normalized, url_hash, status, embedder_id, page_count,
		        chunk_count, qdrant_collection, last_modified, created_at, ready_at
		 FROM crawls WHERE url_hash=$1 AND embedder_id=$2 AND status='ready'`,
		urlHash, embedderID,
	)
	var r CrawlRecord
	err := row.Scan(&r.ID, &r.URLRaw, &r.URLNormalized, &r.URLHash, &r.Status,
		&r.EmbedderID, &r.PageCount, &r.ChunkCount, &r.QdrantCollection,
		&r.LastModified, &r.CreatedAt, &r.ReadyAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PostgresStore) CreateCrawl(ctx context.Context, r *CrawlRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO crawls (id, url_raw, url_normalized, url_hash, status, embedder_id,
		                     qdrant_collection, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,now())`,
		r.ID, r.URLRaw, r.URLNormalized, r.URLHash, r.Status, r.EmbedderID, r.QdrantCollection,
	)
	return err
}

func (s *PostgresStore) UpdateCrawlStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE crawls SET status=$1 WHERE id=$2`, status, id)
	return err
}

func (s *PostgresStore) UpdateCrawlReady(ctx context.Context, id string, pageCount, chunkCount int, lastModified *time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE crawls SET status='ready', page_count=$2, chunk_count=$3,
		                   last_modified=$4, ready_at=now() WHERE id=$1`,
		id, pageCount, chunkCount, lastModified,
	)
	return err
}

func (s *PostgresStore) GetCrawlByID(ctx context.Context, id string) (*CrawlRecord, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, url_raw, url_normalized, url_hash, status, embedder_id, page_count,
		        chunk_count, qdrant_collection, last_modified, created_at, ready_at
		 FROM crawls WHERE id=$1`, id,
	)
	var r CrawlRecord
	err := row.Scan(&r.ID, &r.URLRaw, &r.URLNormalized, &r.URLHash, &r.Status,
		&r.EmbedderID, &r.PageCount, &r.ChunkCount, &r.QdrantCollection,
		&r.LastModified, &r.CreatedAt, &r.ReadyAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PostgresStore) CreateUserCrawl(ctx context.Context, r *UserCrawlRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_crawls (id, user_id, crawl_id, mcp_api_key_hash, created_at)
		 VALUES ($1,$2,$3,$4,now())`,
		r.ID, r.UserID, r.CrawlID, r.MCPAPIKeyHash,
	)
	return err
}

func (s *PostgresStore) CreateCrawlPage(ctx context.Context, crawlID, url, title string, chunkCount int) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO crawl_pages (id, crawl_id, url, title, chunk_count, crawled_at)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, now())
		 ON CONFLICT DO NOTHING`,
		crawlID, url, title, chunkCount,
	)
	return err
}

// FindCrawlByPageURL returns the most recent ready crawl that already scraped url.
// Returns nil, nil when not found.
func (s *PostgresStore) FindCrawlByPageURL(ctx context.Context, url string) (*CrawlRecord, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT c.id, c.url_raw, c.url_normalized, c.url_hash, c.status, c.embedder_id,
		        c.page_count, c.chunk_count, c.qdrant_collection, c.last_modified,
		        c.created_at, c.ready_at
		 FROM crawls c
		 JOIN crawl_pages cp ON cp.crawl_id = c.id
		 WHERE cp.url = $1 AND c.status = 'ready'
		 ORDER BY c.ready_at DESC
		 LIMIT 1`, url,
	)
	var r CrawlRecord
	err := row.Scan(&r.ID, &r.URLRaw, &r.URLNormalized, &r.URLHash, &r.Status,
		&r.EmbedderID, &r.PageCount, &r.ChunkCount, &r.QdrantCollection,
		&r.LastModified, &r.CreatedAt, &r.ReadyAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PostgresStore) ListUserCrawls(ctx context.Context, userID string) ([]CrawlRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, url_raw, url_normalized, url_hash, status, embedder_id,
		        page_count, chunk_count, qdrant_collection, last_modified, created_at, ready_at
		 FROM crawls
		 WHERE id IN (SELECT DISTINCT crawl_id FROM user_crawls WHERE user_id = $1)
		 ORDER BY created_at DESC
		 LIMIT 50`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CrawlRecord
	for rows.Next() {
		var r CrawlRecord
		if err := rows.Scan(&r.ID, &r.URLRaw, &r.URLNormalized, &r.URLHash, &r.Status,
			&r.EmbedderID, &r.PageCount, &r.ChunkCount, &r.QdrantCollection,
			&r.LastModified, &r.CreatedAt, &r.ReadyAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetUserCrawlByCrawlID(ctx context.Context, crawlID string) (*UserCrawlRecord, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, user_id, crawl_id, mcp_api_key_hash
		 FROM user_crawls WHERE crawl_id=$1
		 ORDER BY created_at DESC LIMIT 1`, crawlID,
	)
	var r UserCrawlRecord
	err := row.Scan(&r.ID, &r.UserID, &r.CrawlID, &r.MCPAPIKeyHash)
	if err != nil {
		return nil, err
	}
	return &r, nil
}
