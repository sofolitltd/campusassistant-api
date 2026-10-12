package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type StudentVerificationStatus string

const (
	StudentVerificationPending  StudentVerificationStatus = "pending"
	StudentVerificationApproved StudentVerificationStatus = "approved"
	StudentVerificationRejected StudentVerificationStatus = "rejected"
)

// StudentVerificationDocType is the second proof a student submits next to
// their student ID card. NID is deliberately not accepted — both options are
// university-issued and carry the roll, without the sensitivity of a national ID.
type StudentVerificationDocType string

const (
	StudentVerificationDocRegistrationCard StudentVerificationDocType = "registration_card"
	StudentVerificationDocCertificate      StudentVerificationDocType = "certificate"
)

// StudentVerificationUploadFolder is the private upload folder both documents
// must come from (see handler.sensitiveUploadFolders).
const StudentVerificationUploadFolder = "student-verification"

// StudentVerificationRequest is the self-service alternative to the
// CR-issued verification code: a signed-in user with no Student profile
// submits their batch + roll and two documents, and an admin approves
// (linking an existing unclaimed Student row, or creating one) or rejects.
type StudentVerificationRequest struct {
	Base
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	User   *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`

	UniversityID uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	University   *University `gorm:"foreignKey:UniversityID" json:"university,omitempty"`
	DepartmentID uuid.UUID   `gorm:"type:uuid;not null;index" json:"department_id"`
	Department   *Department `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`
	BatchID      uuid.UUID   `gorm:"type:uuid;not null;index" json:"batch_id"`
	Batch        *Batch      `gorm:"foreignKey:BatchID" json:"batch,omitempty"`
	SessionID    uuid.UUID   `gorm:"type:uuid;not null" json:"session_id"`
	Session      *Session    `gorm:"foreignKey:SessionID" json:"session,omitempty"`

	// StudentID is the roll, same meaning as Student.StudentID.
	StudentID string `gorm:"size:50;not null;index" json:"student_id"`
	Name      string `gorm:"size:100;not null" json:"name"`
	Phone     string `gorm:"size:20" json:"phone"`

	// Both documents are private Attachments, viewable only through the
	// admin presigned-URL resolver (/attachments/:id/url).
	IDCardAttachmentID    uuid.UUID                  `gorm:"type:uuid;not null" json:"id_card_attachment_id"`
	SecondDocType         StudentVerificationDocType `gorm:"type:varchar(30);not null" json:"second_doc_type"`
	SecondDocAttachmentID uuid.UUID                  `gorm:"type:uuid;not null" json:"second_doc_attachment_id"`

	Status          StudentVerificationStatus `gorm:"type:varchar(20);default:'pending';index" json:"status"`
	RejectionReason string                    `gorm:"type:text" json:"rejection_reason,omitempty"`
	ReviewedByID    *uuid.UUID                `gorm:"type:uuid" json:"reviewed_by_id,omitempty"`
	ReviewedAt      *time.Time                `json:"reviewed_at,omitempty"`
	LinkedStudentID *uuid.UUID                `gorm:"type:uuid" json:"linked_student_id,omitempty"`
}

type StudentVerificationRepository interface {
	Create(ctx context.Context, r *StudentVerificationRequest) error
	GetByID(ctx context.Context, id uuid.UUID) (*StudentVerificationRequest, error)
	GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]StudentVerificationRequest, int64, error)
	GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*StudentVerificationRequest, error)
}
