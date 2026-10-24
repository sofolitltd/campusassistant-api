package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLevelUpdateReplacesBatches(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&domain.Batch{}, &domain.Level{}); err != nil {
		t.Fatal(err)
	}

	b1 := domain.Batch{Name: "Batch 1", Slug: "batch-1"}
	b2 := domain.Batch{Name: "Batch 2", Slug: "batch-2"}
	db.Create(&b1)
	db.Create(&b2)

	repo := NewLevelRepository(db)
	ctx := context.Background()
	level := domain.Level{Name: "1st Year", BatchIDs: []uuid.UUID{b1.ID, b2.ID}}
	if err := repo.Create(ctx, &level); err != nil {
		t.Fatal(err)
	}

	linked := func() int64 {
		var n int64
		db.Table("level_batches").Where("level_id = ?", level.ID).Count(&n)
		return n
	}
	update := func(ids []uuid.UUID) {
		t.Helper()
		existing, err := repo.GetByID(ctx, level.ID)
		if err != nil {
			t.Fatal(err)
		}
		existing.BatchIDs = ids
		if err := repo.Update(ctx, existing); err != nil {
			t.Fatal(err)
		}
	}

	update([]uuid.UUID{b1.ID})
	if n := linked(); n != 1 {
		t.Fatalf("after removing a batch: %d linked, want 1", n)
	}

	update(nil)
	if n := linked(); n != 1 {
		t.Fatalf("update without batch_ids changed batches: %d linked, want 1", n)
	}

	update([]uuid.UUID{})
	if n := linked(); n != 0 {
		t.Fatalf("after clearing: %d linked, want 0", n)
	}
}

func TestLevelDeleteRefusedWhileCoursesRemain(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&domain.Batch{}, &domain.Level{}, &domain.Course{}); err != nil {
		t.Fatal(err)
	}

	repo := NewLevelRepository(db)
	ctx := context.Background()
	level := domain.Level{Name: "1st Year"}
	if err := repo.Create(ctx, &level); err != nil {
		t.Fatal(err)
	}
	course := domain.Course{CourseCode: "psy-101", CourseTitle: "Intro", LevelID: &level.ID}
	if err := db.Create(&course).Error; err != nil {
		t.Fatal(err)
	}

	if err := repo.Delete(ctx, level.ID); !errors.Is(err, domain.ErrInUse) {
		t.Fatalf("Delete with a course: got %v, want ErrInUse", err)
	}
	if err := repo.HardDelete(ctx, level.ID); !errors.Is(err, domain.ErrInUse) {
		t.Fatalf("HardDelete with a course: got %v, want ErrInUse", err)
	}

	// A deleted course no longer holds the level.
	db.Delete(&course)
	if err := repo.Delete(ctx, level.ID); err != nil {
		t.Fatalf("Delete without courses: %v", err)
	}
}
