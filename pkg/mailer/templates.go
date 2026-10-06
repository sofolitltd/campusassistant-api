package mailer

import (
	"fmt"
	"html"
	"strings"
)

// PasswordResetSubject is the subject line for the OTP mail.
const PasswordResetSubject = "Your Campus Assistant password reset code"

// PasswordResetBodies renders the HTML and plain-text bodies for a reset code.
//
// The markup is intentionally table-free, inline-styled and simple: mail
// clients strip <style> blocks, and anything clever tends to render badly in
// Gmail's mobile app. minutes is the code's validity window, surfaced so the
// copy can never drift from domain.ResetCodeTTL.
func PasswordResetBodies(code string, minutes int) (htmlBody, textBody string) {
	safeCode := html.EscapeString(code)

	htmlBody = fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#111">
  <h2 style="margin:0 0 8px;font-size:20px">Password reset</h2>
  <p style="margin:0 0 24px;color:#555;font-size:14px;line-height:1.5">
    Use this code to reset your Campus Assistant password.
  </p>
  <div style="font-size:32px;font-weight:700;letter-spacing:8px;text-align:center;padding:20px;background:#f4f4f5;border-radius:8px;margin-bottom:24px">%s</div>
  <p style="margin:0 0 8px;color:#555;font-size:14px;line-height:1.5">
    This code expires in %d minutes and can only be used once.
  </p>
  <p style="margin:0;color:#888;font-size:13px;line-height:1.5">
    If you didn't request a password reset, you can safely ignore this email —
    your password will not change.
  </p>
</div>`, safeCode, minutes)

	textBody = strings.Join([]string{
		"Password reset",
		"",
		"Use this code to reset your Campus Assistant password:",
		"",
		"    " + code,
		"",
		fmt.Sprintf("This code expires in %d minutes and can only be used once.", minutes),
		"",
		"If you didn't request a password reset, you can safely ignore this",
		"email — your password will not change.",
	}, "\n")

	return htmlBody, textBody
}
