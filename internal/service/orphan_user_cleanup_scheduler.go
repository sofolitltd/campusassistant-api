package service

import (
	"context"
	"log"
	"time"

	"campusassistant-api/internal/domain"

	"gorm.io/gorm"
)

// OrphanUserCleanupScheduler soft-deletes accounts stuck mid-signup in the
// document-based student onboarding flow (see
// DocumentSignupScreen/VerificationRequestScreen in the Flutter app):
// /auth/register creates the User row immediately, but the university/
// department/batch/session it belongs to is only validated afterwards, at
// /my/student-verification submission. Account creation and that validation
// are two independent, non-transactional writes, so any failure in between
// (an invalid placement, the user abandoning the flow, a crash) leaves a
// permanent account with no verification request and no way to ever become
// useful. This sweeps those up well after the fact, never touching an
// account that still has a chance of completing (recently created, or with
// any verification request on file, whatever its status).
type OrphanUserCleanupScheduler struct {
	db        *gorm.DB
	interval  time.Duration
	orphanAge time.Duration
}

func NewOrphanUserCleanupScheduler(db *gorm.DB) *OrphanUserCleanupScheduler {
	return &OrphanUserCleanupScheduler{
		db:        db,
		interval:  1 * time.Hour,
		orphanAge: 72 * time.Hour,
	}
}

// Start runs the sweep loop in a background goroutine until ctx is cancelled.
func (s *OrphanUserCleanupScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *OrphanUserCleanupScheduler) tick(ctx context.Context) {
	cutoff := time.Now().Add(-s.orphanAge)

	result := s.db.WithContext(ctx).
		Where("role = ?", domain.RoleStudent).
		Where("is_verified = false").
		Where("university_id IS NULL AND department_id IS NULL").
		Where("created_at < ?", cutoff).
		Where("id NOT IN (?)",
			s.db.Model(&domain.StudentVerificationRequest{}).Select("user_id"),
		).
		Delete(&domain.User{})

	if result.Error != nil {
		log.Printf("OrphanUserCleanupScheduler: sweep failed: %v", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		log.Printf("OrphanUserCleanupScheduler: soft-deleted %d orphaned signup(s) older than %s", result.RowsAffected, s.orphanAge)
	}
}
