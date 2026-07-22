package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type skillRepository struct {
	db *gorm.DB
}

func NewSkillRepository(db *gorm.DB) domain.SkillRepository {
	return &skillRepository{db: db}
}

func orderVideosByIndex(db *gorm.DB) *gorm.DB {
	return db.Order("index asc")
}

func (r *skillRepository) GetAllSkills(ctx context.Context) ([]domain.Skill, error) {
	var skills []domain.Skill
	err := r.db.WithContext(ctx).
		Preload("Targets").
		Preload("Videos", orderVideosByIndex).
		Order("index asc").
		Find(&skills).Error
	return skills, err
}

func (r *skillRepository) GetSkillByID(ctx context.Context, id uuid.UUID) (*domain.Skill, error) {
	var skill domain.Skill
	err := r.db.WithContext(ctx).
		Preload("Targets").
		Preload("Videos", orderVideosByIndex).
		First(&skill, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &skill, nil
}

func (r *skillRepository) CreateSkill(ctx context.Context, skill *domain.Skill) error {
	return r.db.WithContext(ctx).Create(skill).Error
}

func (r *skillRepository) UpdateSkill(ctx context.Context, skill *domain.Skill) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete old targets first to handle multi-select updates correctly
		// (same reasoning as subscriptionRepository.UpdatePlan).
		if err := tx.Where("skill_id = ?", skill.ID).Delete(&domain.SkillTarget{}).Error; err != nil {
			return err
		}
		return tx.Save(skill).Error
	})
}

func (r *skillRepository) DeleteSkill(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Skill{}, id).Error
}

func (r *skillRepository) GetSkillsByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]domain.Skill, error) {
	var skills []domain.Skill
	err := r.db.WithContext(ctx).
		Distinct("skills.*").
		Joins("LEFT JOIN skill_targets ON skill_targets.skill_id = skills.id").
		Where("skills.is_published = ?", true).
		Where("skill_targets.id IS NULL OR (skill_targets.university_id = ? AND (skill_targets.department_id = ? OR skill_targets.department_id = ?))",
			universityID, departmentID, uuid.Nil).
		Order("skills.index asc").
		Preload("Videos", orderVideosByIndex).
		Find(&skills).Error
	return skills, err
}
