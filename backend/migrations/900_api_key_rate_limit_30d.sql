-- Add the 30d rate limit window to api_keys table
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS rate_limit_30d decimal(20,8) NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS usage_30d decimal(20,8) NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS window_30d_start timestamptz;
