// Package mailer sends transactional email.
//
// The transport is deliberately behind an interface: the current
// implementation is plain SMTP (Gmail with an App Password), but swapping to
// another provider later should be a config change plus one new file, not a
// rewrite of every call site.
package mailer

import (
	"campusassistant-api/internal/config"
	"strings"
)

// Mailer sends a single message. Implementations must be safe for concurrent
// use — handlers send from goroutines.
type Mailer interface {
	// Send delivers a multipart message. Both bodies should be supplied:
	// HTML-only mail is far more likely to be filtered as spam.
	Send(to, subject, htmlBody, textBody string) error
}

// New returns an SMTP mailer when SMTP is configured, and a no-op mailer that
// only logs otherwise.
//
// This mirrors how FCM degrades in pkg/fcm — a missing optional credential
// disables the feature rather than preventing the server from booting. It also
// keeps local development usable without handing every developer a live Gmail
// App Password: the code is written to the log instead.
func New(cfg *config.Config) Mailer {
	if strings.TrimSpace(cfg.SMTPHost) == "" ||
		strings.TrimSpace(cfg.SMTPUsername) == "" ||
		strings.TrimSpace(cfg.SMTPPassword) == "" {
		return NewNoopMailer()
	}
	return NewSMTPMailer(cfg)
}
