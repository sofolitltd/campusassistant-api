package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestModeratorAssignmentDerivesStudentScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&domain.Moderator{}, &domain.User{}, &domain.Student{}, &domain.Batch{},
		&domain.AuditLog{}, &domain.SystemLog{},
	); err != nil {
		t.Fatal(err)
	}
	userID, adminID := uuid.New(), uuid.New()
	universityID, departmentID, batchID := uuid.New(), uuid.New(), uuid.New()
	user := domain.User{Email: "student@example.test", Role: domain.RoleStudent, IsActive: true}
	user.ID = userID
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	batch := domain.Batch{Base: domain.Base{ID: batchID}, Name: "CSE-21", DepartmentID: departmentID, UniversityID: universityID}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	student := domain.Student{
		UserID: &userID, IsClaimed: true, BatchID: batchID, DepartmentID: departmentID,
		UniversityID: universityID, SessionID: uuid.New(), StudentID: "123",
	}
	if err := db.Create(&student).Error; err != nil {
		t.Fatal(err)
	}

	h := NewModeratorAdminHandler(db, nil)
	gin.SetMode(gin.TestMode)
	body := []byte(`{"user_id":"` + userID.String() + `","scope_type":"university","scope_id":"` + uuid.NewString() + `"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/moderators", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", adminID)
	c.Set("user_role", "super_admin")
	h.Create(c)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	var created domain.Moderator
	if err := db.First(&created, "user_id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	if created.ScopeID != universityID || created.UniversityID != universityID || created.DepartmentID != nil {
		t.Fatalf("server did not derive the student's university scope: %#v", created)
	}
	if created.GrantedBy == nil || *created.GrantedBy != adminID {
		t.Fatalf("granting admin was not recorded: %#v", created.GrantedBy)
	}

	duplicate := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(duplicate)
	c.Request = httptest.NewRequest(http.MethodPost, "/moderators", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", adminID)
	c.Set("user_role", "super_admin")
	h.Create(c)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409: %s", duplicate.Code, duplicate.Body.String())
	}
}
