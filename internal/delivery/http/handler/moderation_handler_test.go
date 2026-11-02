package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestModerationStudentsOnlyReturnsAssignedBatch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Student{}, &domain.Moderator{}); err != nil {
		t.Fatal(err)
	}
	universityID, departmentID, firstBatchID, secondBatchID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	moderatorID, strangerID := uuid.New(), uuid.New()
	first := domain.Student{
		StudentID: "1001", BatchID: firstBatchID, DepartmentID: departmentID,
		UniversityID: universityID, SessionID: uuid.New(), Name: "Assigned", VerificationCode: "111111",
	}
	second := domain.Student{
		StudentID: "2001", BatchID: secondBatchID, DepartmentID: departmentID,
		UniversityID: universityID, SessionID: uuid.New(), Name: "Outside", VerificationCode: "222222",
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	assignment := domain.Moderator{
		UserID: moderatorID, ScopeType: domain.ModeratorScopeBatch, ScopeID: firstBatchID,
		UniversityID: universityID, DepartmentID: &departmentID, IsActive: true,
	}
	if err := db.Create(&assignment).Error; err != nil {
		t.Fatal(err)
	}
	h := NewModerationHandler(db, nil, nil, nil, nil)

	call := func(userID uuid.UUID) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet,
			"/moderation/students?scope_type=batch&scope_id="+firstBatchID.String(), nil)
		c.Set("user_id", userID)
		c.Set("user_role", "student")
		h.Students(c)
		return recorder
	}

	allowed := call(moderatorID)
	if allowed.Code != http.StatusOK {
		t.Fatalf("moderator status = %d, want 200: %s", allowed.Code, allowed.Body.String())
	}
	var response struct {
		Data  []domain.Student `json:"data"`
		Count int64            `json:"count"`
	}
	if err := json.Unmarshal(allowed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 1 || len(response.Data) != 1 {
		t.Fatalf("moderator got %d rows (count %d), want only one assigned row", len(response.Data), response.Count)
	}
	if response.Data[0].ID != first.ID || response.Data[0].VerificationCode != "111111" {
		t.Fatalf("moderator response did not contain assigned student and code: %#v", response.Data[0])
	}

	denied := call(strangerID)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unassigned student status = %d, want 403: %s", denied.Code, denied.Body.String())
	}
}
