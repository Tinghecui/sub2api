ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS original_cache_creation_tokens INTEGER NOT NULL DEFAULT 0;
