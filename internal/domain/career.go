package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type CareerJobStatus string

const (
	CareerJobPending   CareerJobStatus = "pending"
	CareerJobApplied   CareerJobStatus = "applied"
	CareerJobCompleted CareerJobStatus = "completed"
)

// CareerJobScope controls whether a student's personal Job entry is shared
// with peers. Mirrors CommunityPost's PostScope: a job stores a snapshot of
// the poster's own affiliation at creation time, and viewers pick a scope
// tab whose query matches their own affiliation against that snapshot.
type CareerJobScope string

const (
	CareerJobScopePrivate    CareerJobScope = "private"
	CareerJobScopeBatch      CareerJobScope = "batch"
	CareerJobScopeDepartment CareerJobScope = "department"
	CareerJobScopeUniversity CareerJobScope = "university"
)

type CareerReminderStatus string

const (
	CareerReminderPending CareerReminderStatus = "pending"
	CareerReminderClaimed CareerReminderStatus = "claimed"
	CareerReminderSent    CareerReminderStatus = "sent"
	CareerReminderCancelled CareerReminderStatus = "cancelled"
)

// CareerCircularCategory groups circulars (e.g. Government Job, Bank Job,
// University Recruitment, Scholarship, Exam Notice). Managed via generic
// CRUD, same pattern as LostFoundCategory.
type CareerCircularCategory struct {
	Base
	Name    string `gorm:"size:100;not null" json:"name"`
	IconKey string `gorm:"size:100" json:"icon_key"`
	Index   int    `gorm:"default:0" json:"index"`
}

// CareerCircular is an admin-authored posting (job/exam/scholarship
// circular) — students browse it and optionally "pull" it into their own
// CareerJob tracker. Targets scope visibility to a university/department,
// same pattern as Product/LostFoundItem; zero targets = global.
type CareerCircular struct {
	Base
	Title          string                 `gorm:"size:255;not null" json:"title"`
	Organization   string                 `gorm:"size:255" json:"organization"`
	CategoryID     uuid.UUID              `gorm:"type:uuid;index" json:"category_id"` // uuid.Nil = uncategorized
	Category       CareerCircularCategory `gorm:"foreignKey:CategoryID;references:ID" json:"category,omitempty"`
	Description    string                 `gorm:"type:text" json:"description"`
	AttachmentURLs pq.StringArray         `gorm:"type:text[];default:'{}'" json:"attachment_urls"`
	PostLink       string                 `gorm:"size:500" json:"post_link"`
	ResourceLink   string                 `gorm:"size:500" json:"resource_link"`
	PublishDate    *time.Time             `gorm:"type:date" json:"publish_date,omitempty"`
	DeadlineDate   *time.Time             `json:"deadline_date,omitempty"`
	ViewsCount     int                    `gorm:"default:0" json:"views_count"`
	IsPublished    bool                   `gorm:"default:false;index" json:"is_published"`
	Targets        []CareerCircularTarget `gorm:"foreignKey:CircularID;constraint:OnDelete:CASCADE" json:"targets"`
}

type CareerCircularTarget struct {
	Base
	CircularID   uuid.UUID `gorm:"type:uuid;not null;index" json:"circular_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}

// CareerJob is a student's personal application tracker entry. It's a
// denormalized copy of a CareerCircular's fields at save time (CircularID
// is just a provenance pointer) so editing/deleting the source circular
// never mutates a student's own saved record — same reasoning as copying
// image URLs onto a Product rather than joining live.
type CareerJob struct {
	Base
	UserID         uuid.UUID       `gorm:"type:uuid;not null;index" json:"user_id"`
	CircularID     *uuid.UUID      `gorm:"type:uuid;index" json:"circular_id,omitempty"`
	Title          string          `gorm:"size:255;not null" json:"title"`
	Organization   string          `gorm:"size:255" json:"organization"`
	PostLink       string          `gorm:"size:500" json:"post_link"`
	ResourceLink   string          `gorm:"size:500" json:"resource_link"`
	AttachmentURLs pq.StringArray  `gorm:"type:text[];default:'{}'" json:"attachment_urls"`
	PublishDate    *time.Time      `gorm:"type:date" json:"publish_date,omitempty"`
	DeadlineDate   *time.Time      `json:"deadline_date,omitempty"`
	Status         CareerJobStatus `gorm:"type:varchar(20);default:'pending';index" json:"status"`
	Notes          string          `gorm:"type:text" json:"notes"`

	// Scope + snapshot fields: opt-in peer sharing. Scope defaults to
	// "private" (never shown to anyone else). If the poster shares with
	// batch/department/university, these three are stamped from the
	// poster's own Student record at creation time and never touched
	// again — same reasoning as CommunityPost's UniversityID/DepartmentID/
	// BatchID snapshot.
	Scope        CareerJobScope `gorm:"type:varchar(20);default:'private';index" json:"scope"`
	UniversityID *uuid.UUID     `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID     `gorm:"type:uuid;index" json:"department_id,omitempty"`
	BatchID      *uuid.UUID     `gorm:"type:uuid;index" json:"batch_id,omitempty"`
	Poster       *User          `gorm:"foreignKey:UserID" json:"poster,omitempty"`
}

// CareerReminder is a scheduled push notification for a student, optionally
// tied to a CareerJob. Delivery is server-driven (a ticking scheduler claims
// due rows and pushes via FCM) rather than a client-scheduled local alarm.
type CareerReminder struct {
	Base
	UserID   uuid.UUID            `gorm:"type:uuid;not null;index" json:"user_id"`
	JobID    *uuid.UUID           `gorm:"type:uuid;index" json:"job_id,omitempty"`
	Title    string               `gorm:"size:255;not null" json:"title"`
	RemindAt time.Time            `gorm:"index;not null" json:"remind_at"`
	Status   CareerReminderStatus `gorm:"type:varchar(20);default:'pending';index" json:"status"`
	SentAt   *time.Time           `json:"sent_at,omitempty"`
}

type CareerCircularFilter struct {
	CategoryID  uuid.UUID
	IsPublished *bool
	Search      string
	Limit       int
	Offset      int
}

// CareerRepository is dedicated (not generic CRUD) because Targets need
// explicit delete-then-save handling on update, jobs/reminders are
// ownership-scoped sub-resources, and reminder delivery needs an atomic
// claim query — same reasoning as LostFoundRepository.
type CareerRepository interface {
	GetAllCirculars(ctx context.Context, filter CareerCircularFilter) ([]CareerCircular, int64, error)
	GetCircularByID(ctx context.Context, id uuid.UUID) (*CareerCircular, error)
	CreateCircular(ctx context.Context, circular *CareerCircular) error
	UpdateCircular(ctx context.Context, circular *CareerCircular) error
	DeleteCircular(ctx context.Context, id uuid.UUID) error
	IncrementCircularViews(ctx context.Context, id uuid.UUID) error

	// GetCircularsByLocation returns published circulars that are either
	// global (no targets) or targeted to this university/department.
	GetCircularsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID, search string) ([]CareerCircular, error)

	GetMyJobs(ctx context.Context, userID uuid.UUID) ([]CareerJob, error)
	GetJobByID(ctx context.Context, id uuid.UUID) (*CareerJob, error)
	CreateJob(ctx context.Context, job *CareerJob) error
	UpdateJob(ctx context.Context, job *CareerJob) error
	SetJobStatus(ctx context.Context, id uuid.UUID, status CareerJobStatus) error
	DeleteJob(ctx context.Context, id uuid.UUID) error

	// GetSharedJobsByScope returns other students' Jobs shared at the given
	// scope, matching the viewer's own batch/department/university against
	// the snapshot stamped on each job at creation time — same matching
	// approach as CommunityRepository.GetPosts.
	GetSharedJobsByScope(ctx context.Context, scope CareerJobScope, viewer CommunityViewer) ([]CareerJob, error)

	CreateReminder(ctx context.Context, reminder *CareerReminder) error
	GetReminderByID(ctx context.Context, id uuid.UUID) (*CareerReminder, error)
	GetMyReminders(ctx context.Context, userID uuid.UUID) ([]CareerReminder, error)
	CancelReminder(ctx context.Context, id uuid.UUID) error

	// ClaimDueReminders atomically flips up to limit pending rows with
	// remind_at <= before to "claimed" and returns them, so the scheduler
	// is safe to run from more than one API replica without double-sending.
	ClaimDueReminders(ctx context.Context, before time.Time, limit int) ([]CareerReminder, error)
	MarkReminderSent(ctx context.Context, id uuid.UUID) error
}
