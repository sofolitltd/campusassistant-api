# Coolify Deploy — Campus Assistant API

## How it works

Build binary locally → push to git → Coolify pulls & runs it.

## Steps

### 1. Build
```bash
make build
```
Produces `campusassistant-api` (linux/amd64, ~85MB).

### 2. Push to main
```bash
git add campusassistant-api
git commit -m "build: deploy"
git push origin main
```

### 3. Coolify
- Watches `main` branch of `github.com/sofolitltd/campusassistant-api`
- Auto-pulls & restarts on new commits

## First-time Coolify setup
| Setting | Value |
|---|---|
| Source | Git (`main` branch) |
| Build method | **None** (binary pre-compiled) |
| Start command | `./campusassistant-api` |
| Port | `8080` |
| Env vars | Coolify dashboard (use `.env` values) |

## Notes
- Binary is tracked in git — don't `.gitignore` it
- Don't pass `-ldflags="-s -w"` — keeps binary analyzable for debugging
- Coolify auto-deploys within seconds of a `git push`
