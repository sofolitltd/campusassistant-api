# Session & Device Management

## Problem

The API uses stateless JWT tokens. Once issued, tokens remain valid until they
expire (access: ~15 min, refresh: ~7 days). There is no way to force-logout
devices or revoke existing sessions — even if a user deletes their
`UserDevice` records (FCM push tokens), existing JWTs keep working.

## Solution: Token Versioning

Add a `TokenVersion` counter to the `User` model and embed it in every access
token. The JWT middleware checks it on each request — if the user's current
`TokenVersion` is higher than the one in the JWT, the token is rejected.

This allows three operations:

| Action | Effect |
|--------|--------|
| **Logout all devices** | Increment `TokenVersion` → all existing JWTs instantly invalid |
| **Logout other devices** | Increment `TokenVersion` + re-issue a fresh access token → only the current session survives |
| **Remove a single device** | `DELETE /devices/:id` — deletes the device row (FCM push stops but session lives until token expires) |

---

## Implementation

### 1. Add `TokenVersion` to User model

**File:** `internal/domain/user.go`

```go
type User struct {
    Base
    // ... existing fields ...
    TokenVersion int `gorm:"default:1" json:"-"`
}
```

### 2. Embed `TokenVersion` in JWT claims

**File:** `pkg/auth/jwt.go`

```go
type Claims struct {
    UserID       uuid.UUID  `json:"user_id"`
    Email        string     `json:"email"`
    Role         string     `json:"role"`
    TokenVersion int        `json:"token_version"`
    UniversityID *uuid.UUID `json:"university_id,omitempty"`
    DepartmentID *uuid.UUID `json:"department_id,omitempty"`
    jwt.RegisteredClaims
}
```

Update `GenerateAccessToken` to accept `tokenVersion int` and set the claim.

### 3. Check `TokenVersion` in JWT middleware

**File:** `internal/delivery/http/middleware/jwt_middleware.go`

After `ValidateToken` succeeds:
1. Extract `user_id` from claims
2. Fetch the user from DB (`SELECT token_version FROM users WHERE id = ?`)
3. If `user.TokenVersion != claims.TokenVersion` → reject with
   `401 { "error": "Session expired, please login again" }`

> **Performance note:** This adds one PK lookup per JWT request. For higher
> traffic, cache `token_version` in Redis with a short TTL.

### 4. Update Auth Handler

**File:** `internal/delivery/http/handler/auth_handler.go`

| Endpoint | Change |
|----------|--------|
| `POST /auth/register` | No change — GORM default `1` |
| `POST /auth/login` | Read `user.TokenVersion` and pass to `GenerateAccessToken` |
| `POST /auth/refresh` | Read `user.TokenVersion` and pass to `GenerateAccessToken` |

### 5. Add logout endpoints

**File:** `internal/delivery/http/handler/device_handler.go`

#### `POST /devices/logout-all`

```go
func (h *DeviceHandler) LogoutAll(c *gin.Context) {
    userID := c.MustGet("user_id").(uuid.UUID)

    tx := h.db.WithContext(c.Request.Context()).Begin()

    // Increment token version — invalidates all existing JWTs
    tx.Model(&domain.User{}).Where("id = ?", userID).
        UpdateColumn("token_version", gorm.Expr("token_version + 1"))

    // Wipe all device records for this user
    tx.Where("user_id = ?", userID).Delete(&domain.UserDevice{})

    tx.Commit()

    c.JSON(http.StatusOK, gin.H{"message": "Logged out of all devices"})
}
```

#### `POST /devices/logout-others`

```go
func (h *DeviceHandler) LogoutOthers(c *gin.Context) {
    userID := c.MustGet("user_id").(uuid.UUID)

    tx := h.db.WithContext(c.Request.Context()).Begin()

    // Increment token version
    var newVersion int
    tx.Model(&domain.User{}).Where("id = ?", userID).
        UpdateColumn("token_version", gorm.Expr("token_version + 1"))
    tx.Raw("SELECT token_version FROM users WHERE id = ?", userID).
        Scan(&newVersion)

    // Wipe all devices for this user
    tx.Where("user_id = ?", userID).Delete(&domain.UserDevice{})

    tx.Commit()

    // Issue a fresh token so the current session survives
    token, _ := h.jwtManager.GenerateAccessToken(userID, ... , newVersion)

    c.JSON(http.StatusOK, gin.H{
        "message":      "Logged out of all other devices",
        "access_token": token,
    })
}
```

### 6. Update Router

**File:** `internal/delivery/http/router.go`

- Pass `db` _and_ `jwtManager` to `NewDeviceHandler(db, jwtManager)`
- Update constructor signature accordingly
- Add routes:

```go
deviceGroup.POST("/logout-all", deviceHandler.LogoutAll)
deviceGroup.POST("/logout-others", deviceHandler.LogoutOthers)
```

---

## Summary of files to change

| # | File | Change |
|---|------|--------|
| 1 | `internal/domain/user.go` | Add `TokenVersion int` field |
| 2 | `pkg/auth/jwt.go` | Add `TokenVersion` to `Claims`; update `GenerateAccessToken` |
| 3 | `internal/delivery/http/middleware/jwt_middleware.go` | DB lookup & version check after signature validation |
| 4 | `internal/delivery/http/handler/auth_handler.go` | Pass `TokenVersion` on login/refresh |
| 5 | `internal/delivery/http/handler/device_handler.go` | Add `LogoutAll`, `LogoutOthers`; update constructor |
| 6 | `internal/delivery/http/router.go` | Wire `jwtManager` into `DeviceHandler`; register new routes |
