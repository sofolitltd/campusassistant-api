package mailer

import "campusassistant-api/pkg/logger"

// NoopMailer is used when SMTP credentials are absent. It logs what would have
// been sent instead of delivering it.
//
// This keeps local development workable without a live Gmail App Password —
// the reset code appears in the server log, so the flow can still be walked
// end to end. It is emphatically not for production: if you see these lines on
// the VPS, the SMTP_* env vars are missing and no user is receiving anything.
type NoopMailer struct{}

func NewNoopMailer() *NoopMailer {
	logger.Infof("Mailer: SMTP not configured — emails will be logged, not sent")
	return &NoopMailer{}
}

func (m *NoopMailer) Send(to, subject, htmlBody, textBody string) error {
	logger.Infof("Mailer (noop) — would send to %s | subject: %s\n%s", to, subject, textBody)
	return nil
}
