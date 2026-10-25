package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"campusassistant-api/internal/domain"
)

// TestRouter_PasswordResetAdminRoutesAreAdminOnly mirrors the admin-mail
// guard test: both /admin/password-resets endpoints must refuse the public
// (no token), ordinary users, and admit admins.
func TestRouter_PasswordResetAdminRoutesAreAdminOnly(t *testing.T) {
	e := newRouterEnv(t)
	student := e.token(t, e.student.ID, "student")
	admin := e.token(t, e.adminID, "admin")

	routes := []struct{ method, path string }{
		{"GET", "/api/v1/admin/password-resets"},
		{"POST", "/api/v1/admin/password-resets/issue"},
	}
	for _, rt := range routes {
		if got := e.call(rt.method, rt.path, "").Code; got != 401 {
			t.Errorf("%s %s without a token = %d, want 401", rt.method, rt.path, got)
		}
		if got := e.call(rt.method, rt.path, student).Code; got != 403 {
			t.Errorf("%s %s as a student = %d, want 403", rt.method, rt.path, got)
		}
		if got := e.call(rt.method, rt.path, admin).Code; got == 401 || got == 403 {
			t.Errorf("%s %s as admin = %d, must be allowed through", rt.method, rt.path, got)
		}
	}
}

func TestRouter_AdminIssueCodeFlow(t *testing.T) {
	e := newRouterEnv(t)
	db := e.db(t)
	if err := db.AutoMigrate(&domain.PasswordReset{}, &domain.EmailLog{}); err != nil {
		t.Fatal(err)
	}

	target := domain.User{Email: "fg-user@test.dev", TokenVersion: 1, IsActive: true}
	db.Create(&target)
	admin := e.token(t, e.adminID, "admin")

	// Unknown email is refused with a plain 404 — allowed to be honest
	// because the caller is an authenticated admin.
	if got := e.callBody("POST", "/api/v1/admin/password-resets/issue", admin,
		`{"email":"nobody@test.dev"}`).Code; got != http.StatusNotFound {
		t.Fatalf("issue for unknown email = %d, want 404", got)
	}

	w := e.callBody("POST", "/api/v1/admin/password-resets/issue", admin, `{"email":"fg-user@test.dev"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("issue = %d (%s), want 200", w.Code, w.Body.String())
	}
	var issued struct {
		Code        string    `json:"code"`
		Email       string    `json:"email"`
		AccountType string    `json:"account_type"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatalf("issue response: %v", err)
	}
	if len(issued.Code) != 6 || issued.Email != "fg-user@test.dev" || issued.AccountType != "user" {
		t.Fatalf("issued = %+v, want a 6-digit code for the user account", issued)
	}

	// The row exists and — critically — the plaintext code never does: only
	// the hash is persisted.
	var first domain.PasswordReset
	db.Where("email = ?", "fg-user@test.dev").First(&first)
	if first.ConsumedAt != nil || first.VerifiedAt != nil || first.CodeHash == "" {
		t.Fatalf("first row = consumed %v verified %v hash empty %v", first.ConsumedAt != nil, first.VerifiedAt != nil, first.CodeHash == "")
	}

	// A second issue supersedes the first: old row consumed, new one pending.
	time.Sleep(20 * time.Millisecond) // keep created_at ordering monotonic in SQLite
	w = e.callBody("POST", "/api/v1/admin/password-resets/issue", admin, `{"email":"fg-user@test.dev"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("second issue = %d (%s), want 200", w.Code, w.Body.String())
	}
	db.First(&first, "id = ?", first.ID) // reload superseded row
	var all []domain.PasswordReset
	db.Where("email = ? AND consumed_at IS NULL", "fg-user@test.dev").Find(&all)
	if !(len(all) == 1 && first.ConsumedAt != nil && first.VerifiedAt == nil) {
		t.Fatalf("after second issue: outstanding rows = %d, first consumed %v verified %v", len(all), first.ConsumedAt != nil, first.VerifiedAt != nil)
	}

	// The history endpoint lists both rows with derived statuses, and never
	// serializes a hash or code.
	hist := e.call("GET", "/api/v1/admin/password-resets?email=fg-user", admin)
	if hist.Code != http.StatusOK {
		t.Fatalf("history = %d (%s), want 200", hist.Code, hist.Body.String())
	}
	if body := hist.Body.String(); strings.Contains(body, "code_hash") ||
		strings.Contains(body, "token_hash") ||
		strings.Contains(body, issued.Code) {
		t.Fatalf("history response leaked sensitive fields: %s", body)
	}
	var page struct {
		Total  int `json:"total"`
		Resets []struct {
			Email  string `json:"email"`
			Status string `json:"status"`
		} `json:"resets"`
	}
	if err := json.Unmarshal(hist.Body.Bytes(), &page); err != nil {
		t.Fatalf("history response: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("history total = %d, want 2", page.Total)
	}
	statuses := map[string]bool{}
	for _, r := range page.Resets {
		statuses[r.Status] = true
	}
	if !statuses["pending"] || !statuses["superseded"] {
		t.Fatalf("statuses = %v, want pending + superseded", statuses)
	}
}
