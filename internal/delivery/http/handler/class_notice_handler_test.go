package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func classNoticeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.CR{}, &domain.ClassNotice{}, &domain.Batch{}, &domain.Moderator{}, &domain.AuditLog{}, &domain.SystemLog{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func callClassNotice(h *ClassNoticeHandler, fn gin.HandlerFunc, userID uuid.UUID, role string, body any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, "/class-notices", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", userID)
	c.Set("user_role", role)
	fn(c)
	return w
}

func TestClassNoticeCreate_ModeratorScope(t *testing.T) {
	db := classNoticeTestDB(t)
	h := NewClassNoticeHandler(db)

	university, otherUniversity := uuid.New(), uuid.New()
	dept, otherDept := uuid.New(), uuid.New()
	myBatch, otherBatch, otherCampusBatch := uuid.New(), uuid.New(), uuid.New()
	batchModerator, deptModerator, universityModerator, expired, future, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	past, futureStart := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)

	for _, batch := range []domain.Batch{
		{Base: domain.Base{ID: myBatch}, Name: "CSE-21", DepartmentID: dept, UniversityID: university},
		{Base: domain.Base{ID: otherBatch}, Name: "CSE-22", DepartmentID: dept, UniversityID: university},
		{Base: domain.Base{ID: otherCampusBatch}, Name: "Other", DepartmentID: otherDept, UniversityID: otherUniversity},
	} {
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, assignment := range []domain.Moderator{
		{UserID: batchModerator, ScopeType: domain.ModeratorScopeBatch, ScopeID: myBatch, UniversityID: university, DepartmentID: &dept, IsActive: true},
		{UserID: deptModerator, ScopeType: domain.ModeratorScopeDepartment, ScopeID: dept, UniversityID: university, DepartmentID: &dept, IsActive: true},
		{UserID: universityModerator, ScopeType: domain.ModeratorScopeUniversity, ScopeID: university, UniversityID: university, IsActive: true},
		{UserID: expired, ScopeType: domain.ModeratorScopeBatch, ScopeID: myBatch, UniversityID: university, DepartmentID: &dept, IsActive: true, TermEnd: &past},
		{UserID: future, ScopeType: domain.ModeratorScopeBatch, ScopeID: myBatch, UniversityID: university, DepartmentID: &dept, IsActive: true, TermStart: &futureStart},
	} {
		if err := db.Create(&assignment).Error; err != nil {
			t.Fatal(err)
		}
	}

	post := func(user uuid.UUID, role string, university, department uuid.UUID, batch *uuid.UUID) *httptest.ResponseRecorder {
		return callClassNotice(h, h.Create, user, role, map[string]any{
			"university_id": university, "department_id": department, "batch_id": batch, "title": "Class off",
		})
	}

	cases := []struct {
		name string
		w    *httptest.ResponseRecorder
		want int
	}{
		{"batch moderator, own batch", post(batchModerator, "student", university, dept, &myBatch), http.StatusCreated},
		{"batch moderator, other batch", post(batchModerator, "student", university, dept, &otherBatch), http.StatusForbidden},
		{"department moderator, any department batch", post(deptModerator, "student", university, dept, &otherBatch), http.StatusCreated},
		{"department moderator, other department", post(deptModerator, "student", otherUniversity, otherDept, &otherCampusBatch), http.StatusForbidden},
		{"university moderator, department batch", post(universityModerator, "student", university, dept, &otherBatch), http.StatusCreated},
		{"expired moderator", post(expired, "student", university, dept, &myBatch), http.StatusForbidden},
		{"future moderator", post(future, "student", university, dept, &myBatch), http.StatusForbidden},
		{"ordinary student", post(stranger, "student", university, dept, &myBatch), http.StatusForbidden},
		{"admin, any batch", post(stranger, "admin", otherUniversity, otherDept, &otherCampusBatch), http.StatusCreated},
	}
	for _, tc := range cases {
		if tc.w.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, tc.w.Code, tc.want, tc.w.Body.String())
		}
	}

	var n domain.ClassNotice
	if err := db.Where("created_by_id = ?", batchModerator).First(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n.BatchID == nil || *n.BatchID != myBatch {
		t.Errorf("batch moderator notice not pinned to their batch: %v", n.BatchID)
	}
}

func TestClassNoticeCreate_Validation(t *testing.T) {
	db := classNoticeTestDB(t)
	h := NewClassNoticeHandler(db)
	dept := uuid.New()
	for name, body := range map[string]map[string]any{
		"missing title":    {"department_id": dept, "title": "  "},
		"unknown filetype": {"department_id": dept, "title": "x", "file_url": "/a.exe", "file_type": "exe"},
	} {
		w := callClassNotice(h, h.Create, uuid.New(), "admin", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, w.Code)
		}
	}
}

func fileHeader(t *testing.T, name string, content []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("image", name)
	part.Write(content)
	mw.Close()
	r := multipart.NewReader(&buf, mw.Boundary())
	form, err := r.ReadForm(10 << 20)
	if err != nil {
		t.Fatal(err)
	}
	return form.File["image"][0]
}

func TestCheckUploadRule_ClassNotices(t *testing.T) {
	rule := restrictedUploadFolders["class-notices"]
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	pdf := append([]byte("%PDF-1.7\n"), make([]byte, 64)...)

	if err := checkUploadRule(rule, fileHeader(t, "a.png", png)); err != nil {
		t.Errorf("png rejected: %v", err)
	}
	if err := checkUploadRule(rule, fileHeader(t, "a.pdf", pdf)); err != nil {
		t.Errorf("pdf rejected: %v", err)
	}
	// Extension and declared type lie; the bytes decide.
	if err := checkUploadRule(rule, fileHeader(t, "evil.png", []byte("<html><script>alert(1)</script></html>"))); err == nil {
		t.Error("html disguised as png accepted")
	}
	if err := checkUploadRule(rule, fileHeader(t, "big.pdf", append(pdf, make([]byte, 6<<20)...))); err == nil {
		t.Error("6MB file accepted")
	}
}
