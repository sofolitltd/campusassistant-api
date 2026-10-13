# Database Migrations

How the schema is kept in sync with the Go models, and what you run and when.

## TL;DR

```bash
make run       # starts the API — never touches the schema
make migrate   # applies schema changes — run this after pulling or writing one
```

If you didn't add/rename a field on a `domain.*` struct, you don't need to run `make migrate`.

## Why this exists

This project has no `golang-migrate`/`goose` SQL files — the schema is derived straight from the Go structs in `internal/domain/` via [GORM AutoMigrate](https://gorm.io/docs/migration.html). That used to run automatically on *every* `make run`, which meant every boot re-diffed ~100 models against the live database before the server could accept a single request. On a laptop that's a multi-second tax on every single dev iteration, for a step that's a no-op 99% of the time.

Now `cmd/api` never migrates — it only serves traffic. Schema changes go through a separate command, `cmd/migrate`, that you run explicitly, exactly once, right after you change a model.

## Day-to-day workflow

1. **Pulled someone else's branch, or about to start a feature that touches the DB?**
   ```bash
   make migrate
   ```
   This applies any new/changed `domain.*` fields to your local database. Safe to run even when nothing changed — it's a no-op in that case, just a few seconds of GORM diffing.

2. **Just start the API as usual:**
   ```bash
   make run
   ```
   This boots immediately — no schema diffing, no migration logging — because step 1 already brought the schema up to date.

3. **Added/renamed a field on a `domain.*` struct as part of your feature?**
   Run `make migrate` again before you test the feature. Rinse and repeat as you iterate on the model.

Rule of thumb: **`make migrate` is a schema command, `make run` is a server command.** You don't run the first one every time you run the second.

## How `RunMigrations` works (`internal/repository/postgres/connection.go`)

Two kinds of steps happen inside `RunMigrations`:

- **`db.AutoMigrate(...)` over the full model list.** This always runs when `make migrate` is invoked — GORM always re-diffs every model against the DB. This is correct and cheap *on-demand*; it was only a problem when it ran on every API boot.
- **One-off legacy cleanup/backfill steps** (dropping old columns, renaming shapes, backfilling data from an earlier schema). These used to re-run their `HasTable`/`HasColumn` checks on every single boot, forever, even though each one only ever does real work once. They're now wrapped in a tiny helper:

  ```go
  once(db, "drop_users_fcm_token", func() error {
      // ... the actual cleanup, unchanged ...
  })
  ```

  `once` checks a ledger table, `schema_migrations` (`internal/domain/schema_migration.go`), for the given key. If it's already recorded, the step is skipped entirely — no DB round trip, no log line. If not, it runs the step and records it. This means:
  - Old cleanup steps stop costing anything at all once they've applied.
  - Adding a new one-time cleanup/backfill in the future is just: wrap it in `once(db, "some_unique_key", func() error { ... })`.
  - **Don't reuse a key, and don't delete a key's `once(...)` call while rows for it still exist in the ledger** — the ledger is keyed by string, so a stale key pointing at removed code is harmless, but reusing a key for unrelated new logic will cause that new logic to silently never run on existing databases.

## Production (Coolify deploy)

There is no CI/CD pipeline or separate release step here (see [DEPLOY_COOLIFY.md](DEPLOY_COOLIFY.md)) — deploy is "build locally, push, Coolify restarts the binary." `DATABASE_URL` in `.env` currently points at the same Postgres instance the deployed API uses, so `make migrate` run from a dev machine *is* the production migration step.

Order matters: run `make migrate` **before** `make build && git push` for any change that adds/renames a `domain.*` field — the currently-running (old) binary tolerates new nullable columns fine, but a new binary querying a column that doesn't exist yet will not.

If this ever moves to a real CI/CD pipeline or a separate staging database, add a dedicated migrate step to that pipeline (`go run ./cmd/migrate` against the target DB, gated before the new version takes traffic) instead of reintroducing boot-time migration in `cmd/api`.

## Adding a new domain model

1. Add the struct to `internal/domain/`.
2. Add it to the `db.AutoMigrate(...)` call in `internal/repository/postgres/connection.go`.
3. Run `make migrate`.
4. Build the repository/usecase/handler layers as usual.
