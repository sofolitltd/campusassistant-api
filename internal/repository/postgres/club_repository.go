package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type clubRepository struct {
	db *gorm.DB
}

func NewClubRepository(db *gorm.DB) domain.ClubRepository {
	return &clubRepository{db: db}
}

func (r *clubRepository) GetAllClubs(ctx context.Context, filters domain.ClubFilters) ([]domain.Club, error) {
	var clubs []domain.Club
	q := r.db.WithContext(ctx).Model(&domain.Club{})
	if filters.UniversityID != nil {
		q = q.Where("university_id = ?", *filters.UniversityID)
	}
	if filters.DepartmentID != nil {
		q = q.Where("department_id = ?", *filters.DepartmentID)
	}
	if filters.ClubType != "" {
		q = q.Where("club_type = ?", filters.ClubType)
	}
	if filters.Category != "" {
		q = q.Where("category = ?", filters.Category)
	}
	if filters.ActiveOnly {
		q = q.Where("is_active = ?", true)
	}
	if err := q.Order("created_at desc").Find(&clubs).Error; err != nil {
		return nil, err
	}

	if filters.RequestingUserID != nil && len(clubs) > 0 {
		ids := make([]uuid.UUID, len(clubs))
		for i, c := range clubs {
			ids[i] = c.ID
		}
		var followedIDs []uuid.UUID
		if err := r.db.WithContext(ctx).Table("club_follows").
			Where("user_id = ? AND club_id IN ?", *filters.RequestingUserID, ids).
			Pluck("club_id", &followedIDs).Error; err != nil {
			return nil, err
		}
		followed := make(map[uuid.UUID]bool, len(followedIDs))
		for _, id := range followedIDs {
			followed[id] = true
		}
		var memberIDs []uuid.UUID
		if err := r.db.WithContext(ctx).Table("club_members").
			Where("user_id = ? AND club_id IN ?", *filters.RequestingUserID, ids).
			Pluck("club_id", &memberIDs).Error; err != nil {
			return nil, err
		}
		isMember := make(map[uuid.UUID]bool, len(memberIDs))
		for _, id := range memberIDs {
			isMember[id] = true
		}
		for i := range clubs {
			clubs[i].IsFollowing = followed[clubs[i].ID]
			clubs[i].IsMember = isMember[clubs[i].ID]
		}
	}

	return clubs, nil
}

func (r *clubRepository) GetClubByID(ctx context.Context, id uuid.UUID, requestingUserID *uuid.UUID) (*domain.Club, error) {
	var club domain.Club
	if err := r.db.WithContext(ctx).First(&club, "id = ?", id).Error; err != nil {
		return nil, err
	}
	if requestingUserID != nil {
		var count int64
		if err := r.db.WithContext(ctx).Table("club_follows").
			Where("club_id = ? AND user_id = ?", id, *requestingUserID).
			Count(&count).Error; err != nil {
			return nil, err
		}
		club.IsFollowing = count > 0
		var memberCount int64
		if err := r.db.WithContext(ctx).Table("club_members").
			Where("club_id = ? AND user_id = ?", id, *requestingUserID).
			Count(&memberCount).Error; err != nil {
			return nil, err
		}
		club.IsMember = memberCount > 0
	}
	return &club, nil
}

func (r *clubRepository) CreateClub(ctx context.Context, club *domain.Club) error {
	return r.db.WithContext(ctx).Create(club).Error
}

func (r *clubRepository) UpdateClub(ctx context.Context, club *domain.Club) error {
	return r.db.WithContext(ctx).Save(club).Error
}

func (r *clubRepository) DeleteClub(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Club{}, "id = ?", id).Error
}

// FollowClub is idempotent — following an already-followed club is a no-op,
// not an error, so the client doesn't need to track prior state carefully.
func (r *clubRepository) FollowClub(ctx context.Context, clubID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		follow := domain.ClubFollow{ClubID: clubID, UserID: userID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&follow)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // already following
		}
		return tx.Model(&domain.Club{}).Where("id = ?", clubID).
			UpdateColumn("followers_count", gorm.Expr("followers_count + ?", 1)).Error
	})
}

func (r *clubRepository) UnfollowClub(ctx context.Context, clubID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&domain.ClubFollow{}, "club_id = ? AND user_id = ?", clubID, userID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // wasn't following
		}
		return tx.Model(&domain.Club{}).Where("id = ? AND followers_count > 0", clubID).
			UpdateColumn("followers_count", gorm.Expr("followers_count - ?", 1)).Error
	})
}

// JoinClub is idempotent, same as FollowClub — but writes to the separate
// club_members table/counter, a distinct roster from following.
func (r *clubRepository) JoinClub(ctx context.Context, clubID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		member := domain.ClubMember{ClubID: clubID, UserID: userID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&member)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // already a member
		}
		return tx.Model(&domain.Club{}).Where("id = ?", clubID).
			UpdateColumn("members_count", gorm.Expr("members_count + ?", 1)).Error
	})
}

func (r *clubRepository) LeaveClub(ctx context.Context, clubID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&domain.ClubMember{}, "club_id = ? AND user_id = ?", clubID, userID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil // wasn't a member
		}
		return tx.Model(&domain.Club{}).Where("id = ? AND members_count > 0", clubID).
			UpdateColumn("members_count", gorm.Expr("members_count - ?", 1)).Error
	})
}

func (r *clubRepository) GetPublicClubMembers(ctx context.Context, clubID uuid.UUID) ([]domain.ClubUserSummary, error) {
	var summaries []domain.ClubUserSummary
	err := r.db.WithContext(ctx).Table("club_members").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url").
		Joins("JOIN users ON users.id = club_members.user_id").
		Where("club_members.club_id = ?", clubID).
		Scan(&summaries).Error
	return summaries, err
}

func (r *clubRepository) GetPublicClubFollowers(ctx context.Context, clubID uuid.UUID) ([]domain.ClubUserSummary, error) {
	var summaries []domain.ClubUserSummary
	err := r.db.WithContext(ctx).Table("club_follows").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url").
		Joins("JOIN users ON users.id = club_follows.user_id").
		Where("club_follows.club_id = ?", clubID).
		Scan(&summaries).Error
	return summaries, err
}

// SuggestClub forces IsActive false and, in the same transaction, seeds a
// ClubManager{Role: owner} row for creatorID — so the requester can already
// manage (edit/add events/posts to) the club while it awaits admin review.
func (r *clubRepository) SuggestClub(ctx context.Context, club *domain.Club, creatorID uuid.UUID) error {
	club.IsActive = false
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(club).Error; err != nil {
			return err
		}
		manager := domain.ClubManager{ClubID: club.ID, UserID: creatorID, Role: domain.ClubManagerRoleOwner}
		manager.CreatedByID = creatorID
		manager.UpdatedByID = creatorID
		return tx.Create(&manager).Error
	})
}
