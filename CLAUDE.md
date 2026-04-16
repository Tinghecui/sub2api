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
