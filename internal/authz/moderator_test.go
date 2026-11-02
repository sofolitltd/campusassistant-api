package authz

import (
	"context"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestCanUsesActiveScopePermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&domain.Moderator{}); err != nil {
		t.Fatal(err)
	}

	userID, batchOnlyUser, temporalUser := uuid.New(), uuid.New(), uuid.New()
	universityID, departmentID, batchID := uuid.New(), uuid.New(), uuid.New()
	futureStart := time.Now().Add(time.Hour)
	expiredEnd := time.Now().Add(-time.Hour)
	roles := []domain.Moderator{
		{UserID: batchOnlyUser, ScopeType: domain.ModeratorScopeBatch, ScopeID: batchID, UniversityID: universityID, DepartmentID: &departmentID, IsActive: true},
		{UserID: userID, ScopeType: domain.ModeratorScopeDepartment, ScopeID: departmentID, UniversityID: universityID, IsActive: true},
		{UserID: userID, ScopeType: domain.ModeratorScopeUniversity, ScopeID: universityID, UniversityID: universityID, IsActive: true},
		{UserID: temporalUser, ScopeType: domain.ModeratorScopeBatch, ScopeID: uuid.New(), UniversityID: universityID, IsActive: true, TermStart: &futureStart},
		{UserID: temporalUser, ScopeType: domain.ModeratorScopeBatch, ScopeID: uuid.New(), UniversityID: universityID, IsActive: true, TermEnd: &expiredEnd},
		{UserID: userID, ScopeType: domain.ModeratorScopeDepartment, ScopeID: uuid.New(), UniversityID: universityID, IsActive: false},
	}
	for i := range roles {
		if err := db.Create(&roles[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	otherBatchID := uuid.New()
	tests := []struct {
		name       string
		userID     uuid.UUID
		permission Permission
		target     Target
		admin      bool
		want       bool
	}{
		{"batch code access", batchOnlyUser, CodesView, Target{UniversityID: universityID, DepartmentID: departmentID, BatchID: batchID}, false, true},
		{"batch cannot review resources", batchOnlyUser, ResourcesReview, Target{UniversityID: universityID, DepartmentID: departmentID, BatchID: batchID}, false, false},
		{"department covers its batches", userID, ResourcesReview, Target{UniversityID: universityID, DepartmentID: departmentID, BatchID: otherBatchID}, false, true},
		{"university covers departments", userID, ResourcesReview, Target{UniversityID: universityID, DepartmentID: uuid.New(), BatchID: otherBatchID}, false, true},
		{"unrelated scope denied", userID, CodesView, Target{UniversityID: uuid.New(), DepartmentID: uuid.New(), BatchID: otherBatchID}, false, false},
		{"admin bypass", uuid.Nil, ResourcesReview, Target{}, true, true},
		{"future scope denied", temporalUser, CodesView, Target{UniversityID: universityID, BatchID: roles[3].ScopeID}, false, false},
		{"expired scope denied", temporalUser, CodesView, Target{UniversityID: universityID, BatchID: roles[4].ScopeID}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Can(context.Background(), db, Actor{UserID: tt.userID, IsAdmin: tt.admin}, tt.permission, tt.target)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Can() = %v, want %v", got, tt.want)
			}
		})
	}
}
