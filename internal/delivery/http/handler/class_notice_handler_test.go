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
	if err := db.AutoMigrate(&domain.CR{}, &domain.ClassNotice{}); err != nil {
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

func TestClassNoticeCreate_CRScope(t *testing.T) {
	db := classNoticeTestDB(t)
	h := NewClassNoticeHandler(db)

	dept, otherDept := uuid.New(), uuid.New()
	myBatch, otherBatch := uuid.New(), uuid.New()
	cr, former, viaStudent, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	studentRow := uuid.New()
	past := time.Now().Add(-time.Hour)

	mk := func(c domain.CR) {
		c.ID = uuid.New()
		c.Name = "CR"
		c.UniversityID = uuid.New()
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk(domain.CR{UserID: &cr, DepartmentID: dept, BatchID: myBatch, IsCurrent: true})
	mk(domain.CR{UserID: &former, DepartmentID: dept, BatchID: myBatch, IsCurrent: false})
	// Imported-style row: linked only through the student profile.
	mk(domain.CR{TargetStudentID: &studentRow, DepartmentID: dept, BatchID: myBatch, IsCurrent: true})
	student := domain.Student{
		UserID: &viaStudent, UniversityID: uuid.New(), DepartmentID: dept,
		BatchID: myBatch, SessionID: uuid.New(),
	}
	student.ID = studentRow
	if err := db.Create(&student).Error; err != nil {
		t.Fatal(err)
	}
	// Term over.
	expired := uuid.New()
	mk(domain.CR{UserID: &expired, DepartmentID: dept, BatchID: myBatch, IsCurrent: true, TermEnd: &past})

	post := func(user uuid.UUID, role string, dept uuid.UUID, batch *uuid.UUID) *httptest.ResponseRecorder {
		return callClassNotice(h, h.Create, user, role, map[string]any{
			"university_id": uuid.New(), "department_id": dept, "batch_id": batch, "title": "Class off",
		})
	}

	cases := []struct {
		name string
		w    *httptest.ResponseRecorder
		want int
	}{
		{"CR, batch omitted -> pinned to own batch", post(cr, "student", dept, nil), http.StatusCreated},
		{"CR, own batch", post(cr, "student", dept, &myBatch), http.StatusCreated},
		{"CR via student link", post(viaStudent, "student", dept, nil), http.StatusCreated},
		{"CR targeting another batch", post(cr, "student", dept, &otherBatch), http.StatusForbidden},
		{"CR of another department", post(cr, "student", otherDept, nil), http.StatusForbidden},
		{"former CR", post(former, "student", dept, nil), http.StatusForbidden},
		{"CR whose term ended", post(expired, "student", dept, nil), http.StatusForbidden},
		{"ordinary student", post(stranger, "student", dept, nil), http.StatusForbidden},
		{"admin, any batch", post(stranger, "admin", dept, &otherBatch), http.StatusCreated},
	}
	for _, tc := range cases {
		if tc.w.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, tc.w.Code, tc.want, tc.w.Body.String())
		}
	}

	var n domain.ClassNotice
	if err := db.Where("created_by_id = ?", cr).First(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n.BatchID == nil || *n.BatchID != myBatch {
		t.Errorf("CR notice not pinned to their batch: %v", n.BatchID)
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
