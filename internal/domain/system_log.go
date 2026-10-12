package domain

import "github.com/google/uuid"

// SystemLog is a persisted record of a failed (4xx/5xx) HTTP request, so the
// admin dashboard can show what's actually going wrong in production instead
// of only having stdout access logs (see middleware.AccessLogger, which
// prints every request but keeps nothing queryable).
type SystemLog struct {
	Base
	Method     string     `gorm:"size:10" json:"method"`
	Path       string     `gorm:"size:255;index" json:"path"` // no query string — see AccessLogger's own comment on why
	StatusCode int        `gorm:"index" json:"status_code"`
	Message    string     `gorm:"type:text" json:"message"` // first error in gin.Context.Errors, if any
	UserID     *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty"`
	IPAddress  string     `gorm:"size:45" json:"ip_address"`
	LatencyMs  int64      `json:"latency_ms"`
}
