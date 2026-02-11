package domain

import (
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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
type User struct {
	Base
	Email        string `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string `gorm:"not null" json:"-"`
	Role         Role   `gorm:"type:varchar(20);default:'student'" json:"role"`
	FirstName    string `gorm:"size:100" json:"first_name"`
	LastName     string `gorm:"size:100" json:"last_name"`
	Phone        string `gorm:"size:20" json:"phone"`
	AvatarURL    string `json:"avatar_url"`
	IsActive     bool   `gorm:"default:true" json:"is_active"`
	IsVerified   bool   `gorm:"default:false" json:"is_verified"` // Email verification

	// Polymorphic relations (optional but clean approach is explicit FKs or separate tables)
	// We will use separate tables linked by UserID for distinct profiles.
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"` // For admins/staff/teachers
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"` // For teachers/students/staff
}

// Student represents a student profile.
type Student struct {
	Base
	UserID     uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"user_id"`
	User       User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user"`
	BatchID    uuid.UUID `gorm:"type:uuid;not null;index" json:"batch_id"`
	Batch      *Batch    `json:"batch,omitempty"`
	RollNumber string    `gorm:"size:50;index" json:"roll_number"`
	RegNumber  string    `gorm:"size:50;index" json:"reg_number"`
	IsCR       bool      `gorm:"default:false" json:"is_cr"` // Class Representative
}

// Teacher represents a teacher profile.
type Teacher struct {
	Base
	UserID       uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"user_id"`
	User         User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user"`
	DepartmentID uuid.UUID `gorm:"type:uuid;not null;index" json:"department_id"`
	Designation  string    `gorm:"size:100" json:"designation"` // e.g. Professor, Lecturer
}

// Staff represents a general staff member.
type Staff struct {
	Base
	UserID       uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"user_id"`
	User         User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user"`
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	Position     string    `gorm:"size:100" json:"position"`
}

// Verification represents user verification requests (e.g. ID card upload).
type Verification struct {
	Base
	UserID      uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Status      string    `gorm:"default:'pending'" json:"status"` // pending, approved, rejected
	DocumentURL string    `json:"document_url"`
}

func (u *User) SetPassword(password string) error {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hashed)
	return nil
}

func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	return err == nil
}
