# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Go REST backend (Gin + GORM + PostgreSQL) for the Campus Assistant platform — serves the `campusassistant` Flutter app and the `campusassistant-admin` Next.js dashboard. JWT auth for end-users, a blanket `X-API-Key` gate for the whole API, Cloudflare R2 for file storage, Firebase for push, bKash for payments.

## Commands

- `make run` — `go run ./cmd/api/main.go`, starts on `:8080` (or `$PORT`)
- `make build` — cross-compiles a linux/amd64 binary to `./campusassistant-api`
- `make reset-db` — runs `scripts/reset_db.go`
- No automated tests exist (`go test` has nothing to run — no `*_test.go` files anywhere). Manual API testing is done via `api_tests.rest` / `auth_tests.rest` (VS Code REST Client format).
- No linter is configured beyond `go vet`/`gofmt` conventions; no CI workflow exists.
- **Swagger is not actually implemented** — despite README/PLAN.md mentioning it, there's no `docs/` folder, no `swag` codegen, and no `/swagger` route. Don't assume it exists.

## Config

Viper loads `.env` (falls back to real env vars if the file is missing) — see `.env.example`. Key vars: `DATABASE_URL`, `PORT`, `ENVIRONMENT` (dev enables auto-migrate), `DB_AUTO_MIGRATE`, `API_KEY`, `JWT_SECRET` + `JWT_ACCESS_TOKEN_EXPIRY`/`JWT_REFRESH_TOKEN_EXPIRY`, `R2_ACCESS_KEY_ID`/`R2_SECRET`/`R2_BUCKET_NAME`/`R2_ACCOUNT_ID`/`R2_PUBLIC_URL`, `FIREBASE_CREDENTIALS_FILE`/`FIREBASE_CREDENTIALS_JSON` (push disabled silently if unset), `BKASH_*` (sandbox by default, prod via `BKASH_PRODUCTION`).

`cmd/api/main.go` also starts two background goroutines on boot: an hourly subscription-expiry sweep and a daily deleted-message cleanup (90-day cutoff).

## Architecture

Clean-architecture layering: **`domain` → `repository` → `usecase` → `delivery/http/handler`**.

- `internal/domain/` — one file per entity + `repository.go` (generic `Repository[T]` interface: Create/GetByID/GetAll/Update/Delete/HardDelete), `base.go` (shared `Base` struct), `errors.go`.
- `internal/repository/postgres/` — GORM impls. `repository.go` has `GormRepository[T]` (generic, via `NewGormRepository[T](db)`), with schema-introspected search filtering, IN-clause list filters, and a "smart linkage" hack that back-fills `User.Student`/`User.Teacher` by email when the relation is missing. Dedicated repos exist per-resource only when queries don't fit the generic shape (club, association, course, career, order, merchant, product, chat, community).
- `internal/usecase/` — thin pass-through today: `generic_usecase.go`'s `genericUsecase[T]` just wraps `domain.Repository[T]`. Only `chat_usecase.go` and `community_usecase.go` have real logic.
- `internal/delivery/http/handler/` — Gin handlers. `generic_handler.go`'s `GenericHandler[T]` binds standard CRUD to routes and auto-sets audit fields (`domain.Auditable`: `SetCreatedBy`/`SetUpdatedBy` from `c.Get("user_id")`) on Create. ~25 dedicated handlers exist for resources needing custom behavior (auth, clubs, associations, merchants, orders, chat, community, lost & found, career, subscriptions, bkash, uploads, notifications, devices).
- `internal/service/` — cross-cutting services injected straight into handlers (not usecases): `NotificationService`, `DeviceTopicService`, `PaymentService` (bKash), `MarketplacePaymentService`, `CareerReminderScheduler`.
- `internal/delivery/http/websocket/` — hubs for chat and notifications.
- `internal/delivery/http/route/` exists as a directory but is **empty** — all routing lives in one file, `internal/delivery/http/router.go` (~800 lines, `NewRouter(cfg, db)`).

**Purely-generic resources** (university, department, faculty, batch, notice, contributor, staff, verification, transport, attachment, hall, organization, alumni, routine, course-category, course-prefix, marketplace-category, lost-found-category, career-circular-category, emergency-contact, skill-video, ...) have no dedicated files at all — they're wired via a `registerRoutes[T](group, db, path)` helper at the bottom of `router.go` that builds a generic repo → usecase → handler stack and registers standard CRUD routes. A resource only "graduates" to dedicated repo/handler files once it needs non-CRUD behavior (follow/join, approval workflow, ownership-scoped `/my/...` routes, denormalized counters) — `router.go` has comments explaining this for Club, Association, Skill, Merchant. **Follow this same pattern for new resources**: start generic, only add dedicated files when the generic CRUD genuinely doesn't fit.

## Auth

Two independent, stackable middlewares (`internal/delivery/http/middleware/`):
- `APIKeyMiddleware` — checks `X-API-Key` against `cfg.APIKey`; **no-ops if `APIKey` is empty** (dev convenience). Applied blanket to the whole `/api/v1` group after the public `/auth` and `/proxy` routes.
- `JWTMiddleware` — reads `Authorization: Bearer <token>` or a `?token=` query param (needed for WebSocket connections, which can't set headers). Validates the JWT, then does a **DB lookup of `token_version`** and compares it against the claim — this is the session-invalidation mechanism. Sets `user_id`/`user_email`/`user_role`/`university_id`/`department_id` into Gin context. Layered on top of the API key requirement for specific groups (`/auth/me`, `/my/*`, `/ws/*`, `/community/*`, `/payments/*`, likes, etc.).

`RoleMiddleware`/`UniversityMiddleware`/`DepartmentMiddleware` exist in `jwt_middleware.go` but are **not currently used anywhere** — RBAC scaffolding exists but isn't wired into routes; don't assume role checks are enforced unless you see a handler explicitly reading `c.Get("user_role")`.

**Session management** (see `SESSION_MANAGEMENT.md` for the full design): every user has a `TokenVersion int` column; it's embedded in every access token. `POST /devices/logout-all` increments it (kills every session everywhere); `POST /devices/logout-others` increments + immediately re-issues a token for the current session only.

WebSocket routes (`/ws/chat/:id`, `/ws/notifications`) are registered on the raw router (not the `/api/v1` group) — JWT-gated only, no API key.

## Storage

`pkg/storage/r2.go` wraps Cloudflare R2 (S3-compatible, AWS SDK v2). `UploadFile`/`UploadReader` return public URLs; `PutObjectAt` writes without a public URL (for private objects); `GetPresignedURL(path, ttl)` is the only way private objects (e.g. merchant verification docs) are ever served. Public unauthenticated `/upload` exists alongside a JWT-gated, ownership-tracked `/my/upload` that routes "sensitive" folders to private+presigned storage.
