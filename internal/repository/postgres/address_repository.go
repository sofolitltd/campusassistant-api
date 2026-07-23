package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type addressRepository struct {
	db *gorm.DB
}

func NewAddressRepository(db *gorm.DB) domain.AddressRepository {
	return &addressRepository{db: db}
}

func (r *addressRepository) GetByUser(ctx context.Context, userID uuid.UUID) ([]domain.Address, error) {
	var addresses []domain.Address
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("is_default desc, created_at desc").
		Find(&addresses).Error
	return addresses, err
}

func (r *addressRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Address, error) {
	var address domain.Address
	err := r.db.WithContext(ctx).First(&address, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &address, nil
}

func (r *addressRepository) Create(ctx context.Context, address *domain.Address) error {
	return r.db.WithContext(ctx).Create(address).Error
}

func (r *addressRepository) Update(ctx context.Context, address *domain.Address) error {
	return r.db.WithContext(ctx).Save(address).Error
}

func (r *addressRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Address{}, id).Error
}

// SetDefault transactionally unsets all other addresses for this user, then
// marks the target one as default.
func (r *addressRepository) SetDefault(ctx context.Context, userID, addressID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Address{}).
			Where("user_id = ?", userID).
			Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Address{}).
			Where("id = ?", addressID).
			Update("is_default", true).Error
	})
}
