package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type associationRepository struct {
	db *gorm.DB
}

func NewAssociationRepository(db *gorm.DB) domain.AssociationRepository {
	return &associationRepository{db: db}
}

func (r *associationRepository) GetAllAssociations(ctx context.Context, filters domain.AssociationFilters) ([]domain.Association, error) {
	var associations []domain.Association
	q := r.db.WithContext(ctx).Model(&domain.Association{})
	if filters.UniversityID != nil {
		q = q.Where("university_id = ?", *filters.UniversityID)
	}
	if filters.AssociationType != "" {
		q = q.Where("association_type = ?", filters.AssociationType)
	}
	if filters.DistrictID != "" {
		q = q.Where("district_id = ?", filters.DistrictID)
	}
	if filters.SubDistrictID != "" {
		q = q.Where("sub_district_id = ?", filters.SubDistrictID)
	}
	if filters.Category != "" {
		q = q.Where("category = ?", filters.Category)
	}
	if filters.ActiveOnly {
		q = q.Where("is_active = ?", true)
	}
	if err := q.Order("created_at desc").Find(&associations).Error; err != nil {
		return nil, err
	}

	if filters.RequestingUserID != nil && len(associations) > 0 {
		ids := make([]uuid.UUID, len(associations))
		for i, a := range associations {
			ids[i] = a.ID
		}
		var followedIDs []uuid.UUID
		if err := r.db.WithContext(ctx).Table("association_follows").
			Where("user_id = ? AND association_id IN ?", *filters.RequestingUserID, ids).
			Pluck("association_id", &followedIDs).Error; err != nil {
			return nil, err
		}
		followed := make(map[uuid.UUID]bool, len(followedIDs))
		for _, id := range followedIDs {
			followed[id] = true
		}
		var memberIDs []uuid.UUID
		if err := r.db.WithContext(ctx).Table("association_members").
			Where("user_id = ? AND association_id IN ?", *filters.RequestingUserID, ids).
			Pluck("association_id", &memberIDs).Error; err != nil {
			return nil, err
		}
		isMember := make(map[uuid.UUID]bool, len(memberIDs))
		for _, id := range memberIDs {
			isMember[id] = true
		}
		for i := range associations {
			associations[i].IsFollowing = followed[associations[i].ID]
			associations[i].IsMember = isMember[associations[i].ID]
		}
	}

	return associations, nil
}

func (r *associationRepository) GetAssociationByID(ctx context.Context, id uuid.UUID, requestingUserID *uuid.UUID) (*domain.Association, error) {
	var association domain.Association
	if err := r.db.WithContext(ctx).First(&association, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if requestingUserID != nil {
		var count int64
		if err := r.db.WithContext(ctx).Table("association_follows").
			Where("association_id = ? AND user_id = ?", id, *requestingUserID).
			Count(&count).Error; err != nil {
			return nil, err
		}
		association.IsFollowing = count > 0
		var memberCount int64
		if err := r.db.WithContext(ctx).Table("association_members").
			Where("association_id = ? AND user_id = ?", id, *requestingUserID).
			Count(&memberCount).Error; err != nil {
			return nil, err
		}
		association.IsMember = memberCount > 0
	}
	return &association, nil
}

func (r *associationRepository) CreateAssociation(ctx context.Context, association *domain.Association) error {
	return r.db.WithContext(ctx).Create(association).Error
}

func (r *associationRepository) UpdateAssociation(ctx context.Context, association *domain.Association) error {
	return r.db.WithContext(ctx).Save(association).Error
}

func (r *associationRepository) DeleteAssociation(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Association{}, "id = ?", id).Error
}

// FollowAssociation is idempotent — mirrors ClubRepository.FollowClub.
func (r *associationRepository) FollowAssociation(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		follow := domain.AssociationFollow{AssociationID: associationID, UserID: userID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&follow)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // already following
		}
		return tx.Model(&domain.Association{}).Where("id = ?", associationID).
			UpdateColumn("followers_count", gorm.Expr("followers_count + ?", 1)).Error
	})
}

func (r *associationRepository) UnfollowAssociation(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&domain.AssociationFollow{}, "association_id = ? AND user_id = ?", associationID, userID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // wasn't following
		}
		return tx.Model(&domain.Association{}).Where("id = ? AND followers_count > 0", associationID).
			UpdateColumn("followers_count", gorm.Expr("followers_count - ?", 1)).Error
	})
}

// JoinAssociation is idempotent, writes to the separate association_members
// table/counter — mirrors ClubRepository.JoinClub.
func (r *associationRepository) JoinAssociation(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		member := domain.AssociationMember{AssociationID: associationID, UserID: userID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&member)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // already a member
		}
		return tx.Model(&domain.Association{}).Where("id = ?", associationID).
			UpdateColumn("members_count", gorm.Expr("members_count + ?", 1)).Error
	})
}

func (r *associationRepository) LeaveAssociation(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&domain.AssociationMember{}, "association_id = ? AND user_id = ?", associationID, userID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // wasn't a member
		}
		return tx.Model(&domain.Association{}).Where("id = ? AND members_count > 0", associationID).
			UpdateColumn("members_count", gorm.Expr("members_count - ?", 1)).Error
	})
}

func (r *associationRepository) GetPublicAssociationMembers(ctx context.Context, associationID uuid.UUID) ([]domain.AssociationUserSummary, error) {
	var summaries []domain.AssociationUserSummary
	err := r.db.WithContext(ctx).Table("association_members").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url").
		Joins("JOIN users ON users.id = association_members.user_id").
		Where("association_members.association_id = ?", associationID).
		Scan(&summaries).Error
	return summaries, err
}

func (r *associationRepository) GetPublicAssociationFollowers(ctx context.Context, associationID uuid.UUID) ([]domain.AssociationUserSummary, error) {
	var summaries []domain.AssociationUserSummary
	err := r.db.WithContext(ctx).Table("association_follows").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url").
		Joins("JOIN users ON users.id = association_follows.user_id").
		Where("association_follows.association_id = ?", associationID).
		Scan(&summaries).Error
	return summaries, err
}

// SuggestAssociation forces IsActive false and seeds an
// AssociationManager{Role: owner} row for creatorID — mirrors
// ClubRepository.SuggestClub.
func (r *associationRepository) SuggestAssociation(ctx context.Context, association *domain.Association, creatorID uuid.UUID) error {
	association.IsActive = false
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(association).Error; err != nil {
			return err
		}
		manager := domain.AssociationManager{AssociationID: association.ID, UserID: creatorID, Role: domain.AssociationManagerRoleOwner}
		manager.CreatedByID = creatorID
		manager.UpdatedByID = creatorID
		return tx.Create(&manager).Error
	})
}
