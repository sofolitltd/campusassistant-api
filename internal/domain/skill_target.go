package domain

import "github.com/google/uuid"

// SkillTarget links a Skill to a specific university or department. A
// Skill with zero targets is global (visible to every user).
type SkillTarget struct {
	Base
	SkillID      uuid.UUID `gorm:"type:uuid;not null;index" json:"skill_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	// DepartmentID uuid.Nil means "whole university" (every department).
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}
