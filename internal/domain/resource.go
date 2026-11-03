package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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

	// Creator (resolved from CreatedByID on read)
	Creator *User `gorm:"foreignKey:CreatedByID" json:"creator,omitempty"`

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

	// BatchOnly keeps the resource to the students of its tagged batches.
	// Otherwise every batch sees it; the tags only say whose it is, and the
	// selected batch's resources are listed first.
	BatchOnly bool `gorm:"default:false;not null" json:"batch_only"`

	// Type-specific metadata (JSONB) — flexible per resource type:
	//   note:     {"lesson_no": 4, "note_type": "typed", "teacher": "Dr. Rahman", "chapter": "Ch2"}
	//   book:     {"author": "Cormen", "publisher": "MIT Press", "edition": "3rd", "isbn": "..."}
	//   question: {"exam_type": "final", "has_answers": true, "marks": 80, "duration": "3h"}
	//   syllabus: {"academic_year": "2024-2025", "credit_hours": 3.0, "effective_from": "2024-01"}
	Metadata *datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"`

	// Complex Metadata (kept for backward compat with Years in question banks)
	Years   *datatypes.JSON `gorm:"type:jsonb" json:"years,omitempty"` // For Questions e.g. [2016, 2017]
	BatchIDs []string `gorm:"-" json:"batch_ids,omitempty"`
	Batches  []Batch  `gorm:"many2many:resource_batches;save_associations:false" json:"batches,omitempty"`

	// Authors credits the people who made it (the uploader is CreatedByID).
	// On update, nil means "not sent, keep"; an empty list clears them.
	Authors []ResourceAuthor `gorm:"foreignKey:ResourceID;save_associations:false" json:"authors,omitempty"`

	// Notify controls whether creating/publishing this resource fans out a
	// notification to its selected batches. Defaults to true when omitted
	// (opt-out, not opt-in) — nil vs explicit false is why this is a pointer.
	Notify *bool `gorm:"-" json:"notify,omitempty"`
}

// ResetForBind clears Authors before an update binds the request body, so an
// omitted "authors" stays nil (keep) and decoded authors don't inherit fields
// from the stored rows encoding/json reuses.
func (r *Resource) ResetForBind() { r.Authors = nil }

// Author roles: whose class it came from, who wrote it, or the textbook it
// summarises.
const (
	AuthorRoleLecture = "lecture"
	AuthorRoleWriter  = "writer"
	AuthorRoleSource  = "source"
)

// ResourceAuthor is one credited person (or textbook) of a resource. It links
// to a teacher or student profile when known; Name is always set, so people
// who aren't in the lists (former teachers, old seniors) are still credited.
type ResourceAuthor struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	ResourceID uuid.UUID  `gorm:"type:uuid;not null;index" json:"resource_id"`
	Position   int        `gorm:"not null;default:0" json:"position"`
	Role       string     `gorm:"size:20;not null" json:"role"`
	TeacherID  *uuid.UUID `gorm:"type:uuid;index" json:"teacher_id,omitempty"`
	StudentID  *uuid.UUID `gorm:"type:uuid;index" json:"student_id,omitempty"`
	Name       string     `gorm:"size:150;not null" json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
}

// MaxResourceAuthors caps the credits on one resource.
const MaxResourceAuthors = 10

// Validate checks the authors sent with a create or update. Names are filled
// from the linked profile when saving, so a linked author may omit it.
func (r *Resource) Validate() error {
	if len(r.Authors) > MaxResourceAuthors {
		return fmt.Errorf("at most %d authors", MaxResourceAuthors)
	}
	for i := range r.Authors {
		a := &r.Authors[i]
		a.Name = strings.TrimSpace(a.Name)
		switch a.Role {
		case AuthorRoleLecture, AuthorRoleWriter, AuthorRoleSource:
		default:
			return fmt.Errorf("author %d: role must be lecture, writer or source", i+1)
		}
		if a.TeacherID != nil && a.StudentID != nil {
			return fmt.Errorf("author %d: link a teacher or a student, not both", i+1)
		}
		if a.Role == AuthorRoleSource && (a.TeacherID != nil || a.StudentID != nil) {
			return fmt.Errorf("author %d: a source is a book name, not a profile", i+1)
		}
		if a.TeacherID == nil && a.StudentID == nil && a.Name == "" {
			return fmt.Errorf("author %d: name is required", i+1)
		}
		if len(a.Name) > 150 {
			return fmt.Errorf("author %d: name is too long", i+1)
		}
	}
	return nil
}

func (a *ResourceAuthor) BeforeCreate(_ *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// ResourceRating is one user's 1-5 star rating of a Resource. Composite PK
// (same shape as CommunityPostLike) so a re-rate upserts in place instead of
// accumulating duplicate rows — Resource.RatingAvg/RatingCount are recomputed
// from this table on every write.
type ResourceRating struct {
	ResourceID uuid.UUID `gorm:"type:uuid;primaryKey" json:"resource_id"`
	UserID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id"`
	Rating     int       `gorm:"not null" json:"rating"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
