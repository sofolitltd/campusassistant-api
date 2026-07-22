package domain

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Club represents a student club or organization within a university.
type Club struct {
	Base
	Name           string         `gorm:"size:255;not null" json:"name"`
	Description    string         `gorm:"type:text" json:"description"`
	ClubType       string         `gorm:"size:50;not null;index" json:"club_type"`
	UniversityID   uuid.UUID      `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID   *uuid.UUID     `gorm:"type:uuid;index" json:"department_id,omitempty"`
	LogoURL        *string        `gorm:"size:500" json:"logo_url,omitempty"`
	BannerURL      *string        `gorm:"size:500" json:"banner_url,omitempty"`
	FoundedYear    *int           `json:"founded_year,omitempty"`
	IsActive       bool           `gorm:"default:true" json:"is_active"`
	SocialLinks    datatypes.JSON `gorm:"type:jsonb" json:"social_links,omitempty"`
	ContactEmail   *string        `gorm:"size:255" json:"contact_email,omitempty"`
	ContactPhone   *string        `gorm:"size:50" json:"contact_phone,omitempty"`
	FollowersCount int            `gorm:"default:0" json:"followers_count"`
	// MembersCount mirrors FollowersCount but for the formal Join roster
	// (ClubMember), a separate relation from following.
	MembersCount int `gorm:"default:0" json:"members_count"`
	// Category is a free string validated against a fixed list at the
	// handler/admin-UI level (Academic, Cultural, Sports, Technology, Arts,
	// Social Service, Debate, Other) — same convention as ClubType.
	Category string `gorm:"size:50;index" json:"category"`
	// IsVerified is a distinct signal from IsActive: Active = visible at
	// all, Verified = "official" badge. A self-suggested club (see
	// SuggestClub) always starts unverified even after an admin activates
	// it — verification is a deliberate second admin action.
	IsVerified bool `gorm:"default:false" json:"is_verified"`

	// IsFollowing/IsMember are populated per-request for the requesting user
	// only — never persisted (see ClubFollow/ClubMember for the relations).
	IsFollowing bool `gorm:"-" json:"is_following,omitempty"`
	IsMember    bool `gorm:"-" json:"is_member,omitempty"`
}

// ClubFollow records that a user follows/is interested in a Club. Mirrors
// NoticeLike's shape — composite primary key, no Base, denormalized count
// kept on the parent row (Club.FollowersCount) for cheap reads.
type ClubFollow struct {
	ClubID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID uuid.UUID `gorm:"type:uuid;primaryKey"`
}

// ClubMember records that a user has formally joined a Club — distinct
// from ClubFollow (a lighter "notify me" relation). Same shape/denormalized
// counter pattern, kept as a separate table since the two are semantically
// different rosters shown in different UI (Members tab vs. the Follow
// button), per product decision.
type ClubMember struct {
	ClubID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID uuid.UUID `gorm:"type:uuid;primaryKey"`
}

// ClubFilters narrows GetAllClubs. Zero-value fields are not applied.
type ClubFilters struct {
	UniversityID *uuid.UUID
	DepartmentID *uuid.UUID
	ClubType     string
	Category     string
	// ActiveOnly restricts results to IsActive clubs — always true for the
	// public/app-facing listing, left false for admin so pending-approval
	// (suggested, not yet activated) clubs are visible for review.
	ActiveOnly bool
	// RequestingUserID, if set, populates IsFollowing per returned club.
	RequestingUserID *uuid.UUID
}

// ClubRepository is a dedicated (non-generic) repository because Club needs
// a denormalized follower count and per-request IsFollowing state that the
// generic GormRepository[T] has no way to compute.
type ClubRepository interface {
	GetAllClubs(ctx context.Context, filters ClubFilters) ([]Club, error)
	GetClubByID(ctx context.Context, id uuid.UUID, requestingUserID *uuid.UUID) (*Club, error)
	CreateClub(ctx context.Context, club *Club) error
	UpdateClub(ctx context.Context, club *Club) error
	DeleteClub(ctx context.Context, id uuid.UUID) error
	FollowClub(ctx context.Context, clubID, userID uuid.UUID) error
	UnfollowClub(ctx context.Context, clubID, userID uuid.UUID) error
	// JoinClub/LeaveClub manage the separate, formal ClubMember roster —
	// independent of Follow/Unfollow.
	JoinClub(ctx context.Context, clubID, userID uuid.UUID) error
	LeaveClub(ctx context.Context, clubID, userID uuid.UUID) error
	GetPublicClubMembers(ctx context.Context, clubID uuid.UUID) ([]ClubUserSummary, error)
	GetPublicClubFollowers(ctx context.Context, clubID uuid.UUID) ([]ClubUserSummary, error)
	// SuggestClub creates a club with IsActive forced to false, regardless
	// of what the caller passed — an admin must review and activate it.
	// It also seeds a ClubManager{Role:"owner"} row for creatorID in the
	// same transaction, so the requester can manage the club immediately
	// while it's pending review.
	SuggestClub(ctx context.Context, club *Club, creatorID uuid.UUID) error
}

// ClubUserSummary is a lightweight user projection for club rosters
// (Members tab, follower list for promotion) — avoids pulling the full
// User model just to show an avatar/name.
type ClubUserSummary struct {
	UserID    uuid.UUID `json:"user_id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	AvatarURL string    `json:"avatar_url"`
	// Role is only populated by ListManagers (empty for follower/member
	// listings).
	Role string `json:"role,omitempty"`
}
