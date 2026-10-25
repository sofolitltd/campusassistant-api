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

// ProfileClaimSubject is the subject line for the profile-claim code mail an
// admin sends to a student.
const ProfileClaimSubject = "Your Campus Assistant profile claim code"

// ProfileClaimBodies renders the HTML and plain-text bodies for the letters
// that carry a student's one-time profile claim code. Claim codes do not
// expire (they die when the profile is claimed), so unlike the reset mail
// there is no validity window — the code is simply the action.
func ProfileClaimBodies(name, code string) (htmlBody, textBody string) {
	safeName := html.EscapeString(name)
	if safeName == "" {
		safeName = "Student"
	}
	safeCode := html.EscapeString(code)

	htmlBody = fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#111">
  <h2 style="margin:0 0 8px;font-size:20px">Claim your Campus Assistant profile</h2>
  <p style="margin:0 0 24px;color:#555;font-size:14px;line-height:1.5">
    Hi %s, your Campus Assistant profile is ready. Use this code to claim it
    and link it to your account.
  </p>
  <div style="font-size:32px;font-weight:700;letter-spacing:8px;text-align:center;padding:20px;background:#f4f4f5;border-radius:8px;margin-bottom:24px">%s</div>
  <p style="margin:0 0 8px;color:#555;font-size:14px;line-height:1.5">
    Log in to your account, open the claim-profile screen and enter this code.
    Each code works once and only for your profile.
  </p>
  <p style="margin:0;color:#888;font-size:13px;line-height:1.5">
    If someone else sent you this code unexpectedly, don't share it — your
    profile is not linked to anything until you claim it yourself.
  </p>
</div>`, safeName, safeCode)

	textBody = strings.Join([]string{
		"Claim your Campus Assistant profile",
		"",
		fmt.Sprintf("Hi %s, your Campus Assistant profile is ready.", name),
		"Use this code to claim it and link it to your account:",
		"",
		"    " + code,
		"",
		"Log in to your account, open the claim-profile screen and enter this",
		"code. Each code works once and only for your profile.",
		"",
		"If someone else sent you this code unexpectedly, don't share it —",
		"your profile is not linked to anything until you claim it yourself.",
	}, "\n")

	return htmlBody, textBody
}

// BroadcastBodies renders the HTML and plain-text bodies for an admin
// broadcast to students. The message is free-form text: blank lines split
// paragraphs. Keep the styling as plain as the other mails — mail clients
// strip everything clever.
func BroadcastBodies(title, message, senderName string) (htmlBody, textBody string) {
	safeTitle := html.EscapeString(title)
	safeSender := html.EscapeString(senderName)

	paras := strings.Split(strings.TrimSpace(message), "\n\n")
	htmlParas := make([]string, 0, len(paras))
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = html.EscapeString(p)
		p = strings.ReplaceAll(p, "\n", "<br>")
		htmlParas = append(htmlParas, fmt.Sprintf(
			`  <p style="margin:0 0 16px;color:#333;font-size:14px;line-height:1.6">%s</p>`, p))
	}
	htmlBody = fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;max-width:480px;margin:0 auto;padding:24px;color:#111">
  <h2 style="margin:0 0 16px;font-size:20px">%s</h2>
%s
  <p style="margin:16px 0 0;color:#888;font-size:13px;line-height:1.5">
    — %s, Campus Assistant admin
  </p>
</div>`, safeTitle, strings.Join(htmlParas, "\n"), safeSender)

	textParts := []string{title, ""}
	for _, p := range paras {
		if p = strings.TrimSpace(p); p != "" {
			textParts = append(textParts, p, "")
		}
	}
	textParts = append(textParts, fmt.Sprintf("— %s, Campus Assistant admin", senderName))
	textBody = strings.Join(textParts, "\n")

	return htmlBody, textBody
}
