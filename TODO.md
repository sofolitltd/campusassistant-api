# TODO — Infrastructure

Open items found 2026-07-30. Nothing here is done yet. Ordered by priority.

---

## 1. `/health` returns 200 even when the database is down

**Priority:** high — blocks item 4.

`internal/delivery/http/router.go:34-46` pings the DB, correctly sets
`"database": "disconnected"` in the body, and then returns **HTTP 200 anyway**:

```go
r.GET("/health", func(c *gin.Context) {
    dbStatus := "connected"
    sqlDB, err := db.DB()
    if err != nil || sqlDB.Ping() != nil {
        dbStatus = "disconnected"
    }
    c.JSON(200, gin.H{ "status": "UP", "database": dbStatus, ... })  // <- always 200
})
```

Any uptime monitor watching status codes reports "all good" while the database
is dead. Until this is fixed, external monitoring is useless.

**Fix:** return `503 Service Unavailable` when the ping fails, and flip
`status` to `DOWN` so both the code and the body agree.

```go
r.GET("/health", func(c *gin.Context) {
    dbStatus, code, status := "connected", netHTTP.StatusOK, "UP"
    sqlDB, err := db.DB()
    if err != nil || sqlDB.Ping() != nil {
        dbStatus, code, status = "disconnected", netHTTP.StatusServiceUnavailable, "DOWN"
    }
    c.JSON(code, gin.H{
        "status": status, "database": dbStatus, "environment": cfg.Environment,
    })
})
```

`netHTTP` is already imported in `router.go` (aliased `net/http`).

**Verify:**
```bash
curl -i localhost:8080/health                    # 200, "status":"UP"
# stop postgres, then:
curl -i localhost:8080/health                    # expect 503, "status":"DOWN"
```

---

## 2. Postgres is exposed to the public internet in plaintext

**Priority:** high — security.

`DATABASE_URL` points at `103.191.50.60:54321` with `sslmode=disable`.
`103.191.50.60` is a **public** IP, so:

- Anyone on the internet can reach port 54321 and brute-force the password.
- `sslmode=disable` means the password and every row of query traffic cross
  the network unencrypted.

**Fix — do both:**

**(a) Stop exposing the port.** In Coolify, open the Postgres resource and
remove the public port mapping. The API and the database are on the same
Docker network, so the API should connect by internal service name instead:

```
DATABASE_URL=postgresql://user:pass@<coolify-service-name>:5432/campusassistant?sslmode=disable
```

`sslmode=disable` is acceptable once traffic never leaves the Docker bridge.
Update the var in the Coolify dashboard, then restart the API.

**(b) Firewall it anyway.** If direct access from a laptop is still wanted,
keep the port but restrict it — defence in depth, and it survives a Coolify
config mistake:

```bash
sudo ufw deny 54321
sudo ufw allow from <your.ip.address> to any port 54321 proto tcp
sudo ufw status numbered
```

For occasional access, prefer an SSH tunnel over an open port entirely:

```bash
ssh -L 54321:localhost:54321 user@103.191.50.60
# then connect to localhost:54321 from the laptop
```

**Verify from a machine that is NOT the laptop** (phone hotspot, or
<https://www.yougetsignal.com/tools/open-ports/>): port 54321 should read as
closed/filtered.

**Note:** after changing `DATABASE_URL`, check whether anything else uses the
public endpoint — the `campusassistant-migration` Python scripts have their own
`config.py` and will break when the port closes.

---

## 3. No database backups exist

**Priority:** high — this is the one that loses the business.

Postgres is self-hosted on the VPS. No managed provider is backing it up. If
that disk fails or the container is deleted, **all data is gone permanently**.

**Fix — Coolify's built-in backups to Cloudflare R2.** R2 is S3-compatible and
credentials already exist in `.env` (`R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`,
`R2_ACCOUNT_ID`).

1. **Create a separate R2 bucket** for backups, e.g. `campusassistant-backups`.
   Do **not** reuse the uploads bucket — different lifecycle rules, and the
   uploads bucket is public.
2. Generate an R2 API token scoped to **that bucket only**. Do not reuse the
   uploads token.
3. In Coolify: **Settings → S3 Storages → Add**, using the R2 S3 endpoint
   `https://<R2_ACCOUNT_ID>.r2.cloudflarestorage.com`, region `auto`, and the
   new bucket + token.
4. Open the Postgres resource → **Backups** tab → add a scheduled backup:
   - Frequency: daily, cron `0 3 * * *` (03:00, off-peak).
   - Destination: the S3 storage from step 3.
   - Retention: 7 daily. Add a weekly `0 3 * * 0` job with ~4 retained if
     monthly history is wanted — corruption often isn't noticed the same day.
   - Enable "Backup now" once to confirm the whole path works.
5. Confirm the dump object actually appears in the R2 bucket, with a
   non-trivial file size.

**Manual fallback**, if Coolify's backup UI misbehaves — cron on the host:

```bash
#!/usr/bin/env bash
set -euo pipefail
STAMP=$(date +%F-%H%M)
FILE=/tmp/campusassistant-$STAMP.sql.gz
docker exec <postgres-container> pg_dump -U <user> campusassistant | gzip > "$FILE"
aws s3 cp "$FILE" "s3://campusassistant-backups/$(date +%Y/%m)/" \
    --endpoint-url "https://<R2_ACCOUNT_ID>.r2.cloudflarestorage.com"
rm -f "$FILE"
```

### Restore test — do not skip

**An untested backup is not a backup.** Once now, then occasionally:

1. Download the newest dump from R2.
2. Restore into a *local* throwaway database — never over production:
   ```bash
   createdb campusassistant_restoretest
   gunzip -c campusassistant-<stamp>.sql.gz | psql campusassistant_restoretest
   ```
3. Point a local API at it (`DATABASE_URL=...campusassistant_restoretest`,
   `DB_AUTO_MIGRATE=false`) and run `go run ./cmd/api/main.go`.
4. Hit `/health` and spot-check a few endpoints. Confirm row counts look sane
   (`users`, `subscriptions`).
5. Drop the test database.

Write the date of the last successful restore test here: **never tested**

---

## 4. No alerting — nothing tells us when the API or DB goes down

**Priority:** medium. Depends on item 1.

Key constraint: **the server cannot report its own death.** Anything running on
the VPS dies with it, so alerting must originate outside.

- **External uptime monitor** — UptimeRobot, BetterStack or Healthchecks.io
  (free tiers are sufficient). Monitor `https://campusassistant.duckdns.org/health`
  every 1–5 minutes, alert to email/Telegram. Once item 1 is done, this single
  check covers both "API down" and "DB down".
- **Coolify notifications** — Settings → Notifications → Telegram/Discord/email.
  Covers container crashes, restart loops and failed deploys. Complements the
  external monitor; does not replace it.
- **Disk-full alerting** — the most common way a small VPS dies. Postgres stops
  accepting writes when the disk fills, and it fills silently. Alert at 80%.

---

## 5. Deploy process ships an 85MB binary through git

**Priority:** low — works fine, but worth revisiting.

Per `coolify_deploy.md`, `make build` produces a linux/amd64 binary that is
committed to `main` (Coolify build method: **None**). Every deploy adds ~85MB
to git history permanently, and the repo will keep getting slower to clone.

Options if it becomes painful: switch Coolify to build from the existing
`Dockerfile`, or publish the binary as a GitHub Release asset instead of a
tracked file.
