package handler

import (
	"bytes"
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

// The admin batch form sends session_ids; they must land in batch_sessions
// on create and replace them on edit, and a PUT without them leaves them.
func TestBatchSessionIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Session{}, &domain.Batch{}, &domain.Department{}, &domain.Level{}, &domain.Student{}, &domain.AuditLog{}, &domain.SystemLog{}); err != nil {
		t.Fatal(err)
	}
	s1, s2 := domain.Session{Name: "2017-2018"}, domain.Session{Name: "2018-2019"}
	s1.ID, s2.ID = uuid.New(), uuid.New()
	db.Create(&s1)
	db.Create(&s2)

	h := NewGenericHandler(usecase.NewGenericUsecase(postgres.NewGormRepository[domain.Batch](db)))
	gin.SetMode(gin.TestMode)
	call := func(fn gin.HandlerFunc, id, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		if id != "" {
			c.Params = gin.Params{{Key: "id", Value: id}}
		}
		c.Set("user_id", uuid.New())
		c.Set("user_role", "super_admin")
		fn(c)
		return rec
	}
	sessionsOf := func(id uuid.UUID) []uuid.UUID {
		var ids []uuid.UUID
		db.Table("batch_sessions").Where("batch_id = ?", id).Order("session_id").Pluck("session_id", &ids)
		return ids
	}

	dept, uni := uuid.New(), uuid.New()
	rec := call(h.Create, "", `{"name":"Batch 1","slug":"batch-1","department_id":"`+dept.String()+`","university_id":"`+uni.String()+`","session_ids":["`+s1.ID.String()+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var b domain.Batch
	db.First(&b, "slug = ?", "batch-1")
	if got := sessionsOf(b.ID); len(got) != 1 || got[0] != s1.ID {
		t.Fatalf("after create sessions = %v", got)
	}

	rec = call(h.Update, b.ID.String(), `{"name":"Batch 1","session_ids":["`+s2.ID.String()+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body.String())
	}
	if got := sessionsOf(b.ID); len(got) != 1 || got[0] != s2.ID {
		t.Fatalf("after edit sessions = %v, want only s2", got)
	}

	rec = call(h.Update, b.ID.String(), `{"is_studying":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("plain update = %d %s", rec.Code, rec.Body.String())
	}
	if got := sessionsOf(b.ID); len(got) != 1 || got[0] != s2.ID {
		t.Fatalf("update without session_ids changed sessions: %v", got)
	}
}
