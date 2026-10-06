package domain

import (
	"context"

	"github.com/google/uuid"
)

type UserReward struct {
	Base
	UserID         uuid.UUID `gorm:"type:uuid;uniqueIndex;not null" json:"user_id"`
	Balance        int       `gorm:"default:50" json:"balance"`
	LifetimeEarned int       `gorm:"default:0" json:"lifetime_earned"`
	LifetimeSpent  int       `gorm:"default:0" json:"lifetime_spent"`
}

type RewardTransactionType string

const (
	RewardEarn  RewardTransactionType = "earn"
	RewardSpend RewardTransactionType = "spend"
)

type RewardTransaction struct {
	Base
	UserID      uuid.UUID             `gorm:"type:uuid;index;not null" json:"user_id"`
	Type        RewardTransactionType `gorm:"type:varchar(10);not null" json:"type"`
	Amount      int                   `gorm:"not null" json:"amount"`
	BalanceAfter int                  `gorm:"not null" json:"balance_after"`
	ResourceID  *uuid.UUID            `gorm:"type:uuid" json:"resource_id,omitempty"`
	Description string                `gorm:"size:255" json:"description"`
}

type RewardRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*UserReward, error)
	Create(ctx context.Context, reward *UserReward) error
	UpdateBalance(ctx context.Context, reward *UserReward) error
	CreateTransaction(ctx context.Context, txn *RewardTransaction) error
	GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]RewardTransaction, int64, error)
	GetOrCreate(ctx context.Context, userID uuid.UUID) (*UserReward, error)
}

func ComputeRewardCost(fileSizeBytes int64) int {
	if fileSizeBytes <= 0 {
		return 1
	}
	mb := fileSizeBytes / (1024 * 1024)
	cost := int(mb / 10)
	if cost < 1 {
		return 1
	}
	return cost
}
