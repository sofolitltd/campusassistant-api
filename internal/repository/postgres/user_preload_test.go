package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestUserGetByIDPreloadsStudentSession(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(
		&domain.User{}, &domain.Student{}, &domain.Session{}, &domain.Batch{},
		&domain.University{}, &domain.Department{}, &domain.Teacher{}, &domain.Staff{}, &domain.Hall{},
	); err != nil {
		t.Fatal(err)
	}

	session := domain.Session{Kind: domain.SessionKindAcademicYear, StartYear: 2025, Name: "2025-2026"}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	batch := domain.Batch{Name: "Batch 25", Slug: "batch-25", DepartmentID: uuid.New(), UniversityID: uuid.New()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	user := domain.User{Email: "student@example.com", Role: domain.RoleStudent, FirstName: "Test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	student := domain.Student{
		UserID: &user.ID, StudentID: "12345", BatchID: batch.ID, SessionID: session.ID,
		DepartmentID: uuid.New(), UniversityID: uuid.New(),
	}
	if err := db.Create(&student).Error; err != nil {
		t.Fatal(err)
	}

	got, err := NewGormRepository[domain.User](db).GetByID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Student == nil || got.Student.Session == nil || got.Student.Batch == nil {
		t.Fatal("student session and batch relations were not preloaded")
	}
	if got.Student.Session.Name != session.Name {
		t.Fatalf("student session name = %q, want %q", got.Student.Session.Name, session.Name)
	}
	if got.Student.Batch.Name != batch.Name {
		t.Fatalf("student batch name = %q, want %q", got.Student.Batch.Name, batch.Name)
	}
}
