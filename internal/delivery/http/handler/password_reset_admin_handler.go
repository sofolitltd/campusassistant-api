package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PasswordResetAdminHandler is the admin dashboard's window into the
// self-service forgot-password flow (password_reset_handler.go). Two jobs:
//
//	List      — show every reset request (who, when, from which IP, which
//	            stage it reached) plus whether the emailed code actually
//	            went out (correlated from email_logs).
//	IssueCode — the repair path when the email never arrives: mint a fresh
//	            code and return it once in the response so support can read
//	            it to the user over a verified channel. Nothing sensitive is
//	            stored beyond the existing bcrypt hash, and the user still
//	            finishes through the normal verify-code/reset-password steps
//	            (which bump TokenVersion for us).
//
// AdminJWT-gated in router.go — never wire it lower.
//
// A deliberate asymmetry with the public flow: the admin endpoints may
// confirm/deny "account exists" with a plain 404, since their caller is
// already an authenticated admin; the public flow must stay silent.
type PasswordResetAdminHandler struct {
	db *gorm.DB
}

func NewPasswordResetAdminHandler(db *gorm.DB) *PasswordResetAdminHandler {
	return &PasswordResetAdminHandler{db: db}
}

// emailLogTolerance bounds the clock-difference window when correlating an
// email_logs row with its reset row: the mailer fires right after the reset
// row is created, so a log whose timestamp is within a few minutes is enough
// to match, without wiring a foreign key the two flows never had.
const emailLogTolerance = 3 * time.Minute

// PasswordResetView is the safe serialization of a PasswordReset row. The
// domain struct is all json:"-" by design (CodeHash/TokenHash must never
// leave the server), so this view re-exposes only the support-relevant
// metadata. No hash or code ever fits in here.
type PasswordResetView struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	AccountType string     `json:"account_type"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	RequestIP   string     `json:"request_ip"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	ConsumedAt  *time.Time `json:"consumed_at,omitempty"`
	EmailStatus string     `json:"email_status,omitempty"` // sent|failed|"" when no matching log
	EmailError  string     `json:"email_error,omitempty"`  // raw SMTP error when EmailStatus==failed
}

// resetRowStatus derives the stage the request reached. Ordering matters:
// consumed/verified are set at different times and superseded rows look very
// different from finished ones.
//
//   - used:      new password actually set (verified, then consumed)
//   - superseded: replaced by a newer request before being used
//   - exhausted: 5 wrong codes burned the row; a new request is needed
//   - verified:  code entered correctly, single-use reset token still live
//   - expired:   code window lapsed, never verified
//   - pending:   code emailed, waiting to be entered
func resetRowStatus(p *domain.PasswordReset, now time.Time) string {
	switch {
	case p.ConsumedAt != nil && p.VerifiedAt != nil:
		return "used"
	case p.ConsumedAt != nil:
		return "superseded"
	case p.Attempts >= domain.MaxResetAttempts:
		return "exhausted"
	case p.VerifiedAt != nil:
		return "verified"
	case !now.Before(p.ExpiresAt):
		return "expired"
	default:
		return "pending"
	}
}

// List returns the reset-request history, newest first — the admin panel's
// answer to "did they request a code, and what happened to it?".
// GET /admin/password-resets (AdminJWT).
//
// Filters: email (ILIKE substring), account_type (user|admin),
// status (pending|verified|used|superseded|expired|exhausted).
// Pagination: page, pageSize (≤100).
func (h *PasswordResetAdminHandler) List(c *gin.Context) {
	now := time.Now()

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	// Rebuilt per query because GORM Count/Find must each start from a fresh
	// statement — reusing one chain after Count is a classic bug source.
	build := func() *gorm.DB {
		q := h.db.Model(&domain.PasswordReset{})
		if email := strings.ToLower(strings.TrimSpace(c.Query("email"))); email != "" {
			// Plain LIKE, not ILIKE: reset rows always store lowercased
			// emails, and LIKE keeps the query portable (tests run SQLite).
			q = q.Where("email LIKE ?", "%"+email+"%")
		}
		if accountType := c.Query("account_type"); accountType != "" {
			q = q.Where("account_type = ?", accountType)
		}
		switch status := c.Query("status"); status {
		case "used":
			q = q.Where("consumed_at IS NOT NULL AND verified_at IS NOT NULL")
		case "superseded":
			q = q.Where("consumed_at IS NOT NULL AND verified_at IS NULL")
		case "exhausted":
			q = q.Where("consumed_at IS NULL AND attempts >= ?", domain.MaxResetAttempts)
		case "verified":
			q = q.Where("consumed_at IS NULL AND verified_at IS NOT NULL")
		case "expired":
			q = q.Where("consumed_at IS NULL AND verified_at IS NULL AND attempts < ? AND expires_at <= ?", domain.MaxResetAttempts, now)
		case "pending":
			q = q.Where("consumed_at IS NULL AND verified_at IS NULL AND attempts < ? AND expires_at > ?", domain.MaxResetAttempts, now)
		case "", "all":
			// no filter
		default:
			// Unknown statuses return everything rather than erroring: the
			// panel is the only caller and resetRowStatus stays authoritative.
		}
		return q
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count password resets"})
		return
	}

	var rows []domain.PasswordReset
	if err := build().Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load password resets"})
		return
	}

	views := make([]PasswordResetView, len(rows))
	emails := make(map[string]bool, len(rows))
	for i, p := range rows {
		views[i] = PasswordResetView{
			ID:          p.ID,
			Email:       p.Email,
			AccountType: p.AccountType,
			Status:      resetRowStatus(&p, now),
			Attempts:    p.Attempts,
			RequestIP:   p.RequestIP,
			CreatedAt:   p.CreatedAt,
			ExpiresAt:   p.ExpiresAt,
			VerifiedAt:  p.VerifiedAt,
			ConsumedAt:  p.ConsumedAt,
		}
		emails[p.Email] = true
	}

	// Correlate the code-email outcome. Reset rows and email_logs have no
	// foreign key (the flow never had one), so match by recipient + a
	// timestamp window instead of inventing one now.
	filtered := filterResetviewsByEmailLog(h.db, emails, views)
	c.JSON(http.StatusOK, gin.H{
		"page":     page,
		"pageSize": pageSize,
		"total":    total,
		"resets":   filtered,
	})
}

func filterResetviewsByEmailLog(db *gorm.DB, emails map[string]bool, views []PasswordResetView) []PasswordResetView {
	if len(emails) == 0 {
		return views
	}
	addresses := make([]string, 0, len(emails))
	for e := range emails {
		addresses = append(addresses, e)
	}

	var logs []domain.EmailLog
	if err := db.Where("kind = ? AND to IN ?", domain.EmailKindPasswordReset, addresses).
		Order("created_at DESC").
		Limit(1000).
		Find(&logs).Error; err != nil {
		// The history view is still useful without delivery info; degrade
		// quietly rather than losing the page.
		logger.Errorf("Password reset admin: failed to load email logs: %v", err)
		return views
	}

	// Index the logs by credential email; they arrive newest-first.
	byEmail := make(map[string][]domain.EmailLog, len(logs))
	for _, log := range logs {
		key := strings.ToLower(log.To)
		byEmail[key] = append(byEmail[key], log)
	}

	for i := range views {
		logsFor := byEmail[strings.ToLower(views[i].Email)]
		found := false
		delta := time.Duration(0)
		for _, log := range logsFor {
			d := log.CreatedAt.Sub(views[i].CreatedAt)
			if d < 0 {
				d = -d
			}
			if d > emailLogTolerance {
				continue
			}
			if !found || d < delta {
				info := log
				views[i].EmailStatus = string(info.Status)
				if info.Status == domain.EmailStatusFailed {
					views[i].EmailError = info.Failure
				}
				found = true
				delta = d
			}
			// Close-enough logs are rare and the window is small; scanning
			// all of them is fine at page-size volumes.
		}
	}
	return views
}

// IssueResetCodeRequest is the body of the admin fresh-code endpoint. The
// admin types in exactly what the user reports — their login email — and the
// server does the rest.
type IssueResetCodeRequest struct {
	Email       string `json:"email" binding:"required,email"`
	AccountType string `json:"account_type"` // "user" (default) | "admin"
}

// IssueCode mints a fresh reset code and returns it in the response — the
// repair path when the email never reached the user (spam folder, dead
// inbox, ...). POST /admin/password-resets/issue (AdminJWT).
//
//	{email, account_type} ->
//	200 {code, email, account_type, expires_at}
//
// The code is shown once, here and nowhere else; the stored row carries only
// its bcrypt hash, exactly like a self-service request. The row's RequestIP
// is the admin's IP, so history still shows who issued it.
func (h *PasswordResetAdminHandler) IssueCode(c *gin.Context) {
	var req IssueResetCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	accountType, err := normalizeAccountType(req.AccountType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	exists, err := accountExists(h.db, email, accountType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "No active account found for that email"})
		return
	}

	code, row, err := issueResetCode(h.db, email, accountType, c.ClientIP())
	if err != nil {
		logger.Errorf("Password reset admin: failed to issue code for %s: %v", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue reset code"})
		return
	}

	issuing, _ := c.Get("user_email")
	logger.Infof("Password reset: admin %v issued a fresh code for %s (%s) from IP %s",
		issuing, email, accountType, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"code":         code,
		"email":        email,
		"account_type": accountType,
		"expires_at":   row.ExpiresAt,
		"minutes":      int(domain.ResetCodeTTL.Minutes()),
	})
}
