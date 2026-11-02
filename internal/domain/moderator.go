package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ModeratorScope string

const (
	ModeratorScopeBatch      ModeratorScope = "batch"
	ModeratorScopeDepartment ModeratorScope = "department"
	ModeratorScopeUniversity ModeratorScope = "university"
)

type Moderator struct {
	Base
	UserID       uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:idx_moderators_active_scope,where:is_active = true AND deleted_at IS NULL" json:"user_id"`
	User         *User          `gorm:"foreignKey:UserID" json:"user,omitempty"`
	ScopeType    ModeratorScope `gorm:"size:20;not null;uniqueIndex:idx_moderators_active_scope,where:is_active = true AND deleted_at IS NULL" json:"scope_type"`
	ScopeID      uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:idx_moderators_active_scope,where:is_active = true AND deleted_at IS NULL" json:"scope_id"`
	UniversityID uuid.UUID      `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID *uuid.UUID     `gorm:"type:uuid;index" json:"department_id,omitempty"`
	TermStart    *time.Time     `json:"term_start,omitempty"`
	TermEnd      *time.Time     `json:"term_end,omitempty"`
	IsActive     bool           `gorm:"default:true;index" json:"is_active"`
	GrantedBy    *uuid.UUID     `gorm:"type:uuid" json:"granted_by,omitempty"`
	RevokedBy    *uuid.UUID     `gorm:"type:uuid" json:"revoked_by,omitempty"`
	RevokedAt    *time.Time     `json:"revoked_at,omitempty"`
	Note         string         `gorm:"type:text" json:"note"`
}

func ActiveModerator(db *gorm.DB) *gorm.DB {
	now := time.Now()
	return db.Where(
		"is_active = true AND (term_start IS NULL OR term_start <= ?) AND (term_end IS NULL OR term_end > ?)",
		now, now,
	)
}

type ModeratorRole struct {
	ID          uuid.UUID      `json:"id"`
	ScopeType   ModeratorScope `json:"scope_type"`
	ScopeID     uuid.UUID      `json:"scope_id"`
	ScopeName   string         `json:"scope_name"`
	TermEnd     *time.Time     `json:"term_end,omitempty"`
	Permissions []string       `json:"permissions"`
}
