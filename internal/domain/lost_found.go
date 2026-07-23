package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type LostFoundType string

const (
	LostFoundTypeLost  LostFoundType = "lost"
	LostFoundTypeFound LostFoundType = "found"
)

type LostFoundStatus string

const (
	LostFoundStatusOpen     LostFoundStatus = "open"
	LostFoundStatusClaimed  LostFoundStatus = "claimed"
	LostFoundStatusResolved LostFoundStatus = "resolved"
	LostFoundStatusRemoved  LostFoundStatus = "removed"
)

type LostFoundClaimStatus string

const (
	LostFoundClaimPending  LostFoundClaimStatus = "pending"
	LostFoundClaimAccepted LostFoundClaimStatus = "accepted"
	LostFoundClaimRejected LostFoundClaimStatus = "rejected"
)

type LostFoundReportStatus string

const (
	LostFoundReportPending   LostFoundReportStatus = "pending"
	LostFoundReportResolved  LostFoundReportStatus = "resolved"
	LostFoundReportDismissed LostFoundReportStatus = "dismissed"
)

// LostFoundCategory groups items in the Lost & Found Portal (e.g. Electronics,
// ID Cards & Documents, Keys, Bags & Wallets). Managed via generic CRUD, same
// pattern as MarketplaceCategory.
type LostFoundCategory struct {
	Base
	Name    string `gorm:"size:100;not null" json:"name"`
	IconKey string `gorm:"size:100" json:"icon_key"`
	Index   int    `gorm:"default:0" json:"index"`
}

// LostFoundItem is a single lost-or-found report posted by a student. Targets
// scope visibility to a university/department, same pattern as Product/Skill.
// A zero-target item is global (visible campus-wide across the platform).
type LostFoundItem struct {
	Base
	Type        LostFoundType     `gorm:"type:varchar(10);not null;index" json:"type"`
	Title       string            `gorm:"size:255;not null" json:"title"`
	Description string            `gorm:"type:text" json:"description"`
	CategoryID  uuid.UUID         `gorm:"type:uuid;index" json:"category_id"` // uuid.Nil = uncategorized
	Category    LostFoundCategory `gorm:"foreignKey:CategoryID;references:ID" json:"category,omitempty"`
	ImageURLs   pq.StringArray    `gorm:"type:text[];default:'{}'" json:"image_urls"`
	Location    string            `gorm:"size:255" json:"location"`
	EventDate   *time.Time        `gorm:"type:date" json:"event_date,omitempty"`

	Status        LostFoundStatus `gorm:"type:varchar(20);default:'open';index" json:"status"`
	RemovalReason string          `gorm:"type:text" json:"removal_reason,omitempty"`
	ResolvedAt    *time.Time      `json:"resolved_at,omitempty"`

	PosterID uuid.UUID `gorm:"type:uuid;not null;index" json:"poster_id"`
	Poster   *User     `gorm:"foreignKey:PosterID" json:"poster,omitempty"`

	Targets []LostFoundItemTarget `gorm:"foreignKey:ItemID;constraint:OnDelete:CASCADE" json:"targets"`

	// Client-only field
	ClaimsCount int `gorm:"-" json:"claims_count,omitempty"`
}

// LostFoundItemTarget links an item to a specific university or department,
// mirrors ProductTarget. DepartmentID uuid.Nil means "whole university".
type LostFoundItemTarget struct {
	Base
	ItemID       uuid.UUID `gorm:"type:uuid;not null;index" json:"item_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}

// LostFoundClaim is a "this is mine" / "I found this" submission from another
// user against an open item. Only the poster can accept/reject.
type LostFoundClaim struct {
	Base
	ItemID    uuid.UUID             `gorm:"type:uuid;not null;index" json:"item_id"`
	ClaimerID uuid.UUID             `gorm:"type:uuid;not null;index" json:"claimer_id"`
	Claimer   *User                 `gorm:"foreignKey:ClaimerID" json:"claimer,omitempty"`
	Message   string                `gorm:"type:text" json:"message"`
	Status    LostFoundClaimStatus  `gorm:"type:varchar(20);default:'pending';index" json:"status"`
}

// LostFoundReport flags an item as spam/abuse/fraud for admin review.
type LostFoundReport struct {
	Base
	ItemID     uuid.UUID             `gorm:"type:uuid;not null;index" json:"item_id"`
	Item       *LostFoundItem        `gorm:"foreignKey:ItemID" json:"item,omitempty"`
	ReporterID uuid.UUID             `gorm:"type:uuid;not null;index" json:"reporter_id"`
	Reporter   *User                 `gorm:"foreignKey:ReporterID" json:"reporter,omitempty"`
	Reason     string                `gorm:"type:text;not null" json:"reason"`
	Status     LostFoundReportStatus `gorm:"type:varchar(20);default:'pending';index" json:"status"`
}

// LostFoundFilter narrows the admin listing.
type LostFoundFilter struct {
	Status     LostFoundStatus
	Type       LostFoundType
	CategoryID uuid.UUID
	Search     string
	Limit      int
	Offset     int
}

// LostFoundRepository is dedicated (not generic CRUD) because Targets need
// explicit delete-then-save handling on update, status is a state machine,
// and claims/reports are sub-resources, same reasoning as ProductRepository.
type LostFoundRepository interface {
	GetAllItems(ctx context.Context, filter LostFoundFilter) ([]LostFoundItem, int64, error)
	GetItemByID(ctx context.Context, id uuid.UUID) (*LostFoundItem, error)
	CreateItem(ctx context.Context, item *LostFoundItem) error
	UpdateItem(ctx context.Context, item *LostFoundItem) error
	SetItemStatus(ctx context.Context, id uuid.UUID, status LostFoundStatus, removalReason string) error
	DeleteItem(ctx context.Context, id uuid.UUID) error
	GetItemsByPoster(ctx context.Context, posterID uuid.UUID) ([]LostFoundItem, error)

	// GetItemsByLocation returns open items that are either global (no
	// targets) or targeted to this university/department. Optional
	// itemType/categoryID/search narrow the results further.
	GetItemsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID, itemType, search string) ([]LostFoundItem, error)

	CreateClaim(ctx context.Context, claim *LostFoundClaim) error
	GetClaimByID(ctx context.Context, id uuid.UUID) (*LostFoundClaim, error)
	GetClaimsByItem(ctx context.Context, itemID uuid.UUID) ([]LostFoundClaim, error)
	SetClaimStatus(ctx context.Context, id uuid.UUID, status LostFoundClaimStatus) error

	CreateReport(ctx context.Context, report *LostFoundReport) error
	GetAllReports(ctx context.Context, status LostFoundReportStatus) ([]LostFoundReport, error)
	SetReportStatus(ctx context.Context, id uuid.UUID, status LostFoundReportStatus) error
}
