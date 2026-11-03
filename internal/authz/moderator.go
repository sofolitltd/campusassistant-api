package authz

import (
	"context"
	"errors"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Permission string

const (
	CodesView           Permission = "codes.view"
	CodesRegenerate     Permission = "codes.regenerate"
	VerificationsReview Permission = "verifications.review"
	ClassNoticesManage  Permission = "class_notices.manage"
	ClassSlotsManage    Permission = "class_slots.manage"
	ResourcesReview     Permission = "resources.review"
	// ResourcesEdit lets a moderator (including a batch CR) correct a
	// resource in their scope, e.g. its authors.
	ResourcesEdit Permission = "resources.edit"
)

type Target struct {
	UniversityID uuid.UUID
	DepartmentID uuid.UUID
	BatchID      uuid.UUID
}

type Actor struct {
	UserID  uuid.UUID
	IsAdmin bool
}

type Scope struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ScopeType    domain.ModeratorScope
	ScopeID      uuid.UUID
	UniversityID uuid.UUID
	DepartmentID *uuid.UUID
	TermStart    *time.Time
	TermEnd      *time.Time
}

func permissions(scope domain.ModeratorScope) []Permission {
	result := []Permission{
		CodesView,
		CodesRegenerate,
		VerificationsReview,
		ClassNoticesManage,
		ClassSlotsManage,
		ResourcesEdit,
	}
	if scope == domain.ModeratorScopeDepartment || scope == domain.ModeratorScopeUniversity {
		result = append(result, ResourcesReview)
	}
	return result
}

func Can(ctx context.Context, db *gorm.DB, actor Actor, permission Permission, target Target) (bool, error) {
	if actor.IsAdmin {
		return true, nil
	}
	if actor.UserID == uuid.Nil {
		return false, nil
	}

	query := domain.ActiveModerator(db.WithContext(ctx)).Where("user_id = ?", actor.UserID)
	scopeConditions := make([]string, 0, 3)
	args := make([]interface{}, 0, 3)
	if target.BatchID != uuid.Nil {
		scopeConditions = append(scopeConditions, "(scope_type = ? AND scope_id = ?)")
		args = append(args, domain.ModeratorScopeBatch, target.BatchID)
	}
	if target.DepartmentID != uuid.Nil {
		scopeConditions = append(scopeConditions, "(scope_type = ? AND scope_id = ?)")
		args = append(args, domain.ModeratorScopeDepartment, target.DepartmentID)
	}
	if target.UniversityID != uuid.Nil {
		scopeConditions = append(scopeConditions, "(scope_type = ? AND scope_id = ?)")
		args = append(args, domain.ModeratorScopeUniversity, target.UniversityID)
	}
	if len(scopeConditions) == 0 {
		return false, nil
	}

	var rows []domain.Moderator
	err := query.Where(joinOr(scopeConditions), args...).Find(&rows).Error
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		for _, permitted := range permissions(row.ScopeType) {
			if permitted == permission {
				return true, nil
			}
		}
	}
	return false, nil
}

func Scopes(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]domain.ModeratorRole, error) {
	var assignments []domain.Moderator
	if err := domain.ActiveModerator(db.WithContext(ctx)).
		Where("user_id = ?", userID).
		Order("created_at ASC").
		Find(&assignments).Error; err != nil {
		return nil, err
	}

	result := make([]domain.ModeratorRole, 0, len(assignments))
	for _, assignment := range assignments {
		name, err := scopeName(db.WithContext(ctx), assignment)
		if err != nil {
			return nil, err
		}
		perms := permissions(assignment.ScopeType)
		permissionNames := make([]string, len(perms))
		for i, permission := range perms {
			permissionNames[i] = string(permission)
		}
		result = append(result, domain.ModeratorRole{
			ID:          assignment.ID,
			ScopeType:   assignment.ScopeType,
			ScopeID:     assignment.ScopeID,
			ScopeName:   name,
			TermEnd:     assignment.TermEnd,
			Permissions: permissionNames,
		})
	}
	return result, nil
}

func scopeName(db *gorm.DB, assignment domain.Moderator) (string, error) {
	table := map[domain.ModeratorScope]string{
		domain.ModeratorScopeBatch:      "batches",
		domain.ModeratorScopeDepartment: "departments",
		domain.ModeratorScopeUniversity: "universities",
	}[assignment.ScopeType]
	if table == "" {
		return "", errors.New("invalid moderator scope")
	}
	var name string
	if err := db.Table(table).Select("name").Where("id = ?", assignment.ScopeID).Scan(&name).Error; err != nil {
		return "", err
	}
	return name, nil
}

func joinOr(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result += " OR " + part
	}
	return result
}
