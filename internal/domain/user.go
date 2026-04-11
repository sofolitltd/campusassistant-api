package domain

import (
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

	// Profile Fields
	FCMToken   string `gorm:"index" json:"fcm_token,omitempty"`
	Role       Role   `gorm:"type:varchar(20);default:'student'" json:"role"`
	FirstName  string `gorm:"size:100" json:"first_name"`
	LastName   string `gorm:"size:100" json:"last_name"`
	Phone      string `gorm:"size:20" json:"phone"`
	Gender     string `gorm:"size:10" json:"gender"` // e.g. Male, Female
	AvatarURL  string `json:"avatar_url"`
	IsActive   bool   `gorm:"default:true" json:"is_active"`
	IsVerified bool   `gorm:"default:false" json:"is_verified"`

	// Privacy Settings
	IsPhonePublic bool `gorm:"default:false" json:"is_phone_public"`
	IsEmailPublic bool `gorm:"default:false" json:"is_email_public"`

	// Organizational Links
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"`
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
