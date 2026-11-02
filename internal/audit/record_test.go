package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestRecordRedactsSensitiveValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&domain.AuditLog{}); err != nil {
		t.Fatal(err)
	}

	event := Event{
		ActorType: "user", ActorID: uuid.New(), ViaRole: "batch_moderator",
		Action: "student.update", EntityType: "student", EntityID: uuid.New(),
		Changes: map[string]any{
			"verification_code": "123456",
			"password_hash":     "bcrypt-secret-value",
			"access_token":      "jwt-secret-value",
			"document_url":      "https://files.invalid/document",
			"avatar_url":        "https://files.invalid/avatar",
			"code":              "regenerated",
		},
	}
	if err := Record(db.WithContext(context.Background()), event); err != nil {
		t.Fatal(err)
	}
	var row domain.AuditLog
	if err := db.First(&row, "entity_id = ?", event.EntityID).Error; err != nil {
		t.Fatal(err)
	}
	var changes map[string]any
	if err := json.Unmarshal(row.Changes, &changes); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"verification_code", "password_hash", "access_token", "document_url", "avatar_url"} {
		if changes[key] != "[redacted]" {
			t.Errorf("%s was not redacted: %#v", key, changes[key])
		}
	}
	if changes["code"] != "regenerated" {
		t.Errorf("code action was not retained: %#v", changes["code"])
	}
	for _, secret := range []string{"123456", "bcrypt-secret-value", "jwt-secret-value", "https://"} {
		if strings.Contains(string(row.Changes), secret) {
			t.Errorf("audit record contains sensitive string %q", secret)
		}
	}
}

func TestDiffOnlyIncludesChangedFields(t *testing.T) {
	before := map[string]any{"name": "Old", "status": "pending", "phone": "111"}
	after := map[string]any{"name": "New", "status": "pending", "phone": "111"}
	diff := Diff(before, after)
	if len(diff) != 1 {
		t.Fatalf("Diff() has %d fields, want one", len(diff))
	}
	if diff["name"] != [2]any{"Old", "New"} {
		t.Fatalf("Diff() name = %#v", diff["name"])
	}
}
