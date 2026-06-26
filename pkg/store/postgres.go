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

type CrawlPage struct {
	URL        string
	Title      string
	ChunkCount int
	CrawledAt  time.Time
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

// UpsertUserByEmail inserts a new user with the given key hash.
// If the email already exists AND already has a key hash, it does NOT overwrite it
// and returns hasKey=true. If the user exists but has no key hash, it sets the hash.
func (s *PostgresStore) UpsertUserByEmail(ctx context.Context, r *UserRecord) (bool, error) {
	var existing string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, platform_api_key_hash, created_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (email)
		 DO UPDATE SET platform_api_key_hash = COALESCE(users.platform_api_key_hash, EXCLUDED.platform_api_key_hash)
		 RETURNING platform_api_key_hash`,
		r.ID, r.Email, r.PlatformAPIKeyHash,
	).Scan(&existing)
	if err != nil {
		return false, err
	}
	// If the returned hash differs from what we tried to insert, an existing hash was kept.
	return existing != r.PlatformAPIKeyHash, nil
}

// RotateUserKey unconditionally replaces the key hash for an existing user.
// Used when the user explicitly requests a new key.
func (s *PostgresStore) RotateUserKey(ctx context.Context, email, keyHash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET platform_api_key_hash = $1 WHERE email = $2`,
		keyHash, email,
	)
	return err
}

func (s *PostgresStore) FindUserByEmail(ctx context.Context, email string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1`, email,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return id, err
}

// VerifyBetterAuthSession queries the Better Auth session + user tables (same Postgres, shared DB).
// Both services use DATABASE_URL — no shared secret is needed; the token itself is the proof.
func (s *PostgresStore) VerifyBetterAuthSession(ctx context.Context, token string) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx,
		`SELECT u.email
		 FROM "session" s
		 JOIN "user" u ON u.id = s."userId"
		 WHERE s.token = $1 AND s."expiresAt" > now()`,
		token,
	).Scan(&email)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return email, err
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

func (s *PostgresStore) GetCrawlPages(ctx context.Context, crawlID string) ([]*CrawlPage, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT url, title, chunk_count, crawled_at FROM crawl_pages
		 WHERE crawl_id = $1 ORDER BY crawled_at ASC`,
		crawlID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CrawlPage
	for rows.Next() {
		var pg CrawlPage
		if err := rows.Scan(&pg.URL, &pg.Title, &pg.ChunkCount, &pg.CrawledAt); err != nil {
			return nil, err
		}
		out = append(out, &pg)
	}
	return out, rows.Err()
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

func (s *PostgresStore) ListUserCrawls(ctx context.Context, userID string, limit, offset int) ([]CrawlRecord, bool, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, url_raw, url_normalized, url_hash, status, embedder_id,
		        page_count, chunk_count, qdrant_collection, last_modified, created_at, ready_at
		 FROM crawls
		 WHERE id IN (SELECT DISTINCT crawl_id FROM user_crawls WHERE user_id = $1)
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		userID, limit+1, offset,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []CrawlRecord
	for rows.Next() {
		var r CrawlRecord
		if err := rows.Scan(&r.ID, &r.URLRaw, &r.URLNormalized, &r.URLHash, &r.Status,
			&r.EmbedderID, &r.PageCount, &r.ChunkCount, &r.QdrantCollection,
			&r.LastModified, &r.CreatedAt, &r.ReadyAt); err != nil {
			return nil, false, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
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
