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

// Two departments both teach PSY-101. Listing by course code needs a
// department, and the level filter stays inside the resource's own
// department.
func TestCourseCodeScopedByDepartment(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Batch{}, &domain.Course{}, &domain.Chapter{}, &domain.Resource{}); err != nil {
		t.Fatal(err)
	}
	uni, psy, edu := uuid.New(), uuid.New(), uuid.New()
	psyLevel, eduLevel := uuid.New(), uuid.New()
	for _, c := range []domain.Course{
		{CourseCode: "PSY-101", CourseTitle: "Intro to Psychology", DepartmentID: psy, UniversityID: uni, LevelID: &psyLevel},
		{CourseCode: "PSY-101", CourseTitle: "Educational Psychology", DepartmentID: edu, UniversityID: uni, LevelID: &eduLevel},
	} {
		c.ID = uuid.New()
		db.Create(&c)
	}
	for _, r := range []domain.Resource{
		{Type: "book", Title: "Psy book", CourseCode: "PSY-101", DepartmentID: psy, UniversityID: uni, Status: domain.ResourceStatusPublished},
		{Type: "book", Title: "Edu book", CourseCode: "PSY-101", DepartmentID: edu, UniversityID: uni, Status: domain.ResourceStatusPublished},
	} {
		r.ID = uuid.New()
		db.Create(&r)
	}

	gin.SetMode(gin.TestMode)
	h := NewGenericHandler(usecase.NewGenericUsecase(postgres.NewResourceRepository(db)))
	r := gin.New()
	r.GET("/resources", h.GetAll)
	list := func(query string) (int, []string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/resources?"+query, nil))
		var body struct {
			Data []domain.Resource `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		var titles []string
		for _, res := range body.Data {
			titles = append(titles, res.Title)
		}
		return w.Code, titles
	}

	if code, _ := list("course_code=PSY-101"); code != http.StatusBadRequest {
		t.Fatalf("course_code without department_id: got %d, want 400", code)
	}
	if code, got := list("course_code=PSY-101&department_id=" + psy.String()); code != 200 || len(got) != 1 || got[0] != "Psy book" {
		t.Fatalf("course_code in Psychology: %d %v", code, got)
	}
	// Psychology's level must not pull in Education's PSY-101 book.
	if _, got := list("level_id=" + psyLevel.String()); len(got) != 1 || got[0] != "Psy book" {
		t.Fatalf("level filter leaked across departments: %v", got)
	}
	// The library search's course-title match is scoped the same way, but
	// it uses ILIKE, which SQLite can't run.
}
