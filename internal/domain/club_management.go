package domain

import (
	"context"

	"github.com/google/uuid"
)

const (
	ClubManagerRoleOwner   = "owner"
	ClubManagerRoleManager = "manager"
)

// ClubManager grants a user permission to self-service manage a Club
// (edit info, post events/updates, promote other managers) without
// needing admin-panel access. Modeled after ClubFollow's join-table shape
// with a Role column added — this is the first "multiple co-owners of one
// resource" concept in this codebase; nothing else needed it before Club.
type ClubManager struct {
	Base
	ClubID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_club_manager_unique" json:"club_id"`
	UserID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_club_manager_unique" json:"user_id"`
	Role   string    `gorm:"size:20;not null" json:"role"` // "owner" | "manager"
}

// ClubPost is the Notifications-tab feed content — a club's own
// independent updates/announcements, distinct from calendared ClubEvents.
// Posting one always notifies the club's followers (see
// handler.notifyClubFollowers).
type ClubPost struct {
	Base
	ClubID   uuid.UUID `gorm:"type:uuid;not null;index" json:"club_id"`
	AuthorID uuid.UUID `gorm:"type:uuid;not null" json:"author_id"`
	Title    string    `gorm:"size:255;not null" json:"title"`
	Body     string    `gorm:"type:text" json:"body"`
	ImageURL string    `gorm:"size:500" json:"image_url"`
}

// ClubManagementRepository backs the JWT-protected "/my/clubs" self-service
// surface — kept separate from ClubRepository because it's a distinct
// concern (manager-role-gated mutation) from public club reads and the
// simple Follow/Join relations.
type ClubManagementRepository interface {
	// GetMyClubs returns every club where userID is the creator or holds a
	// ClubManager row, regardless of the club's IsActive status.
	GetMyClubs(ctx context.Context, userID uuid.UUID) ([]Club, error)
	// GetManagerRole returns "" (not an error) if userID has no ClubManager
	// row for clubID.
	GetManagerRole(ctx context.Context, clubID, userID uuid.UUID) (string, error)
	// UpdateMyClub saves club as-is — the handler is responsible for only
	// binding the allowed content-field whitelist onto it beforehand.
	UpdateMyClub(ctx context.Context, club *Club) error
	ListManagers(ctx context.Context, clubID uuid.UUID) ([]ClubUserSummary, error)
	// PromoteManager 400s at the handler level if targetUserID isn't
	// currently a ClubFollow of clubID — this method assumes that's
	// already validated.
	PromoteManager(ctx context.Context, clubID, targetUserID uuid.UUID, role string) error
	RemoveManager(ctx context.Context, clubID, targetUserID uuid.UUID) error
	CreateClubPost(ctx context.Context, post *ClubPost) error
	GetPublicClubPosts(ctx context.Context, clubID uuid.UUID) ([]ClubPost, error)
}
