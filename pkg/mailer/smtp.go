package mailer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"campusassistant-api/internal/config"
)

// SMTPMailer sends through a plain SMTP server.
//
// Uses net/smtp from the standard library — no third-party dependency. Gmail
// on port 587 advertises STARTTLS, which smtp.SendMail negotiates
// automatically; PlainAuth then refuses to transmit the credentials over an
// unencrypted link, so the password is never sent in the clear.
type SMTPMailer struct {
	host      string
	port      string
	username  string
	password  string
	fromEmail string
	fromName  string
}

func NewSMTPMailer(cfg *config.Config) *SMTPMailer {
	port := strings.TrimSpace(cfg.SMTPPort)
	if port == "" {
		port = "587"
	}

	// Fall back to the SMTP account itself if no explicit From is configured.
	// Gmail rewrites a mismatched From to the authenticated account anyway.
	from := strings.TrimSpace(cfg.SMTPFromEmail)
	if from == "" {
		from = cfg.SMTPUsername
	}

	name := strings.TrimSpace(cfg.SMTPFromName)
	if name == "" {
		name = "Campus Assistant"
	}

	return &SMTPMailer{
		host:      strings.TrimSpace(cfg.SMTPHost),
		port:      port,
		username:  strings.TrimSpace(cfg.SMTPUsername),
		password:  cfg.SMTPPassword,
		fromEmail: from,
		fromName:  name,
	}
}

func (m *SMTPMailer) Send(to, subject, htmlBody, textBody string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}

	msg, err := m.build(to, subject, htmlBody, textBody)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(m.host, m.port)
	auth := smtp.PlainAuth("", m.username, m.password, m.host)

	if err := smtp.SendMail(addr, auth, m.fromEmail, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send to %s failed: %w", to, err)
	}
	return nil
}

// build assembles a multipart/alternative message. Sending both a plain-text
// and an HTML part materially improves the odds of landing in the inbox rather
// than the spam folder.
func (m *SMTPMailer) build(to, subject, htmlBody, textBody string) ([]byte, error) {
	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}

	from := (&mail.Address{Name: m.fromName, Address: m.fromEmail}).String()

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	// Q-encode so non-ASCII subjects survive transit.
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	// Reset codes are transactional, not marketing — ask well-behaved clients
	// not to auto-reply and not to index them.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + "\"\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normalizeNewlines(textBody) + "\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normalizeNewlines(htmlBody) + "\r\n\r\n")

	b.WriteString("--" + boundary + "--\r\n")

	return []byte(b.String()), nil
}

func randomBoundary() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate MIME boundary: %w", err)
	}
	return "ca-" + hex.EncodeToString(buf), nil
}

// normalizeNewlines converts bare LF to CRLF as SMTP requires, without
// doubling up on input that is already CRLF.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}
