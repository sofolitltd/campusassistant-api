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
			Where("user_id = ? AND association_id IN ? AND status = ?", *filters.RequestingUserID, ids, domain.MembershipStatusApproved).
			Pluck("association_id", &memberIDs).Error; err != nil {
			return nil, err
		}
		isMember := make(map[uuid.UUID]bool, len(memberIDs))
		for _, id := range memberIDs {
			isMember[id] = true
		}
		var pendingIDs []uuid.UUID
		if err := r.db.WithContext(ctx).Table("association_members").
			Where("user_id = ? AND association_id IN ? AND status = ?", *filters.RequestingUserID, ids, domain.MembershipStatusPending).
			Pluck("association_id", &pendingIDs).Error; err != nil {
			return nil, err
		}
		isPending := make(map[uuid.UUID]bool, len(pendingIDs))
		for _, id := range pendingIDs {
			isPending[id] = true
		}
		for i := range associations {
			associations[i].IsFollowing = followed[associations[i].ID]
			associations[i].IsMember = isMember[associations[i].ID]
			associations[i].IsPendingMember = isPending[associations[i].ID]
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
			Where("association_id = ? AND user_id = ? AND status = ?", id, *requestingUserID, domain.MembershipStatusApproved).
			Count(&memberCount).Error; err != nil {
			return nil, err
		}
		association.IsMember = memberCount > 0
		var pendingCount int64
		if err := r.db.WithContext(ctx).Table("association_members").
			Where("association_id = ? AND user_id = ? AND status = ?", id, *requestingUserID, domain.MembershipStatusPending).
			Count(&pendingCount).Error; err != nil {
			return nil, err
		}
		association.IsPendingMember = pendingCount > 0
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

// JoinAssociation is idempotent and creates a *pending* membership request —
// admin approval (ApproveAssociationMember) is what actually admits the user
// and bumps members_count. Mirrors the shape of ClubRepository.JoinClub but
// no longer auto-approves.
func (r *associationRepository) JoinAssociation(ctx context.Context, associationID, userID uuid.UUID) error {
	member := domain.AssociationMember{AssociationID: associationID, UserID: userID, Status: domain.MembershipStatusPending}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error
}

// LeaveAssociation deletes the caller's membership row regardless of its
// status — this lets an approved member actually leave *and* lets a
// requester cancel a still-pending join request, both via the same
// DELETE /associations/:id/join call. members_count is only decremented if
// the row being removed was an approved (counted) membership.
func (r *associationRepository) LeaveAssociation(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var member domain.AssociationMember
		err := tx.Where("association_id = ? AND user_id = ?", associationID, userID).First(&member).Error
		if err == gorm.ErrRecordNotFound {
			return nil // wasn't a member or pending requester
		}
		if err != nil {
			return err
		}
		if err := tx.Delete(&member).Error; err != nil {
			return err
		}
		if member.Status != domain.MembershipStatusApproved {
			return nil
		}
		return tx.Model(&domain.Association{}).Where("id = ? AND members_count > 0", associationID).
			UpdateColumn("members_count", gorm.Expr("members_count - ?", 1)).Error
	})
}

// ApproveAssociationMember admits a pending requester: flips their row to
// approved and bumps members_count (the increment JoinAssociation used to
// do before it started creating pending rows).
func (r *associationRepository) ApproveAssociationMember(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.AssociationMember{}).
			Where("association_id = ? AND user_id = ? AND status = ?", associationID, userID, domain.MembershipStatusPending).
			Update("status", domain.MembershipStatusApproved)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // no such pending request
		}
		return tx.Model(&domain.Association{}).Where("id = ?", associationID).
			UpdateColumn("members_count", gorm.Expr("members_count + ?", 1)).Error
	})
}

// RejectAssociationMember discards a pending request. No counter change —
// a pending row was never counted in members_count.
func (r *associationRepository) RejectAssociationMember(ctx context.Context, associationID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Delete(&domain.AssociationMember{}, "association_id = ? AND user_id = ? AND status = ?", associationID, userID, domain.MembershipStatusPending).Error
}

func (r *associationRepository) GetPublicAssociationMembers(ctx context.Context, associationID uuid.UUID) ([]domain.AssociationUserSummary, error) {
	var summaries []domain.AssociationUserSummary
	err := r.db.WithContext(ctx).Table("association_members").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url").
		Joins("JOIN users ON users.id = association_members.user_id").
		Where("association_members.association_id = ? AND association_members.status = ?", associationID, domain.MembershipStatusApproved).
		Scan(&summaries).Error
	return summaries, err
}

// GetPendingAssociationMembers powers the admin Members-tab approval queue —
// every user awaiting a decision on their join request.
func (r *associationRepository) GetPendingAssociationMembers(ctx context.Context, associationID uuid.UUID) ([]domain.AssociationUserSummary, error) {
	var summaries []domain.AssociationUserSummary
	err := r.db.WithContext(ctx).Table("association_members").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url").
		Joins("JOIN users ON users.id = association_members.user_id").
		Where("association_members.association_id = ? AND association_members.status = ?", associationID, domain.MembershipStatusPending).
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

// GetJoinedAssociations returns every association userID has a row for in
// association_members, newest-joined first isn't tracked (no joined_at
// column on the join table), so this orders by the association's own
// created_at like every other association list.
func (r *associationRepository) GetJoinedAssociations(ctx context.Context, userID uuid.UUID) ([]domain.Association, error) {
	var associations []domain.Association
	err := r.db.WithContext(ctx).
		Joins("JOIN association_members ON association_members.association_id = associations.id").
		Where("association_members.user_id = ? AND association_members.status = ?", userID, domain.MembershipStatusApproved).
		Order("associations.created_at desc").
		Find(&associations).Error
	if err != nil {
		return nil, err
	}
	for i := range associations {
		associations[i].IsMember = true
	}
	return associations, nil
}
