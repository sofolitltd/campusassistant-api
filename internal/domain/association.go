package domain

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Association represents a district or sub-district (upazila) based student
// association within a university — the regional counterpart to Club, which
// is scoped to University/Department instead. Structurally a near-exact
// clone of Club; see club.go for the pattern this mirrors.
type Association struct {
	Base
	Name            string         `gorm:"size:255;not null" json:"name"`
	Description     string         `gorm:"type:text" json:"description"`
	AssociationType string         `gorm:"size:50;not null;index" json:"association_type"`
	UniversityID    uuid.UUID      `gorm:"type:uuid;not null;index" json:"university_id"`
	// DistrictID/SubDistrictID reference domain.BDDistricts by ID (a stable
	// slug, not a uuid) — there is no districts DB table, so the name is
	// denormalized onto the row for cheap reads.
	DistrictID      string         `gorm:"size:100;not null;index" json:"district_id"`
	DistrictName    string         `gorm:"size:100;not null" json:"district_name"`
	SubDistrictID   *string        `gorm:"size:100;index" json:"sub_district_id,omitempty"`
	SubDistrictName *string        `gorm:"size:100" json:"sub_district_name,omitempty"`
	LogoURL         *string        `gorm:"size:500" json:"logo_url,omitempty"`
	BannerURL       *string        `gorm:"size:500" json:"banner_url,omitempty"`
	FoundedYear     *int           `json:"founded_year,omitempty"`
	IsActive        bool           `gorm:"default:true" json:"is_active"`
	SocialLinks     datatypes.JSON `gorm:"type:jsonb" json:"social_links,omitempty"`
	ContactEmail    *string        `gorm:"size:255" json:"contact_email,omitempty"`
	ContactPhone    *string        `gorm:"size:50" json:"contact_phone,omitempty"`
	FollowersCount  int            `gorm:"default:0" json:"followers_count"`
	MembersCount    int            `gorm:"default:0" json:"members_count"`
	Category        string         `gorm:"size:50;index" json:"category"`
	IsVerified      bool           `gorm:"default:false" json:"is_verified"`

	IsFollowing bool `gorm:"-" json:"is_following,omitempty"`
	IsMember    bool `gorm:"-" json:"is_member,omitempty"`
}

// AssociationFollow records that a user follows/is interested in an
// Association. Mirrors ClubFollow's shape.
type AssociationFollow struct {
	AssociationID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID        uuid.UUID `gorm:"type:uuid;primaryKey"`
}

// AssociationMember records that a user has formally joined an Association —
// distinct from AssociationFollow, mirrors ClubMember.
type AssociationMember struct {
	AssociationID uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID        uuid.UUID `gorm:"type:uuid;primaryKey"`
}

// AssociationFilters narrows GetAllAssociations. Zero-value fields are not
// applied.
type AssociationFilters struct {
	UniversityID     *uuid.UUID
	AssociationType  string
	DistrictID       string
	SubDistrictID    string
	Category         string
	ActiveOnly       bool
	RequestingUserID *uuid.UUID
}

// AssociationRepository is dedicated (not generic) for the same reasons as
// ClubRepository — denormalized follower count, per-request IsFollowing.
type AssociationRepository interface {
	GetAllAssociations(ctx context.Context, filters AssociationFilters) ([]Association, error)
	GetAssociationByID(ctx context.Context, id uuid.UUID, requestingUserID *uuid.UUID) (*Association, error)
	CreateAssociation(ctx context.Context, a *Association) error
	UpdateAssociation(ctx context.Context, a *Association) error
	DeleteAssociation(ctx context.Context, id uuid.UUID) error
	FollowAssociation(ctx context.Context, associationID, userID uuid.UUID) error
	UnfollowAssociation(ctx context.Context, associationID, userID uuid.UUID) error
	JoinAssociation(ctx context.Context, associationID, userID uuid.UUID) error
	LeaveAssociation(ctx context.Context, associationID, userID uuid.UUID) error
	GetPublicAssociationMembers(ctx context.Context, associationID uuid.UUID) ([]AssociationUserSummary, error)
	GetPublicAssociationFollowers(ctx context.Context, associationID uuid.UUID) ([]AssociationUserSummary, error)
	SuggestAssociation(ctx context.Context, a *Association, creatorID uuid.UUID) error
}

// AssociationUserSummary is a lightweight user projection for association
// rosters — mirrors ClubUserSummary.
type AssociationUserSummary struct {
	UserID    uuid.UUID `json:"user_id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	AvatarURL string    `json:"avatar_url"`
	Role      string    `json:"role,omitempty"`
}
