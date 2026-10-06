package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type rewardRepository struct {
	db *gorm.DB
}

func NewRewardRepository(db *gorm.DB) domain.RewardRepository {
	return &rewardRepository{db: db}
}

func (r *rewardRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*domain.UserReward, error) {
	var reward domain.UserReward
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&reward).Error
	if err != nil {
		return nil, err
	}
	return &reward, nil
}

func (r *rewardRepository) Create(ctx context.Context, reward *domain.UserReward) error {
	return r.db.WithContext(ctx).Create(reward).Error
}

func (r *rewardRepository) UpdateBalance(ctx context.Context, reward *domain.UserReward) error {
	return r.db.WithContext(ctx).
		Model(&domain.UserReward{}).
		Where("user_id = ?", reward.UserID).
		Updates(map[string]interface{}{
			"balance":         reward.Balance,
			"lifetime_earned": reward.LifetimeEarned,
			"lifetime_spent":  reward.LifetimeSpent,
		}).Error
}

func (r *rewardRepository) CreateTransaction(ctx context.Context, txn *domain.RewardTransaction) error {
	return r.db.WithContext(ctx).Create(txn).Error
}

func (r *rewardRepository) GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.RewardTransaction, int64, error) {
	var txns []domain.RewardTransaction
	var count int64

	q := r.db.WithContext(ctx).Model(&domain.RewardTransaction{}).Where("user_id = ?", userID)

	if err := q.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&txns).Error; err != nil {
		return nil, 0, err
	}

	return txns, count, nil
}

func (r *rewardRepository) GetOrCreate(ctx context.Context, userID uuid.UUID) (*domain.UserReward, error) {
	reward, err := r.GetByUserID(ctx, userID)
	if err == nil {
		return reward, nil
	}

	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	reward = &domain.UserReward{
		UserID:         userID,
		Balance:        50,
		LifetimeEarned: 0,
		LifetimeSpent:  0,
	}
	if err := r.Create(ctx, reward); err != nil {
		return nil, err
	}

	bonusTxn := &domain.RewardTransaction{
		UserID:      userID,
		Type:        domain.RewardEarn,
		Amount:      50,
		BalanceAfter: 50,
		Description: "Welcome bonus",
	}
	if err := r.CreateTransaction(ctx, bonusTxn); err != nil {
		return nil, err
	}

	return reward, nil
}
