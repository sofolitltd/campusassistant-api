package mailer

import (
	"os"
	"testing"

	"campusassistant-api/internal/config"
)

// TestSMTPSmoke sends one real email using the SMTP_* env vars.
// Skipped unless SMTP_SMOKE_TO is set, so it never runs by accident.
func TestSMTPSmoke(t *testing.T) {
	to := os.Getenv("SMTP_SMOKE_TO")
	if to == "" {
		t.Skip("SMTP_SMOKE_TO not set")
	}

	cfg := &config.Config{
		SMTPHost:      os.Getenv("SMTP_HOST"),
		SMTPPort:      os.Getenv("SMTP_PORT"),
		SMTPUsername:  os.Getenv("SMTP_USERNAME"),
		SMTPPassword:  os.Getenv("SMTP_PASSWORD"),
		SMTPFromEmail: os.Getenv("SMTP_FROM_EMAIL"),
		SMTPFromName:  os.Getenv("SMTP_FROM_NAME"),
	}

	htmlBody, textBody := PasswordResetBodies("123456", 10)
	if err := NewSMTPMailer(cfg).Send(to, PasswordResetSubject, htmlBody, textBody); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	t.Logf("sent to %s", to)
}
