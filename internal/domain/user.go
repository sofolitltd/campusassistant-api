package domain

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleSuperAdmin      Role = "super_admin"
	RoleUniversityAdmin Role = "university_admin"
	RoleDepartmentAdmin Role = "department_admin"
	RoleTeacher         Role = "teacher"
	RoleStudent         Role = "student"
	RoleStaff           Role = "staff"
)

// User represents the authentication entity.
// Supports both JWT (password-based) and Firebase authentication.
type User struct {
	Base
	// Authentication Fields
	Email        string `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string `gorm:"size:255" json:"-"` // JWT auth (bcrypt hash, never expose in JSON)
	// TokenVersion is embedded in every access token; bumping it invalidates
	// all previously-issued tokens (see SESSION_MANAGEMENT.md).
	TokenVersion int `gorm:"default:1" json:"-"`

	// Profile Fields
	Role       Role   `gorm:"type:varchar(20);default:'student'" json:"role"`
	FirstName  string `gorm:"size:100" json:"first_name"`
	LastName   string `gorm:"size:100" json:"last_name"`
	Phone      string `gorm:"size:20" json:"phone"`
	Gender     string `gorm:"size:10" json:"gender"` // e.g. Male, Female
	AvatarURL  string `json:"avatar_url"`
	IsActive   bool   `gorm:"default:true" json:"is_active"`
	IsVerified bool   `gorm:"default:false" json:"is_verified"`
	IsPro      bool   `gorm:"default:false" json:"is_pro"`
	ProExpiry  *time.Time `json:"pro_expiry,omitempty"`

	// Privacy Settings
	IsPhonePublic bool `gorm:"default:false" json:"is_phone_public"`
	IsEmailPublic bool `gorm:"default:false" json:"is_email_public"`

	// Organizational Links
	UniversityID *uuid.UUID  `gorm:"type:uuid;index" json:"university_id,omitempty"`
	University   *University `gorm:"foreignKey:UniversityID" json:"university,omitempty"`
	DepartmentID *uuid.UUID  `gorm:"type:uuid;index" json:"department_id,omitempty"`
	Department   *Department `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`

	// Academic Batch (populated from Student.Batch for convenience, not a DB column)
	Batch string `gorm:"-" json:"batch"`

	// Academic/Professional Profiles (Preloadable)
	Student *Student `gorm:"foreignKey:UserID" json:"student,omitempty"`
	Teacher *Teacher `gorm:"foreignKey:UserID" json:"teacher,omitempty"`
}

// FullName returns the user's full name
func (u *User) FullName() string {
	return u.FirstName + " " + u.LastName
}

// Verification represents user verification requests (e.g. ID card upload).
type Verification struct {
	Base
	UserID      uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Status      string    `gorm:"default:'pending'" json:"status"` // pending, approved, rejected
	DocumentURL string    `json:"document_url"`
}
