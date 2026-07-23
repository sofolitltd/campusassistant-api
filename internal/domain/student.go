package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Student represents a student profile.
type Student struct {
	Base
	UserID           *uuid.UUID  `gorm:"type:uuid;uniqueIndex" json:"user_id,omitempty"`
	User             *User       `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user,omitempty"`
	StudentID        string      `gorm:"size:50;index" json:"student_id"` // Firestore "id": "15608055"
	BatchID          uuid.UUID   `gorm:"type:uuid;not null;index" json:"batch_id"`
	Batch            *Batch      `json:"batch,omitempty"`
	DepartmentID     uuid.UUID   `gorm:"type:uuid;not null;index" json:"department_id"`
	Department       *Department `json:"department,omitempty"`
	UniversityID     uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	University       *University `json:"university,omitempty"`
	SessionID        uuid.UUID   `gorm:"type:uuid;not null;index" json:"session_id"`
	Session          *Session    `json:"session,omitempty"`
	HallID           *uuid.UUID  `gorm:"type:uuid;index" json:"hall_id,omitempty"`
	Hall             *Hall       `json:"hall,omitempty"`
	Name             string      `gorm:"size:100" json:"name"`
	Email            string      `gorm:"size:100" json:"email"`
	Phone            string      `gorm:"size:20" json:"phone"`
	IsRegular        bool        `gorm:"default:true" json:"is_regular"`
	BloodGroup       string      `gorm:"size:5" json:"blood_group"`
	Weight           int         `gorm:"default:0" json:"weight"` // Firestore "orderBy"
	IsCR             bool        `gorm:"default:false" json:"is_cr"`
	VerificationCode string      `gorm:"size:20;index" json:"verification_code"` // For profile claiming
	IsClaimed        bool        `gorm:"default:false" json:"is_claimed"`
	ClaimedAt        *time.Time  `json:"claimed_at,omitempty"`
	// PresentAddress/PermanentAddress are the student's own home location —
	// distinct from the marketplace Address model (which snapshots
	// per-order shipping details). Stored as JSONB (see StudentAddressInfo)
	// rather than new columns, mirroring Association's SocialLinks pattern;
	// district/sub-district reference domain.BDDistricts by slug ID, same
	// as Association.DistrictID/SubDistrictID.
	PresentAddress   *datatypes.JSON `gorm:"type:jsonb" json:"present_address,omitempty"`
	PermanentAddress *datatypes.JSON `gorm:"type:jsonb" json:"permanent_address,omitempty"`
}

// StudentAddressInfo is the shape stored inside Student.PresentAddress /
// PermanentAddress. DistrictName/SubDistrictName are resolved and
// denormalized server-side from domain.BDDistricts — never trust
// client-submitted names, only the IDs.
type StudentAddressInfo struct {
	DistrictID      string `json:"district_id"`
	DistrictName    string `json:"district_name"`
	SubDistrictID   string `json:"sub_district_id,omitempty"`
	SubDistrictName string `json:"sub_district_name,omitempty"`
	AddressLine     string `json:"address_line,omitempty"`
}
