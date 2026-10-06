package domain

import "time"

// Account types a password reset can target. Users and admins live in
// separate tables with separate logins, so the reset row has to remember
// which one the code was issued for.
const (
	ResetAccountUser  = "user"
	ResetAccountAdmin = "admin"
)

// PasswordReset is a single in-flight "forgot password" attempt.
//
// Every field is json:"-" on purpose — this row must never be serialized out
// of an endpoint. Leaking CodeHash or TokenHash would defeat the whole flow.
//
// The 6-digit code is bcrypt-hashed rather than SHA-256'd: six digits is only
// ~20 bits of entropy, so a fast hash would be brute-forced instantly if the
// database ever leaked. TokenHash is SHA-256 because the reset token is 32
// random bytes, where a fast hash is not a weakness.
type PasswordReset struct {
	Base
	Email       string     `gorm:"index;not null" json:"-"`
	AccountType string     `gorm:"size:10;not null" json:"-"` // ResetAccountUser | ResetAccountAdmin
	CodeHash    string     `gorm:"size:255;not null" json:"-"`
	TokenHash   string     `gorm:"size:64;index" json:"-"` // set once the code is verified
	RequestIP   string     `gorm:"size:45" json:"-"`       // 45 = max INET6_ADDRSTRLEN
	Attempts    int        `gorm:"default:0" json:"-"`
	ExpiresAt   time.Time  `gorm:"index;not null" json:"-"`
	VerifiedAt  *time.Time `json:"-"`
	ConsumedAt  *time.Time `json:"-"`
}

// Usable reports whether this row can still accept a code attempt.
func (p *PasswordReset) Usable(now time.Time) bool {
	return p.ConsumedAt == nil && p.Attempts < MaxResetAttempts && now.Before(p.ExpiresAt)
}

const (
	// ResetCodeTTL is how long a 6-digit code stays valid.
	ResetCodeTTL = 10 * time.Minute
	// ResetTokenTTL is how long the post-verification token stays valid.
	// Deliberately short — it is only used to submit the new password.
	ResetTokenTTL = 15 * time.Minute
	// MaxResetAttempts is how many wrong codes kill a reset row.
	MaxResetAttempts = 5
	// ResetRateWindow / MaxResetRequests throttle how many codes can be
	// requested per email and per IP within the window.
	ResetRateWindow  = 15 * time.Minute
	MaxResetRequests = 3
)
