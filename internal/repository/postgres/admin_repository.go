package postgres

import (
	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AdminRepository struct {
	db *gorm.DB
}

func NewAdminRepository(db *gorm.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

func (r *AdminRepository) FindByEmail(email string) (*domain.Admin, error) {
	var admin domain.Admin
	err := r.db.Where("email = ?", email).First(&admin).Error
	if err != nil {
		return nil, err
	}
	return &admin, nil
}

func (r *AdminRepository) FindByID(id uuid.UUID) (*domain.Admin, error) {
	var admin domain.Admin
	err := r.db.First(&admin, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &admin, nil
}

func (r *AdminRepository) Create(admin *domain.Admin) error {
	return r.db.Create(admin).Error
}

func (r *AdminRepository) List() ([]domain.Admin, error) {
	var admins []domain.Admin
	err := r.db.Order("created_at desc").Find(&admins).Error
	if err != nil {
		return nil, err
	}
	return admins, nil
}

func (r *AdminRepository) DeleteByID(id uuid.UUID) error {
	return r.db.Delete(&domain.Admin{}, "id = ?", id).Error
}

func (r *AdminRepository) UpdatePassword(id uuid.UUID, hashedPassword string) error {
	return r.db.Model(&domain.Admin{}).Where("id = ?", id).Update("password_hash", hashedPassword).Error
}

func (r *AdminRepository) UpdateName(id uuid.UUID, name string) error {
	return r.db.Model(&domain.Admin{}).Where("id = ?", id).Update("name", name).Error
}
