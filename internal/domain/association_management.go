package domain

import (
	"context"

	"github.com/google/uuid"
)

const (
	AssociationManagerRoleOwner   = "owner"
	AssociationManagerRoleManager = "manager"
)

// AssociationManager grants a user permission to self-service manage an
// Association — mirrors ClubManager.
type AssociationManager struct {
	Base
	AssociationID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_association_manager_unique" json:"association_id"`
	UserID        uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_association_manager_unique" json:"user_id"`
	Role          string    `gorm:"size:20;not null" json:"role"` // "owner" | "manager"
}

// AssociationPost is the Notifications-tab feed content for an association —
// mirrors ClubPost.
type AssociationPost struct {
	Base
	AssociationID uuid.UUID `gorm:"type:uuid;not null;index" json:"association_id"`
	AuthorID      uuid.UUID `gorm:"type:uuid;not null" json:"author_id"`
	Title         string    `gorm:"size:255;not null" json:"title"`
	Body          string    `gorm:"type:text" json:"body"`
	ImageURL      string    `gorm:"size:500" json:"image_url"`
}

// AssociationManagementRepository backs the JWT-protected "/my/associations"
// self-service surface — mirrors ClubManagementRepository.
type AssociationManagementRepository interface {
	GetMyAssociations(ctx context.Context, userID uuid.UUID) ([]Association, error)
	GetManagerRole(ctx context.Context, associationID, userID uuid.UUID) (string, error)
	UpdateMyAssociation(ctx context.Context, a *Association) error
	ListManagers(ctx context.Context, associationID uuid.UUID) ([]AssociationUserSummary, error)
	PromoteManager(ctx context.Context, associationID, targetUserID uuid.UUID, role string) error
	RemoveManager(ctx context.Context, associationID, targetUserID uuid.UUID) error
	CreateAssociationPost(ctx context.Context, post *AssociationPost) error
	GetPublicAssociationPosts(ctx context.Context, associationID uuid.UUID) ([]AssociationPost, error)
}
