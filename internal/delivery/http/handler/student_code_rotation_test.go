package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestRotateUnclaimedCodesRotatesOnlyUnclaimedUnlinkedProfiles(t *testing.T) {
	db := rotationTestDB(t)
	userID := uuid.New()
	students := []domain.Student{
		{StudentID: "1001", Name: "Unclaimed 1", VerificationCode: "111111"},
		{StudentID: "1002", Name: "Unclaimed 2", VerificationCode: "222222"},
		{StudentID: "1003", Name: "Claimed", VerificationCode: "333333", IsClaimed: true},
		{StudentID: "1004", Name: "Linked but unclaimed", VerificationCode: "444444", UserID: &userID},
	}
	for i := range students {
		students[i].UniversityID = uuid.New()
		students[i].DepartmentID = uuid.New()
		students[i].BatchID = uuid.New()
		students[i].SessionID = uuid.New()
		if err := db.Create(&students[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/students/rotate-unclaimed-codes", strings.NewReader(`{"confirm":true}`))
	c.Set("user_role", "admin")
	c.Set("user_id", uuid.New())
	handler := &StudentHandler{db: db}
	handler.RotateUnclaimedCodes(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["rotated_count"] != float64(2) {
		t.Fatalf("rotated_count = %#v, want 2", response["rotated_count"])
	}
	if _, includesCode := response["verification_code"]; includesCode {
		t.Fatalf("rotation response must not expose codes: %#v", response)
	}

	var stored []domain.Student
	if err := db.Order("student_id ASC").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, student := range stored {
		switch student.StudentID {
		case "1001", "1002":
			if student.VerificationCode == "111111" || student.VerificationCode == "222222" {
				t.Fatalf("old code was reused: %q", student.VerificationCode)
			}
			if len(student.VerificationCode) != 6 || used[student.VerificationCode] {
				t.Fatalf("replacement code invalid or duplicated: %q", student.VerificationCode)
			}
			used[student.VerificationCode] = true
		case "1003":
			if student.VerificationCode != "333333" {
				t.Fatalf("claimed student's code changed to %q", student.VerificationCode)
			}
		case "1004":
			if student.VerificationCode != "444444" {
				t.Fatalf("linked unclaimed student's code changed to %q", student.VerificationCode)
			}
		}
	}

	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).Where("action = ?", "code.bulk_regenerate").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("bulk rotation audit entries = %d, want 1", auditCount)
	}
}

func TestRotateUnclaimedCodesRequiresConfirmation(t *testing.T) {
	db := rotationTestDB(t)
	student := domain.Student{StudentID: "1001", VerificationCode: "111111"}
	if err := db.Create(&student).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/students/rotate-unclaimed-codes", strings.NewReader(`{"confirm":false}`))
	handler := &StudentHandler{db: db}
	handler.RotateUnclaimedCodes(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var stored domain.Student
	if err := db.First(&stored, "id = ?", student.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.VerificationCode != "111111" {
		t.Fatalf("code changed without confirmation: %q", stored.VerificationCode)
	}
}

func rotationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Student{}, &domain.AuditLog{}, &domain.SystemLog{}); err != nil {
		t.Fatal(err)
	}
	return db
}
