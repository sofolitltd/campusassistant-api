package domain

import (
	"context"

	"github.com/google/uuid"
)

// Skill is a small, YouTube-video-backed course shown on the app's home
// page ("Skill Up" section) — a global content type, not tied to any
// university/department unless explicitly targeted via SkillTarget.
type Skill struct {
	Base
	Title        string        `gorm:"size:255;not null" json:"title"`
	Description  string        `gorm:"type:text" json:"description"`
	ThumbnailURL string        `gorm:"size:500" json:"thumbnail_url"`
	Index        int           `gorm:"default:0" json:"index"` // home-page ordering
	IsPublished  bool          `gorm:"default:false" json:"is_published"`
	Targets      []SkillTarget `gorm:"foreignKey:SkillID;constraint:OnDelete:CASCADE" json:"targets"`
	Videos       []SkillVideo  `gorm:"foreignKey:SkillID;constraint:OnDelete:CASCADE" json:"videos,omitempty"`
}

// SkillRepository defines database operations for the Skill catalog. Not a
// generic-CRUD resource: Targets need explicit delete-then-save handling
// on update (see UpdateSkill), same reason SubscriptionPlan isn't generic.
type SkillRepository interface {
	GetAllSkills(ctx context.Context) ([]Skill, error)
	GetSkillByID(ctx context.Context, id uuid.UUID) (*Skill, error)
	CreateSkill(ctx context.Context, skill *Skill) error
	UpdateSkill(ctx context.Context, skill *Skill) error
	DeleteSkill(ctx context.Context, id uuid.UUID) error

	// GetSkillsByLocation returns published skills that are either global
	// (no targets) or targeted to this university (department_id uuid.Nil
	// on the target means "whole university").
	GetSkillsByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]Skill, error)
}
