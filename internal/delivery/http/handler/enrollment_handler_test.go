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

func TestPromoteAndRevert(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&domain.Session{}, &domain.Batch{}, &domain.Student{}, &domain.StudentEnrollment{},
		&domain.User{}, &domain.Moderator{}, &domain.AuditLog{}, &domain.SystemLog{},
	); err != nil {
		t.Fatal(err)
	}
	universityID, departmentID, adminID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	user := domain.User{Email: "s@example.test", Role: domain.RoleStudent, IsActive: true}
	user.ID = userID
	db.Create(&user)

	honoursSession := domain.Session{Name: "2017-2018"}
	mastersSession := domain.Session{Name: "2021-2022"}
	for _, s := range []*domain.Session{&honoursSession, &mastersSession} {
		s.ID = uuid.New()
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	honours := domain.Batch{Name: "Batch 10", Slug: "batch-10", DepartmentID: departmentID, UniversityID: universityID}
	honours.ID = uuid.New()
	masters := domain.Batch{Name: "Masters Batch 10", Slug: "masters-batch-10", Program: domain.BatchProgramMasters, DepartmentID: departmentID, UniversityID: universityID}
	masters.ID = uuid.New()
	db.Create(&honours)
	db.Create(&masters)
	if honours.Program != domain.BatchProgramHonours {
		t.Fatalf("default program = %q, want honours", honours.Program)
	}
	db.Exec("INSERT INTO batch_sessions (batch_id, session_id) VALUES (?, ?), (?, ?)", honours.ID, honoursSession.ID, masters.ID, mastersSession.ID)

	student := domain.Student{
		UserID: &userID, BatchID: honours.ID, SessionID: honoursSession.ID, DepartmentID: departmentID,
		UniversityID: universityID, StudentID: "17608001", IsCR: true, Name: "Rahim",
	}
	student.ID = uuid.New()
	db.Create(&student)
	mod := domain.Moderator{UserID: userID, ScopeType: domain.ModeratorScopeBatch, ScopeID: honours.ID, UniversityID: universityID, IsActive: true}
	mod.ID = uuid.New()
	db.Create(&mod)

	gin.SetMode(gin.TestMode)
	h := NewEnrollmentHandler(db)
	call := func(fn gin.HandlerFunc, params gin.Params, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Params = params
		c.Set("user_id", adminID)
		c.Set("user_role", "super_admin")
		fn(c)
		return rec
	}

	// Wrong session for the target batch is rejected.
	rec := call(h.Promote, gin.Params{{Key: "id", Value: masters.ID.String()}},
		`{"session_id":"`+honoursSession.ID.String()+`","students":[{"id":"`+student.ID.String()+`"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong session status = %d, want 400", rec.Code)
	}

	rec = call(h.Promote, gin.Params{{Key: "id", Value: masters.ID.String()}},
		`{"session_id":"`+mastersSession.ID.String()+`","students":[{"id":"`+student.ID.String()+`","roll":"M-55"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status = %d: %s", rec.Code, rec.Body.String())
	}

	var after domain.Student
	db.First(&after, "id = ?", student.ID)
	if after.BatchID != masters.ID || after.SessionID != mastersSession.ID || after.StudentID != "M-55" || after.IsCR {
		t.Fatalf("student after promote = batch %s session %s roll %q cr %v", after.BatchID, after.SessionID, after.StudentID, after.IsCR)
	}
	var rows []domain.StudentEnrollment
	db.Find(&rows, "student_id = ?", student.ID)
	if len(rows) != 1 || rows[0].BatchID != honours.ID || rows[0].Roll != "17608001" || !rows[0].IsCR || rows[0].Program != domain.BatchProgramHonours {
		t.Fatalf("enrollments = %+v", rows)
	}
	var gotMod domain.Moderator
	db.First(&gotMod, "id = ?", mod.ID)
	if gotMod.IsActive {
		t.Fatal("old batch moderator role should be revoked")
	}

	// Promoting again into the same batch fails.
	rec = call(h.Promote, gin.Params{{Key: "id", Value: masters.ID.String()}},
		`{"session_id":"`+mastersSession.ID.String()+`","students":[{"id":"`+student.ID.String()+`"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("double promote status = %d, want 400", rec.Code)
	}

	rec = call(h.Revert, gin.Params{{Key: "id", Value: student.ID.String()}, {Key: "enrollmentId", Value: rows[0].ID.String()}}, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("revert status = %d: %s", rec.Code, rec.Body.String())
	}
	db.First(&after, "id = ?", student.ID)
	if after.BatchID != honours.ID || after.StudentID != "17608001" || !after.IsCR {
		t.Fatalf("student after revert = %+v", after)
	}
	var count int64
	db.Model(&domain.StudentEnrollment{}).Unscoped().Where("student_id = ?", student.ID).Count(&count)
	if count != 0 {
		t.Fatalf("enrollments after revert = %d, want 0", count)
	}
}
