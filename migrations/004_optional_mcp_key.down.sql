UPDATE user_crawls SET mcp_api_key_hash = '' WHERE mcp_api_key_hash IS NULL;
ALTER TABLE user_crawls ALTER COLUMN mcp_api_key_hash SET NOT NULL;
