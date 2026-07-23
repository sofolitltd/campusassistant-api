package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type orderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) domain.OrderRepository {
	return &orderRepository{db: db}
}

func (r *orderRepository) Create(ctx context.Context, order *domain.Order) error {
	return r.db.WithContext(ctx).Create(order).Error
}

func (r *orderRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	var order domain.Order
	err := r.db.WithContext(ctx).
		Preload("Items").
		First(&order, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *orderRepository) GetByBuyer(ctx context.Context, buyerID uuid.UUID) ([]domain.Order, error) {
	var orders []domain.Order
	err := r.db.WithContext(ctx).
		Preload("Items").
		Where("buyer_id = ?", buyerID).
		Order("created_at desc").
		Find(&orders).Error
	return orders, err
}

func (r *orderRepository) GetAll(ctx context.Context, status domain.OrderStatus) ([]domain.Order, error) {
	var orders []domain.Order
	q := r.db.WithContext(ctx).Preload("Items")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Order("created_at desc").Find(&orders).Error
	return orders, err
}

func (r *orderRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.OrderStatus) error {
	return r.db.WithContext(ctx).Model(&domain.Order{}).
		Where("id = ?", id).
		Update("status", status).Error
}
