package handler

import (
	"bytes"
	"context"
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

// Every batch sees every resource, its own first; batch-only resources stay
// with their batch; a renamed course shares chapters and resources with its
// old code; a new batch can take an earlier batch's syllabus.
func TestStudyVisibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Batch{}, &domain.Student{}, &domain.Level{}, &domain.Course{}, &domain.Chapter{}, &domain.Resource{}); err != nil {
		t.Fatal(err)
	}
	uni, psy, edu := uuid.New(), uuid.New(), uuid.New()
	mkBatch := func(name string) domain.Batch {
		b := domain.Batch{Name: name, Slug: name, DepartmentID: psy, UniversityID: uni}
		b.ID = uuid.New()
		db.Create(&b)
		return b
	}
	b20, b21 := mkBatch("batch-20"), mkBatch("batch-21")
	mkStudent := func(batch uuid.UUID) uuid.UUID {
		userID := uuid.New()
		s := domain.Student{Name: "s", StudentID: uuid.NewString(), BatchID: batch, DepartmentID: psy, UniversityID: uni, UserID: &userID}
		s.ID = uuid.New()
		db.Create(&s)
		return userID
	}
	student20, student21 := mkStudent(b20.ID), mkStudent(b21.ID)

	// psy-101 became 0313PSY-1101 for Batch 21; Education has its own psy-101.
	for _, c := range []domain.Course{
		{CourseCode: "psy-101", CourseTitle: "Intro", DepartmentID: psy, UniversityID: uni},
		{CourseCode: "0313PSY-1101", PreviousCode: "psy-101", CourseTitle: "Intro", DepartmentID: psy, UniversityID: uni},
		{CourseCode: "psy-101", CourseTitle: "Edu Psy", DepartmentID: edu, UniversityID: uni},
	} {
		c.ID = uuid.New()
		db.Create(&c)
	}
	mkRes := func(title, code string, dept uuid.UUID, batchOnly bool, batches ...uuid.UUID) {
		r := domain.Resource{Type: "note", Title: title, CourseCode: code, DepartmentID: dept, UniversityID: uni,
			Status: domain.ResourceStatusPublished, BatchOnly: batchOnly, FileURL: "https://r2/" + title}
		r.ID = uuid.New()
		db.Create(&r)
		for _, b := range batches {
			db.Exec("INSERT INTO resource_batches (resource_id, batch_id) VALUES (?, ?)", r.ID, b)
		}
	}
	mkRes("old note", "psy-101", psy, false, b20.ID)
	mkRes("b21 suggestion", "0313PSY-1101", psy, true, b21.ID)
	mkRes("new note", "0313PSY-1101", psy, false, b21.ID)
	mkRes("edu note", "psy-101", edu, false)
	ch := domain.Chapter{CourseCode: "psy-101", ChapterNo: 1, ChapterTitle: "History", DepartmentID: psy, UniversityID: uni}
	ch.ID = uuid.New()
	db.Create(&ch)
	db.Exec("INSERT INTO chapter_batches (chapter_id, batch_id) VALUES (?, ?)", ch.ID, b20.ID)

	gin.SetMode(gin.TestMode)
	resources := NewResourceHandler(usecase.NewGenericUsecase(postgres.NewResourceRepository(db)), nil, db, nil)
	chapters := NewGenericHandler(usecase.NewGenericUsecase(postgres.NewChapterRepository(db)))
	as := func(role string, user uuid.UUID) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("user_role", role); c.Set("user_id", user); c.Next() })
		r.GET("/resources", resources.GetAll)
		r.GET("/chapters", chapters.GetAll)
		r.POST("/batches/:id/copy-syllabus", NewSyllabusCopyHandler(db).CopySyllabus)
		return r
	}
	titles := func(r *gin.Engine, path string) []string {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body struct {
			Data []struct {
				Title        string `json:"title"`
				ChapterTitle string `json:"chapter_title"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		out := []string{}
		for _, d := range body.Data {
			out = append(out, d.Title+d.ChapterTitle)
		}
		return out
	}
	q := "/resources?department_id=" + psy.String() + "&course_code="
	eq := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
	}

	// Batch 21 on its new code: its own first, then Batch 20's note from the
	// old code. Not Education's psy-101.
	eq("batch 21 student", titles(as("student", student21), q+"0313PSY-1101&batch_id="+b21.ID.String()),
		"new note", "b21 suggestion", "old note")
	// Batch 20 on the old code sees Batch 21's shared note, not its suggestion.
	eq("batch 20 student", titles(as("student", student20), q+"psy-101&batch_id="+b20.ID.String()),
		"old note", "new note")
	// The admin saves codes upper-cased; older rows are lower-case.
	eq("upper-case code", titles(as("student", student20), q+"PSY-101&batch_id="+b20.ID.String()),
		"old note", "new note")
	// The app also sends the batch name; with batch_id it doesn't filter.
	eq("batch name alongside id", titles(as("student", student20), q+"psy-101&batch=batch-20&batch_id="+b20.ID.String()),
		"old note", "new note")
	// Picking Batch 21 in the dropdown doesn't reveal its suggestion either.
	eq("batch 20 student picking 21", titles(as("student", student20), q+"psy-101&batch_id="+b21.ID.String()),
		"new note", "old note")
	// Admin sees everything.
	if got := titles(as("admin", uuid.New()), q+"psy-101"); len(got) != 3 {
		t.Fatalf("admin: %v", got)
	}
	// Chapters filed under the old code show under the new one.
	eq("chapters via previous code", titles(as("student", student21), "/chapters?department_id="+psy.String()+"&course_code=0313PSY-1101"), "History")

	// Batch 22 takes Batch 20's syllabus; running it twice adds nothing more.
	b22 := mkBatch("batch-22")
	copyFrom := func() int {
		w := httptest.NewRecorder()
		body, _ := json.Marshal(gin.H{"from_batch_id": b20.ID})
		as("admin", uuid.New()).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/batches/"+b22.ID.String()+"/copy-syllabus", bytes.NewReader(body)))
		return w.Code
	}
	if code := copyFrom(); code != 200 {
		t.Fatalf("copy-syllabus: %d", code)
	}
	copyFrom()
	var n int64
	db.Table("chapter_batches").Where("batch_id = ?", b22.ID).Count(&n)
	if n != 1 {
		t.Fatalf("batch 22 chapter tags: %d, want 1", n)
	}

	// A stored file another row still uses is kept on purge.
	if !fileStillUsed(context.Background(), db, "https://r2/old note") || fileStillUsed(context.Background(), db, "https://r2/gone") {
		t.Fatal("fileStillUsed")
	}
}
