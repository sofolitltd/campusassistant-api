package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type orderTransactionRepository struct {
	db *gorm.DB
}

func NewOrderTransactionRepository(db *gorm.DB) domain.OrderTransactionRepository {
	return &orderTransactionRepository{db: db}
}

func (r *orderTransactionRepository) Create(ctx context.Context, tx *domain.OrderTransaction) error {
	return r.db.WithContext(ctx).Create(tx).Error
}

func (r *orderTransactionRepository) GetByPaymentID(ctx context.Context, paymentID string) (*domain.OrderTransaction, error) {
	var tx domain.OrderTransaction
	err := r.db.WithContext(ctx).Where("payment_id = ?", paymentID).First(&tx).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *orderTransactionRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.BkashTransactionStatus, trxID, rawResponse string) error {
	return r.db.WithContext(ctx).Model(&domain.OrderTransaction{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       status,
			"trx_id":       trxID,
			"raw_response": rawResponse,
		}).Error
}
