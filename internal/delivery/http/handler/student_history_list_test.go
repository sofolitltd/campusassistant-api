package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// An Honors batch whose students moved to Masters still lists them with
// include_history=true, as they were in Honors; without the flag, and in
// the Masters batch, nothing changes.
func TestStudentListIncludeHistory(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Session{}, &domain.Batch{}, &domain.Student{}, &domain.StudentEnrollment{}, &domain.User{}); err != nil {
		t.Fatal(err)
	}
	uni, dept := uuid.New(), uuid.New()
	hs, ms := domain.Session{Name: "2017-2018"}, domain.Session{Name: "2021-2022"}
	hs.ID, ms.ID = uuid.New(), uuid.New()
	db.Create(&hs)
	db.Create(&ms)
	honours := domain.Batch{Name: "Batch 10", Slug: "batch-10", DepartmentID: dept, UniversityID: uni}
	masters := domain.Batch{Name: "Masters Batch 10", Slug: "masters-batch-10", Program: domain.BatchProgramMasters, DepartmentID: dept, UniversityID: uni}
	honours.ID, masters.ID = uuid.New(), uuid.New()
	db.Create(&honours)
	db.Create(&masters)

	mk := func(name, roll string, batch, session uuid.UUID, regular bool) domain.Student {
		s := domain.Student{Name: name, StudentID: roll, BatchID: batch, SessionID: session, DepartmentID: dept, UniversityID: uni, IsRegular: regular}
		s.ID = uuid.New()
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if !regular {
			db.Model(&s).Update("is_regular", false)
		}
		return s
	}
	moved := mk("Rahim", "M-001", masters.ID, ms.ID, true)     // was Honors 17608001, irregular there
	stayed := mk("Karim", "17608002", honours.ID, hs.ID, true) // never promoted
	newcomer := mk("Salma", "M-900", masters.ID, ms.ID, true)  // joined Masters from outside
	enr := domain.StudentEnrollment{StudentID: moved.ID, BatchID: honours.ID, SessionID: hs.ID, DepartmentID: dept, UniversityID: uni,
		Program: domain.BatchProgramHonours, Roll: "17608001", IsCR: true, IsRegular: false, Status: domain.EnrollmentStatusCompleted}
	enr.ID = uuid.New()
	db.Create(&enr)
	db.Model(&enr).Update("is_regular", false)

	h := NewStudentHandler(usecase.NewGenericUsecase(postgres.NewGormRepository[domain.Student](db)), db, nil)
	gin.SetMode(gin.TestMode)
	list := func(query string) (int, []domain.Student) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/students?"+query, nil)
		c.Set("user_role", "super_admin")
		h.GetAll(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", query, rec.Code, rec.Body.String())
		}
		var body struct {
			Data  []domain.Student `json:"data"`
			Count int              `json:"count"`
		}
		json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Count, body.Data
	}

	n, rows := list("batch_id=" + honours.ID.String() + "&include_history=true")
	if n != 2 || len(rows) != 2 {
		t.Fatalf("honours with history: count %d rows %d, want 2", n, len(rows))
	}
	for _, r := range rows {
		switch r.ID {
		case moved.ID:
			if r.StudentID != "17608001" || r.BatchID != honours.ID || r.SessionID != hs.ID || !r.IsCR || r.IsRegular {
				t.Fatalf("moved student not shown as in Honors: %+v", r)
			}
			if r.CurrentBatchID == nil || *r.CurrentBatchID != masters.ID || r.HistoryEnrollmentID == nil || *r.HistoryEnrollmentID != enr.ID {
				t.Fatalf("moved student not marked: current %v enrollment %v", r.CurrentBatchID, r.HistoryEnrollmentID)
			}
		case stayed.ID:
			if r.CurrentBatchID != nil || r.HistoryEnrollmentID != nil {
				t.Fatal("current member marked as history")
			}
		default:
			t.Fatalf("unexpected row %s", r.Name)
		}
	}

	if n, _ := list("batch_id=" + honours.ID.String() + "&include_history=true&is_regular=false"); n != 1 {
		t.Fatalf("irregular in honours = %d, want 1 (the enrollment's value)", n)
	}
	if n, _ := list("batch_id=" + honours.ID.String() + "&include_history=true&search=17608001"); n != 1 {
		t.Fatalf("search by honours roll = %d, want 1", n)
	}
	if n, _ := list("batch_id=" + honours.ID.String()); n != 1 {
		t.Fatalf("honours without flag = %d, want only the current member", n)
	}
	n, rows = list("batch_id=" + masters.ID.String() + "&include_history=true")
	if n != 2 {
		t.Fatalf("masters = %d, want 2", n)
	}
	for _, r := range rows {
		if r.HistoryEnrollmentID != nil {
			t.Fatalf("masters row %s marked as history", r.Name)
		}
		if r.ID == moved.ID && r.StudentID != "M-001" {
			t.Fatalf("masters roll = %s", r.StudentID)
		}
	}
	_ = newcomer
}
