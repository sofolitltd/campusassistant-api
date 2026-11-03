package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// A note credits its authors (teacher, students, plain names, a textbook);
// only the uploader, admins and moderators in scope (a batch CR included) can
// change them; profiles count what they're credited with.
func TestResourceAuthors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Batch{}, &domain.Student{}, &domain.Teacher{}, &domain.Moderator{},
		&domain.Resource{}, &domain.ResourceAuthor{}, &domain.AuditLog{}, &domain.SystemLog{}); err != nil {
		t.Fatal(err)
	}
	uni, psy := uuid.New(), uuid.New()
	batch := domain.Batch{Name: "batch-13", Slug: "batch-13", DepartmentID: psy, UniversityID: uni}
	batch.ID = uuid.New()
	db.Create(&batch)
	teacher := domain.Teacher{Name: "Md. Rohmotul Islam", DepartmentID: psy, UniversityID: uni}
	teacher.ID = uuid.New()
	db.Create(&teacher)
	mkStudent := func(name string) (profile, user uuid.UUID) {
		user = uuid.New()
		s := domain.Student{Name: name, StudentID: uuid.NewString(), BatchID: batch.ID, DepartmentID: psy, UniversityID: uni, UserID: &user}
		s.ID = uuid.New()
		db.Create(&s)
		return s.ID, user
	}
	hridoy, hridoyUser := mkStudent("Afjal Hossain Hridoy")
	tanvir, _ := mkStudent("Tanvir Ahmed")
	_, otherUser := mkStudent("Someone Else")
	_, crUser := mkStudent("Batch CR")
	db.Create(&domain.Moderator{UserID: crUser, ScopeType: domain.ModeratorScopeBatch, ScopeID: batch.ID, UniversityID: uni, DepartmentID: &psy, IsActive: true})

	gin.SetMode(gin.TestMode)
	resources := NewResourceHandler(usecase.NewGenericUsecase(postgres.NewResourceRepository(db)), nil, db, nil)
	as := func(role string, user uuid.UUID) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_role", role); c.Set("user_id", user); c.Next() })
		r.POST("/resources", resources.Create)
		r.GET("/resources", resources.GetAll)
		r.GET("/resources/contributor", resources.ContributorStats)
		r.GET("/resources/:id", resources.GetByID)
		r.PUT("/resources/:id", resources.Update)
		r.DELETE("/resources/:id", resources.Delete)
		return r
	}
	call := func(r *gin.Engine, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, &buf))
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	names := func(res map[string]any) string {
		authors, _ := res["authors"].([]any)
		s := ""
		for _, a := range authors {
			m := a.(map[string]any)
			s += fmt.Sprintf("%s:%s;", m["role"], m["name"])
		}
		return s
	}
	note := func(title string, authors ...map[string]any) map[string]any {
		return map[string]any{"type": "note", "title": title, "course_code": "psy-101", "status": "published",
			"department_id": psy, "university_id": uni, "batch_ids": []string{batch.ID.String()}, "authors": authors}
	}
	lecture := map[string]any{"role": "lecture", "teacher_id": teacher.ID}
	byHridoy := map[string]any{"role": "writer", "student_id": hridoy}

	// The uploader credits the teacher, two students, a plain name and a book.
	// Linked authors take their profile's name.
	code, created := call(as("student", hridoyUser), http.MethodPost, "/resources", note("Bandura",
		lecture, byHridoy, map[string]any{"role": "writer", "student_id": tanvir},
		map[string]any{"role": "writer", "name": " Sojol "}, map[string]any{"role": "source", "name": "Davison"}))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	want := "lecture:Md. Rohmotul Islam;writer:Afjal Hossain Hridoy;writer:Tanvir Ahmed;writer:Sojol;source:Davison;"
	if got := names(created); got != want {
		t.Fatalf("created authors: %s", got)
	}
	id := created["id"].(string)
	if _, got := call(as("student", otherUser), http.MethodGet, "/resources/"+id, nil); names(got) != want {
		t.Fatalf("read back: %s", names(got))
	}

	// Bad input is a 400.
	for name, a := range map[string]map[string]any{
		"unknown role":   {"role": "editor", "name": "x"},
		"both links":     {"role": "writer", "teacher_id": teacher.ID, "student_id": hridoy},
		"book with link": {"role": "source", "student_id": hridoy},
		"no name":        {"role": "writer"},
	} {
		if code, _ := call(as("student", hridoyUser), http.MethodPost, "/resources", note("bad", a)); code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", name, code)
		}
	}

	// Someone else can neither edit nor delete it.
	if code, _ := call(as("student", otherUser), http.MethodPut, "/resources/"+id, map[string]any{"authors": []any{}}); code != http.StatusForbidden {
		t.Fatalf("other student edit: %d", code)
	}
	if code, _ := call(as("student", otherUser), http.MethodDelete, "/resources/"+id, nil); code != http.StatusForbidden {
		t.Fatalf("other student delete: %d", code)
	}

	// The batch CR corrects the authors; an update without "authors" keeps them.
	code, updated := call(as("student", crUser), http.MethodPut, "/resources/"+id, map[string]any{"authors": []any{lecture, byHridoy}})
	if code != http.StatusOK || names(updated) != "lecture:Md. Rohmotul Islam;writer:Afjal Hossain Hridoy;" {
		t.Fatalf("CR edit: %d %s", code, names(updated))
	}
	if _, kept := call(as("student", hridoyUser), http.MethodPut, "/resources/"+id, map[string]any{"title": "Bandura (edited)"}); names(kept) != names(updated) {
		t.Fatalf("title-only update changed authors: %s", names(kept))
	}

	// Profile lists and stats. Four more notes make Hridoy the department's
	// top writer (5 notes); the teacher has one lecture credit from each.
	for i := 0; i < 4; i++ {
		call(as("student", hridoyUser), http.MethodPost, "/resources", note(fmt.Sprintf("note %d", i), lecture, byHridoy))
	}
	call(as("student", hridoyUser), http.MethodPost, "/resources", note("by Tanvir", map[string]any{"role": "writer", "student_id": tanvir}))

	_, list := call(as("student", otherUser), http.MethodGet, "/resources?author_teacher_id="+teacher.ID.String(), nil)
	if n := len(list["data"].([]any)); n != 5 {
		t.Fatalf("teacher's notes: %d", n)
	}
	_, stats := call(as("student", otherUser), http.MethodGet, "/resources/contributor?student_id="+hridoy.String(), nil)
	if stats["written"] != 5.0 || stats["uploaded"] != 6.0 || stats["rank"] != 1.0 || stats["badge"] != "top_contributor" {
		t.Fatalf("hridoy stats: %v", stats)
	}
	_, stats = call(as("student", otherUser), http.MethodGet, "/resources/contributor?student_id="+tanvir.String(), nil)
	if stats["written"] != 1.0 || stats["rank"] != 2.0 || stats["badge"] != nil {
		t.Fatalf("tanvir stats: %v", stats)
	}
	_, stats = call(as("student", otherUser), http.MethodGet, "/resources/contributor?teacher_id="+teacher.ID.String(), nil)
	if stats["lectures"] != 5.0 || stats["written"] != 0.0 {
		t.Fatalf("teacher stats: %v", stats)
	}

	// An empty list clears the credits.
	if _, cleared := call(as("admin", uuid.New()), http.MethodPut, "/resources/"+id, map[string]any{"authors": []any{}}); names(cleared) != "" {
		t.Fatalf("clear: %s", names(cleared))
	}
}
