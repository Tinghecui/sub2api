# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Sub2API is an AI API Gateway Platform that distributes and manages API quotas from AI subscriptions (Claude, OpenAI, Gemini, etc.). It proxies requests to upstream providers while handling auth, billing, load balancing, and multi-account management.

**Run modes:** `standard` (full SaaS with billing) and `simple` (hides SaaS features, skips quota checks).

## Tech Stack

- **Backend:** Go 1.26.2, Gin (HTTP), Ent (ORM), Wire (DI)
- **Frontend:** Vue 3 + TypeScript, Vite, TailwindCSS, Pinia, pnpm (NOT npm)
- **Database:** PostgreSQL 15+ with Redis 7+ for caching/rate-limiting
- **Payments:** Stripe, Alipay, WeChat Pay (provider registry pattern)

## Build & Dev Commands

```bash
# Full build (frontend + backend)
make build

# Backend only
cd backend && make build

# Frontend only
cd frontend && pnpm install && pnpm run build

# Run backend dev server
cd backend && go run ./cmd/server/

# Run frontend dev server
cd frontend && pnpm dev

# Ent ORM codegen (after modifying ent/schema/*.go)
cd backend && go generate ./ent

# Wire DI codegen
cd backend && go generate ./cmd/server
```

## Testing

```bash
# All tests
make test

# Backend unit tests
cd backend && go test -tags=unit ./...

# Backend integration tests (needs Docker)
cd backend && go test -tags=integration ./...

# Backend lint
cd backend && golangci-lint run ./...

# Frontend lint + typecheck
cd frontend && pnpm run lint:check
cd frontend && pnpm run typecheck
```

## Architecture

```
Request → Gin Middleware (JWT/API Key auth, rate limiting, CORS, logging)
        → Handler (HTTP layer)
        → Service (business logic)
        → Repository (data access via Ent ORM + Redis cache)
        → PostgreSQL / Redis
```

**Gateway routing:** `/v1/messages` (Claude), `/v1/chat/completions` (OpenAI), `/v1beta/*` (Gemini native). Supports sticky sessions via `session_id` header for consistent upstream account routing.

**Key backend directories:**
- `backend/cmd/server/` — entry point, Wire DI setup
- `backend/ent/schema/` — Ent schema definitions (edit these)
- `backend/ent/` — generated Ent code (do NOT edit, run `go generate ./ent`)
- `backend/internal/handler/` — HTTP handlers by domain
- `backend/internal/service/` — business logic
- `backend/internal/repository/` — data access layer
- `backend/internal/server/routes/` — route registration
- `backend/internal/middleware/` — auth, rate limiting, logging
- `backend/internal/payment/` — payment provider integrations
- `backend/migrations/` — SQL migration files

**Frontend:** SPA embedded into the Go binary at compile time (`-tags embed`). Output goes to `backend/internal/web/dist/`.

## CI/CD

- **backend-ci.yml:** unit + integration tests, golangci-lint v2.9 (Go 1.25.7 in CI)
- **security-scan.yml:** govulncheck, gosec, pnpm audit
- **release.yml:** triggered by `v*` tags, builds multiarch Docker images

## Critical Gotchas

1. **Always use pnpm** for frontend — npm will cause CI failures. Commit `pnpm-lock.yaml` when dependencies change.
2. **Ent schema changes** require `go generate ./ent` and committing the generated files.
3. **Interface changes** require updating all test stubs/mocks that implement the interface.
4. **Config priority:** env vars > `config.yaml` > defaults. Env vars use prefixes: `SERVER_*`, `DATABASE_*`, `REDIS_*`, `JWT_*`, `TOTP_*`.
5. **Secrets encryption:** TOTP secrets and OAuth tokens are AES-encrypted. `TOTP_ENCRYPTION_KEY` must be fixed across instances.
6. **Batch account edits:** never mix different platform accounts (OpenAI + Gemini) in batch operations — model mappings can get corrupted.

## PR Checklist

- `go test -tags=unit ./...` passes
- `go test -tags=integration ./...` passes
- `golangci-lint run ./...` clean
- `pnpm-lock.yaml` synced (if `package.json` changed)
- Ent generated code committed (if schema changed)
- All test stubs updated (if interfaces changed)

## Current Work Scope

当前讨论范围只关注 **Anthropic 平台** 的 group（`platform = "anthropic"`）。OpenAI / Gemini 等其他平台暂不在改动范围内。

## Hard-Won Lessons (踩坑记录)

### 1. Ent Select 字段陷阱
`GetByKeyForAuth` (`backend/internal/repository/api_key_repo.go`) 使用显式 `.Select(...)` 优化性能。**新增 Group 字段必须手动加入这个 Select 列表**，否则 auth cache snapshot 中该字段静默为零值。`GetByID` / `GetByIDLite` 不受影响（无 Select 限制）。

教训来源：cache inflate 功能上线后流式路径完全不生效，花了多轮部署和 debug 日志才定位。

### 2. 计费走 5m/1h 分桶而非聚合值
`billing_service.go` 的 `computeCacheCreationCost()` 在 `SupportsCacheBreakdown=true` 时（LiteLLM 动态定价自动启用），用 `ephemeral_5m_tokens + ephemeral_1h_tokens` 计费，**不用** `cache_creation_input_tokens` 聚合值。

因此，任何修改 cache_creation 的逻辑都必须**同步修改 5m/1h 分桶**。当前 inflate 策略：`ephemeral_5m = inflated_aggregate`, `ephemeral_1h = 0`。

### 3. Auth Cache 层级
L1 内存（15s） → L2 Redis（5min） → DB 回源。Snapshot 有 `apiKeyAuthSnapshotVersion` 版本号（当前 v6），版本不匹配自动回源。修改 Group/APIKey 后如果需要立即生效，重启 Redis + sub2api。

### 4. 计费与日志分离
`applyUsageBilling()` 原子事务先扣费 → `writeUsageLogBestEffort()` 异步写日志。日志写入失败**不影响扣费**。usage_logs INSERT 使用显式 47+ 列 raw SQL（非 ORM），新增列需要同步修改 `prepareUsageLogInsert()`。

### 5. Group 软删除与 Fallback 引用
Groups 使用 `SoftDeleteMixin`。删除 group 后，其他 group 的 `fallback_group_id` 引用不会自动清除。前端编辑保存时会带着失效的引用，导致 `validateFallbackGroup` 返回 404。

## Production Server (64.186.230.163)

- **Service:** systemd `sub2api.service`, user `sub2api`
- **Binary:** `/opt/sub2api/sub2api`
- **Config:** `/opt/sub2api/config.yaml`
- **DB:** PostgreSQL localhost:5432, user=sub2api, db=sub2api
- **Redis:** localhost:6379, no password
- **Audit Log:** Cloudflare R2 bucket `sub2api-audit-logs`

### 部署流程
```bash
# 1. 本地编译（需要先 pnpm build 前端）
cd frontend && npx pnpm run build
cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -o ../sub2api-linux ./cmd/server/

# 2. 上传（不替换旧的）
scp sub2api-linux root@64.186.230.163:/opt/sub2api/sub2api.new

# 3. DB migration（零停机，先于二进制切换）
ssh root@64.186.230.163 "PGPASSWORD='Sub2api@2026' psql -h localhost -U sub2api -d sub2api < migration.sql"

# 4. 切换 + 重启（~2s 停机）
ssh root@64.186.230.163 "cd /opt/sub2api && chown sub2api:sub2api sub2api.new && chmod +x sub2api.new && mv sub2api sub2api.old && mv sub2api.new sub2api && systemctl restart sub2api"
```

### 回滚
```bash
ssh root@64.186.230.163 "cd /opt/sub2api && mv sub2api sub2api.new && mv sub2api.old sub2api && cp config.yaml.bak config.yaml && systemctl restart sub2api"
```
