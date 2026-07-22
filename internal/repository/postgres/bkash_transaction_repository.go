package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type bkashTransactionRepository struct {
	db *gorm.DB
}

func NewBkashTransactionRepository(db *gorm.DB) domain.BkashTransactionRepository {
	return &bkashTransactionRepository{db: db}
}

func (r *bkashTransactionRepository) Create(ctx context.Context, tx *domain.BkashTransaction) error {
	return r.db.WithContext(ctx).Create(tx).Error
}

func (r *bkashTransactionRepository) GetByPaymentID(ctx context.Context, paymentID string) (*domain.BkashTransaction, error) {
	var tx domain.BkashTransaction
	err := r.db.WithContext(ctx).Where("payment_id = ?", paymentID).First(&tx).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *bkashTransactionRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.BkashTransactionStatus, trxID, rawResponse string) error {
	return r.db.WithContext(ctx).Model(&domain.BkashTransaction{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       status,
			"trx_id":       trxID,
			"raw_response": rawResponse,
		}).Error
}

func (r *bkashTransactionRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.BkashTransaction, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&domain.BkashTransaction{}).
		Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var txs []domain.BkashTransaction
	err := r.db.WithContext(ctx).
		Preload("Plan").
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&txs).Error
	if err != nil {
		return nil, 0, err
	}
	return txs, total, nil
}
