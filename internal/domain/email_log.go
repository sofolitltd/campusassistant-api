package domain

import "github.com/google/uuid"

// EmailKind identifies which mail flow produced an email_logs row.
type EmailKind string

const (
	EmailKindAdminBroadcast EmailKind = "admin_broadcast"
	EmailKindProfileClaim   EmailKind = "profile_claim"
	// EmailKindPasswordReset covers the self-service forgot-password code
	// mail (password_reset_handler.go). Logging it here is how the admin
	// dashboard answers "did the code email actually go out?" for a user who
	// swears it never arrived.
	EmailKindPasswordReset EmailKind = "password_reset"
)

// EmailStatus is the per-recipient outcome of a send attempt.
type EmailStatus string

const (
	EmailStatusSent   EmailStatus = "sent"
	EmailStatusFailed EmailStatus = "failed"
)

// EmailLog persists one row per transactional email send attempt, so the
// admin dashboard can answer "did we actually mail them?" without access to
// the server's stdout (same motivation as SystemLog). Failed rows keep the
// raw SMTP error — that's the only place the Gmail rejection reason lands
// anywhere queryable.
type EmailLog struct {
	Base
	To        string      `gorm:"type:text;index" json:"to"`
	Subject   string      `gorm:"type:text" json:"subject"`
	Kind      EmailKind   `gorm:"size:32;index" json:"kind"`
	Status    EmailStatus `gorm:"size:16;index" json:"status"`
	Failure   string      `gorm:"type:text" json:"failure,omitempty"` // raw SMTP error when Status==failed
	StudentID *uuid.UUID  `gorm:"type:uuid;index" json:"student_id,omitempty"`
}
