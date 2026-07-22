package postgres

import (
	"context"
	"errors"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type clubManagementRepository struct {
	db *gorm.DB
}

func NewClubManagementRepository(db *gorm.DB) domain.ClubManagementRepository {
	return &clubManagementRepository{db: db}
}

func (r *clubManagementRepository) GetMyClubs(ctx context.Context, userID uuid.UUID) ([]domain.Club, error) {
	var clubs []domain.Club
	err := r.db.WithContext(ctx).
		Distinct("clubs.*").
		Joins("LEFT JOIN club_managers ON club_managers.club_id = clubs.id AND club_managers.user_id = ?", userID).
		Where("clubs.created_by_id = ? OR club_managers.user_id = ?", userID, userID).
		Order("clubs.created_at desc").
		Find(&clubs).Error
	return clubs, err
}

func (r *clubManagementRepository) GetManagerRole(ctx context.Context, clubID, userID uuid.UUID) (string, error) {
	var m domain.ClubManager
	err := r.db.WithContext(ctx).Where("club_id = ? AND user_id = ?", clubID, userID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return m.Role, nil
}

func (r *clubManagementRepository) UpdateMyClub(ctx context.Context, club *domain.Club) error {
	return r.db.WithContext(ctx).Save(club).Error
}

func (r *clubManagementRepository) ListManagers(ctx context.Context, clubID uuid.UUID) ([]domain.ClubUserSummary, error) {
	var summaries []domain.ClubUserSummary
	err := r.db.WithContext(ctx).Table("club_managers").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url, club_managers.role").
		Joins("JOIN users ON users.id = club_managers.user_id").
		Where("club_managers.club_id = ?", clubID).
		Order("club_managers.role asc"). // "manager" < "owner" alphabetically; handler/UI can resort if needed
		Scan(&summaries).Error
	return summaries, err
}

// PromoteManager inserts, or updates the role if a row already exists —
// callers (handler) are responsible for confirming targetUserID currently
// follows clubID before calling this.
func (r *clubManagementRepository) PromoteManager(ctx context.Context, clubID, targetUserID uuid.UUID, role string) error {
	manager := domain.ClubManager{ClubID: clubID, UserID: targetUserID, Role: role}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "club_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"role"}),
		}).
		Create(&manager).Error
}

func (r *clubManagementRepository) RemoveManager(ctx context.Context, clubID, targetUserID uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.ClubManager{}, "club_id = ? AND user_id = ?", clubID, targetUserID).Error
}

func (r *clubManagementRepository) CreateClubPost(ctx context.Context, post *domain.ClubPost) error {
	return r.db.WithContext(ctx).Create(post).Error
}

func (r *clubManagementRepository) GetPublicClubPosts(ctx context.Context, clubID uuid.UUID) ([]domain.ClubPost, error) {
	var posts []domain.ClubPost
	err := r.db.WithContext(ctx).
		Where("club_id = ?", clubID).
		Order("created_at desc").
		Find(&posts).Error
	return posts, err
}
