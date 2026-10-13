# JWT Authentication

Reference for the auth system: token format, every `/api/v1/auth/*` endpoint,
and how protected routes are enforced.

## Tokens

Implemented in `pkg/auth/jwt.go`, signed HS256 with `JWT_SECRET`.

| Token | Lifetime (env, default) | Claims |
|---|---|---|
| Access | `JWT_ACCESS_TOKEN_EXPIRY` minutes, default `60` | `user_id`, `email`, `role`, `token_version`, `university_id?`, `department_id?`, plus standard `exp`/`iat`/`nbf`/`iss` (`iss: "campusassistant-api"`) |
| Refresh | `JWT_REFRESH_TOKEN_EXPIRY` hours, default `168` (7 days) | Standard claims only (`sub` = user ID) — no role/email, so a refresh token alone can't authorize a request |

`token_version` exists so sessions can be revoked before a token's natural
expiry: it's stored on `User` (and compared on every request, see
`JWTMiddleware` below) and incremented to invalidate outstanding tokens —
there is no token blocklist/table to maintain.

Passwords are hashed with bcrypt (cost `10`, `pkg/auth/password.go`),
minimum length 8.

## Logout / invalidating sessions

There's no `POST /auth/logout` — stateless JWTs can't be individually revoked,
so "logout" means bumping `token_version` so every token issued before that
point stops passing `JWTMiddleware`'s check. Two endpoints do this
(`internal/delivery/http/handler/device_handler.go`, both **JWT**):

| Method & Path | Effect |
|---|---|
| `POST /api/v1/devices/logout-all` | Increments `token_version` and deletes all `UserDevice` rows (FCM push tokens) for the user. Every existing access token, including the one making this request, stops working. |
| `POST /api/v1/devices/logout-others` | Same increment + device wipe, but immediately reissues a fresh access token for the *current* session so only other devices are logged out. Response includes the new `access_token`. |

`POST /auth/change-password` (see below) also bumps `token_version` when
`logout_other_devices` is true (the default).

Removing a single device (`DELETE /devices/:id`) only deletes that device's
row — push notifications stop, but its session's JWT remains valid until it
expires naturally; it doesn't touch `token_version`.

## Roles

Defined in `internal/domain/user.go`: `super_admin`, `university_admin`,
`department_admin`, `teacher`, `student`, `staff`. There's a separate
`admins` table/login for staff accounts (`admin-login` below) — its `Role`
field isn't restricted to this enum.

Enforcement is explicit per-route middleware chaining
(`middleware.RoleMiddleware("teacher", "staff", ...)` on the routes that need
it in `internal/delivery/http/router.go`), not an annotation/decorator
pattern — check the route's middleware chain in `router.go` to see what a
given endpoint actually requires.

## Endpoints

All under `/api/v1/auth` (`internal/delivery/http/router.go`, handlers in
`internal/delivery/http/handler/auth_handler.go` and
`password_reset_handler.go`). Routes marked **JWT** require
`Authorization: Bearer <access_token>`.

| Method & Path | Body | Response |
|---|---|---|
| `POST /register` | `email, password (min 8), first_name, last_name, phone, gender, university_id, department_id` (`role` is accepted but ignored — always created as `student`) | `201` `AuthResponse{access_token, refresh_token, user, expires_in}` |
| `POST /login` | `email, password` | `200` `AuthResponse` (user loaded with Student/Teacher/Staff relations) |
| `POST /admin-login` | `email, password` | `200` `{access_token, refresh_token, expires_in, admin:{id,email,name,role}}` — checks the `admins` table, not `users` |
| `POST /refresh` | `refresh_token` | `200` `{access_token, expires_in}` — no new refresh token is issued |
| `POST /forgot-password` | `email, account_type ("user"\|"admin", default "user")` | `200` generic message always (enumeration-safe); rate-limited per-IP and per-email |
| `POST /verify-reset-code` | `email, account_type, code (6 digits)` | `200` `{reset_token, expires_in}` — opaque random token, **not** a JWT, stored SHA-256-hashed |
| `POST /reset-password` | `email, account_type, reset_token, new_password` | `200` message. For `user` accounts this bumps `token_version`, invalidating every existing session; `admins` have no `token_version` so existing admin sessions survive a reset |
| **JWT** `GET /me` | — | `200` full `domain.User` |
| **JWT** `POST /change-password` | `old_password, new_password, logout_other_devices (bool, default true)` | `200` `AuthResponse` with reissued tokens; optionally bumps `token_version` and clears `UserDevice` rows |

There is no `POST /auth/logout` — see [Logout / invalidating sessions](#logout--invalidating-sessions) below.

## Middleware (`internal/delivery/http/middleware/`)

- **`jwt_middleware.go` — `JWTMiddleware(jwtManager, db)`**: reads
  `Authorization: Bearer <token>` (falls back to a `?token=` query param only
  for WebSocket upgrade requests, since browsers can't set headers on the
  upgrade handshake). Validates the signature/expiry, then re-checks
  `token_version` against the DB (`admins` are hardcoded to version `1`;
  `users.token_version` otherwise) so a revoked session dies immediately
  instead of waiting out the token's `exp`. Failure responses:
  - expired → `401 {"error": "Token has expired"}`
  - invalid signature/shape → `401 {"error": "Invalid token"}`
  - stale `token_version` → `401 {"error": "Session expired, please login again"}`

  On success it sets Gin context keys `user_id`, `user_email`, `user_role`,
  `university_id`, `department_id` for downstream handlers/middleware to read.

- **`RoleMiddleware(allowedRoles ...string)`**: 403
  `{"error": "Insufficient permissions"}` if `user_role` isn't in the list.

- **`UniversityMiddleware()` / `DepartmentMiddleware()`**: require a non-nil
  `university_id`/`department_id` in context — for routes scoped to a
  specific institution.

- **`access_middleware.go` — `Guard`**: a newer, stricter alternative
  (`Policy`: `Public | AuthJWT | AdminJWT`, paired into `Access{Read, Write}`)
  being rolled out route-by-route. Controlled by `ACCESS_ENFORCE`
  (default on in development): when off, policy violations are logged but not
  blocked, so a misclassified route fails loud in logs before it fails loud
  in production.

## Example

```bash
# Login
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"SecurePass123!"}'

# Call a protected route
curl http://localhost:8080/api/v1/auth/me \
  -H "Authorization: Bearer <access_token>"
```
