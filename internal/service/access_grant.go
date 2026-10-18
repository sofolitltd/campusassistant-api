package service

import (
	"errors"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// userScope is where a user sits in the university → department → batch
// tree. User-level university/department win; the Student row fills gaps
// and is the only source of the batch.
type userScope struct {
	CreatedAt    time.Time
	UniversityID *uuid.UUID
	DepartmentID *uuid.UUID
	BatchID      *uuid.UUID
}

func loadUserScope(db *gorm.DB, userID uuid.UUID) (userScope, error) {
	var user domain.User
	if err := db.Select("id", "created_at", "university_id", "department_id").
		First(&user, "id = ?", userID).Error; err != nil {
		return userScope{}, err
	}
	s := userScope{CreatedAt: user.CreatedAt, UniversityID: user.UniversityID, DepartmentID: user.DepartmentID}

	var student domain.Student
	err := db.Select("id", "batch_id", "department_id", "university_id").
		Where("user_id = ?", userID).First(&student).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return userScope{}, err
	}
	if err == nil {
		s.BatchID = &student.BatchID
		if s.UniversityID == nil {
			s.UniversityID = &student.UniversityID
		}
		if s.DepartmentID == nil {
			s.DepartmentID = &student.DepartmentID
		}
	}
	return s, nil
}

// grantUntil is when g stops applying to a user created at createdAt: nil
// for never, and ok=false if it no longer applies at all.
func grantUntil(g domain.AccessGrant, createdAt, now time.Time) (until *time.Time, ok bool) {
	until = g.EndsAt
	if g.NewUserDays > 0 {
		cutoff := createdAt.AddDate(0, 0, g.NewUserDays)
		if until == nil || cutoff.Before(*until) {
			until = &cutoff
		}
	}
	if until != nil && !until.After(now) {
		return nil, false
	}
	return until, true
}

// later reports whether a ends after b, where nil means "never ends".
func later(a, b *time.Time) bool {
	if a == nil {
		return b != nil
	}
	return b != nil && a.After(*b)
}

// ResolveGrantPerks merges every active AccessGrant that covers the user.
// Each perk takes the latest end date among the grants that switch it on.
func ResolveGrantPerks(db *gorm.DB, userID uuid.UUID, now time.Time) (*domain.GrantPerks, error) {
	scope, err := loadUserScope(db, userID)
	if err != nil {
		return nil, err
	}

	q := db.Where("starts_at <= ? AND (ends_at IS NULL OR ends_at > ?)", now, now)
	match := db.Where("scope_type = ?", domain.GrantScopeAll).
		Or("scope_type = ? AND scope_id = ?", domain.GrantScopeUser, userID)
	if scope.UniversityID != nil {
		match = match.Or("scope_type = ? AND scope_id = ?", domain.GrantScopeUniversity, *scope.UniversityID)
	}
	if scope.DepartmentID != nil {
		match = match.Or("scope_type = ? AND scope_id = ?", domain.GrantScopeDepartment, *scope.DepartmentID)
	}
	if scope.BatchID != nil {
		match = match.Or("scope_type = ? AND scope_id = ?", domain.GrantScopeBatch, *scope.BatchID)
	}
	var grants []domain.AccessGrant
	if err := q.Where(match).Find(&grants).Error; err != nil {
		return nil, err
	}

	perks := &domain.GrantPerks{}
	var labelUntil *time.Time
	for _, g := range grants {
		until, ok := grantUntil(g, scope.CreatedAt, now)
		if !ok || (!g.AdFree && !g.CoinFree) {
			continue
		}
		if g.AdFree && (!perks.AdFree || later(until, perks.AdFreeUntil)) {
			perks.AdFree, perks.AdFreeUntil = true, until
		}
		if g.CoinFree && (!perks.CoinFree || later(until, perks.CoinFreeUntil)) {
			perks.CoinFree, perks.CoinFreeUntil = true, until
		}
		if perks.Label == "" || later(until, labelUntil) {
			perks.Label, labelUntil = g.Label, until
		}
	}
	return perks, nil
}
