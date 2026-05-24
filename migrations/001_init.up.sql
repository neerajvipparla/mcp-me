CREATE TABLE IF NOT EXISTS users (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email                TEXT NOT NULL UNIQUE,
    platform_api_key_hash TEXT NOT NULL UNIQUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS crawls (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url_raw             TEXT NOT NULL,
    url_normalized      TEXT NOT NULL,
    url_hash            TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'queued',
    embedder_id         TEXT NOT NULL,
    page_count          INT NOT NULL DEFAULT 0,
    chunk_count         INT NOT NULL DEFAULT 0,
    qdrant_collection   TEXT NOT NULL,
    last_modified       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    ready_at            TIMESTAMPTZ,
    UNIQUE(url_hash, embedder_id)
);

CREATE TABLE IF NOT EXISTS user_crawls (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id),
    crawl_id         UUID NOT NULL REFERENCES crawls(id),
    mcp_api_key_hash TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS crawl_pages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    crawl_id    UUID NOT NULL REFERENCES crawls(id),
    url         TEXT NOT NULL,
    title       TEXT,
    chunk_count INT NOT NULL DEFAULT 0,
    crawled_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_crawls_hash_embedder  ON crawls(url_hash, embedder_id);
CREATE INDEX IF NOT EXISTS idx_crawl_pages_crawl_id  ON crawl_pages(crawl_id);
CREATE INDEX IF NOT EXISTS idx_user_crawls_crawl_id  ON user_crawls(crawl_id);
