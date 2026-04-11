package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/datatypes"
)

// ResourceStatus is the moderation/workflow state of a resource.
type ResourceStatus string

const (
	ResourceStatusPublished ResourceStatus = "published" // Visible to everyone
	ResourceStatusPending   ResourceStatus = "pending"   // Awaiting admin review
	ResourceStatusRejected  ResourceStatus = "rejected"  // Denied by admin
	ResourceStatusDraft     ResourceStatus = "draft"     // Saved but not submitted yet
)

// ResourceAccessLevel controls who can access the content.
type ResourceAccessLevel string

const (
	AccessLevelBasic ResourceAccessLevel = "basic" // Free for all
	AccessLevelPro   ResourceAccessLevel = "pro"   // Pro subscribers only
)

// Resource represents any academic material (Note, Question, Book, Syllabus).
// Uses a unified model with a JSONB `metadata` field for type-specific data.
type Resource struct {
	Base
	Type         ResourceType `gorm:"size:20;index" json:"type"` // note, question, book, syllabus
	Title        string       `gorm:"size:255;not null" json:"title"`
	Description  string       `gorm:"size:1000" json:"description"`
	CourseCode   string       `gorm:"size:50;index" json:"course_code"`
	FileURL      string       `json:"file_url"`
	ThumbnailURL string       `gorm:"size:500" json:"thumbnail_url"` // Optional PDF preview image
	LessonNo     int          `json:"lesson_no"`

	// Workflow / Moderation
	Status       ResourceStatus      `gorm:"size:20;default:'published';index" json:"status"` // published, pending, rejected, draft
	AccessLevel  ResourceAccessLevel `gorm:"size:20;default:'basic'" json:"access_level"`     // basic, pro
	RejectedNote string              `gorm:"size:500" json:"rejected_note,omitempty"`         // Admin's rejection reason
	ReviewedByID *uuid.UUID          `gorm:"type:uuid;index" json:"reviewed_by_id,omitempty"`
	ReviewedAt   *time.Time          `json:"reviewed_at,omitempty"`

	// Uploader
	UploaderID   *uuid.UUID `gorm:"type:uuid;index" json:"uploader_id,omitempty"`
	UploaderUID  string     `gorm:"size:128;index" json:"uploader_uid"`
	UploaderName string     `gorm:"size:100" json:"uploader_name"`
	Uploader     *User      `gorm:"foreignKey:UploaderID" json:"uploader,omitempty"`

	// Org Relations
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`

	// File Metrics
	FileSizeBytes int64 `gorm:"default:0" json:"file_size_bytes"` // e.g. 2457600
	PageCount     int   `gorm:"default:0" json:"page_count"`      // Number of pages in document
	DownloadCount int64 `gorm:"default:0" json:"download_count"`  // Total download counter
	ViewCount     int   `gorm:"default:0" json:"view_count"`      // Total view counter

	// Quality Signals
	RatingAvg   float64 `gorm:"default:0" json:"rating_avg"`      // 0.0 – 5.0 average
	RatingCount int     `gorm:"default:0" json:"rating_count"`    // Number of ratings
	IsVerified  bool    `gorm:"default:false" json:"is_verified"` // Admin-verified quality badge

	// Discovery
	Tags     pq.StringArray `gorm:"type:text[];default:'{}'" json:"tags"` // ["midterm","important"]
	IsPublic bool           `gorm:"default:true" json:"is_public"`

	// Type-specific metadata (JSONB) — flexible per resource type:
	//   note:     {"lesson_no": 4, "note_type": "typed", "teacher": "Dr. Rahman", "chapter": "Ch2"}
	//   book:     {"author": "Cormen", "publisher": "MIT Press", "edition": "3rd", "isbn": "..."}
	//   question: {"exam_type": "final", "has_answers": true, "marks": 80, "duration": "3h"}
	//   syllabus: {"academic_year": "2024-2025", "credit_hours": 3.0, "effective_from": "2024-01"}
	Metadata *datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"`

	// Complex Metadata (kept for backward compat with Years in question banks)
	Years   *datatypes.JSON `gorm:"type:jsonb" json:"years,omitempty"` // For Questions e.g. [2016, 2017]
	Batches []Batch         `gorm:"many2many:resource_batches;save_associations:false" json:"batches,omitempty"`

	// TODO: FCM notification fields — add when notification service is ready
	// NotifyOnApproval bool  — flag to send push to uploader on status change
}
