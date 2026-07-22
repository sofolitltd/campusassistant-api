package postgres

import (
	"context"
	"errors"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type associationManagementRepository struct {
	db *gorm.DB
}

func NewAssociationManagementRepository(db *gorm.DB) domain.AssociationManagementRepository {
	return &associationManagementRepository{db: db}
}

func (r *associationManagementRepository) GetMyAssociations(ctx context.Context, userID uuid.UUID) ([]domain.Association, error) {
	var associations []domain.Association
	err := r.db.WithContext(ctx).
		Distinct("associations.*").
		Joins("LEFT JOIN association_managers ON association_managers.association_id = associations.id AND association_managers.user_id = ?", userID).
		Where("associations.created_by_id = ? OR association_managers.user_id = ?", userID, userID).
		Order("associations.created_at desc").
		Find(&associations).Error
	return associations, err
}

func (r *associationManagementRepository) GetManagerRole(ctx context.Context, associationID, userID uuid.UUID) (string, error) {
	var m domain.AssociationManager
	err := r.db.WithContext(ctx).Where("association_id = ? AND user_id = ?", associationID, userID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return m.Role, nil
}

func (r *associationManagementRepository) UpdateMyAssociation(ctx context.Context, association *domain.Association) error {
	return r.db.WithContext(ctx).Save(association).Error
}

func (r *associationManagementRepository) ListManagers(ctx context.Context, associationID uuid.UUID) ([]domain.AssociationUserSummary, error) {
	var summaries []domain.AssociationUserSummary
	err := r.db.WithContext(ctx).Table("association_managers").
		Select("users.id as user_id, users.first_name, users.last_name, users.avatar_url, association_managers.role").
		Joins("JOIN users ON users.id = association_managers.user_id").
		Where("association_managers.association_id = ?", associationID).
		Order("association_managers.role asc").
		Scan(&summaries).Error
	return summaries, err
}

// PromoteManager inserts, or updates the role if a row already exists —
// mirrors ClubManagementRepository.PromoteManager.
func (r *associationManagementRepository) PromoteManager(ctx context.Context, associationID, targetUserID uuid.UUID, role string) error {
	manager := domain.AssociationManager{AssociationID: associationID, UserID: targetUserID, Role: role}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "association_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"role"}),
		}).
		Create(&manager).Error
}

func (r *associationManagementRepository) RemoveManager(ctx context.Context, associationID, targetUserID uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.AssociationManager{}, "association_id = ? AND user_id = ?", associationID, targetUserID).Error
}

func (r *associationManagementRepository) CreateAssociationPost(ctx context.Context, post *domain.AssociationPost) error {
	return r.db.WithContext(ctx).Create(post).Error
}

func (r *associationManagementRepository) GetPublicAssociationPosts(ctx context.Context, associationID uuid.UUID) ([]domain.AssociationPost, error) {
	var posts []domain.AssociationPost
	err := r.db.WithContext(ctx).
		Where("association_id = ?", associationID).
		Order("created_at desc").
		Find(&posts).Error
	return posts, err
}
