package handler

import (
	"net/http"
	"strconv"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type RewardHandler struct {
	rewardRepo domain.RewardRepository
	db         *gorm.DB
}

func NewRewardHandler(rewardRepo domain.RewardRepository, db *gorm.DB) *RewardHandler {
	return &RewardHandler{rewardRepo: rewardRepo, db: db}
}

type BalanceResponse struct {
	Balance        int   `json:"balance"`
	LifetimeEarned int   `json:"lifetime_earned"`
	LifetimeSpent  int   `json:"lifetime_spent"`
}

// GET /rewards/balance
func (h *RewardHandler) GetBalance(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	reward, err := h.rewardRepo.GetOrCreate(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get balance"})
		return
	}

	c.JSON(http.StatusOK, BalanceResponse{
		Balance:        reward.Balance,
		LifetimeEarned: reward.LifetimeEarned,
		LifetimeSpent:  reward.LifetimeSpent,
	})
}

const (
	adRewardDescription = "Earned by watching ad"
	adRewardCooldown    = 30 * time.Second
	adRewardDailyLimit  = 5
)

type EarnResponse struct {
	Balance        int `json:"balance"`
	LifetimeEarned int `json:"lifetime_earned"`
	LifetimeSpent  int `json:"lifetime_spent"`
	Earned         int `json:"earned"`
}

// POST /rewards/earn
func (h *RewardHandler) Earn(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	// Throttle ad rewards: a minimum gap between claims and a rolling 24h cap.
	since := time.Now().Add(-24 * time.Hour)
	var recent []time.Time
	if err := h.db.WithContext(c.Request.Context()).
		Model(&domain.RewardTransaction{}).
		Where("user_id = ? AND type = ? AND description = ? AND created_at > ?",
			userID, domain.RewardEarn, adRewardDescription, since).
		Order("created_at desc").
		Pluck("created_at", &recent).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check reward limits"})
		return
	}
	if len(recent) >= adRewardDailyLimit {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Daily ad reward limit reached. Try again tomorrow."})
		return
	}
	if len(recent) > 0 && time.Since(recent[0]) < adRewardCooldown {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Please wait a moment before claiming another reward."})
		return
	}

	reward, err := h.rewardRepo.GetOrCreate(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get balance"})
		return
	}

	reward.Balance++
	reward.LifetimeEarned++

	if err := h.rewardRepo.UpdateBalance(c.Request.Context(), reward); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update balance"})
		return
	}

	txn := &domain.RewardTransaction{
		UserID:       userID,
		Type:         domain.RewardEarn,
		Amount:       1,
		BalanceAfter: reward.Balance,
		Description:  adRewardDescription,
	}
	if err := h.rewardRepo.CreateTransaction(c.Request.Context(), txn); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record transaction"})
		return
	}

	c.JSON(http.StatusOK, EarnResponse{
		Balance:        reward.Balance,
		LifetimeEarned: reward.LifetimeEarned,
		LifetimeSpent:  reward.LifetimeSpent,
		Earned:         1,
	})
}

type SpendResponse struct {
	Spent      int  `json:"spent"`
	Balance    int  `json:"balance"`
	IsPro      bool `json:"is_pro"`
}

// POST /rewards/spend/:resourceId
func (h *RewardHandler) Spend(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	resourceID, err := uuid.Parse(c.Param("resourceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid resource ID"})
		return
	}

	var user domain.User
	if err := h.db.WithContext(c.Request.Context()).First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	if user.IsPro {
		c.JSON(http.StatusOK, SpendResponse{Spent: 0, Balance: 0, IsPro: true})
		return
	}

	var resource domain.Resource
	if err := h.db.WithContext(c.Request.Context()).First(&resource, resourceID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
		return
	}

	cost := domain.ComputeRewardCost(resource.FileSizeBytes)

	reward, err := h.rewardRepo.GetOrCreate(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get balance"})
		return
	}

	if reward.Balance < cost {
		c.JSON(http.StatusOK, gin.H{
			"insufficient": true,
			"required":      cost,
			"balance":       reward.Balance,
			"deficit":       cost - reward.Balance,
		})
		return
	}

	reward.Balance -= cost
	reward.LifetimeSpent += cost

	if err := h.rewardRepo.UpdateBalance(c.Request.Context(), reward); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update balance"})
		return
	}

	description := "Spent for resource access"
	if resource.Title != "" {
		description = "Spent for: " + truncate(resource.Title, 100)
	}
	txn := &domain.RewardTransaction{
		UserID:       userID,
		Type:         domain.RewardSpend,
		Amount:       cost,
		BalanceAfter: reward.Balance,
		ResourceID:   &resourceID,
		Description:  description,
	}
	if err := h.rewardRepo.CreateTransaction(c.Request.Context(), txn); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record transaction"})
		return
	}

	c.JSON(http.StatusOK, SpendResponse{
		Spent:   cost,
		Balance: reward.Balance,
		IsPro:   false,
	})
}

type TransactionResponse struct {
	Data  []domain.RewardTransaction `json:"data"`
	Count int64                      `json:"count"`
	Limit int                        `json:"limit"`
	Offset int                       `json:"offset"`
}

// GET /rewards/transactions
func (h *RewardHandler) GetTransactions(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	txns, count, err := h.rewardRepo.GetTransactions(c.Request.Context(), userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get transactions"})
		return
	}

	if txns == nil {
		txns = []domain.RewardTransaction{}
	}

	c.JSON(http.StatusOK, TransactionResponse{
		Data:   txns,
		Count:  count,
		Limit:  limit,
		Offset: offset,
	})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// GET /rewards/cost/:resourceId — returns the reward cost for a resource
func (h *RewardHandler) GetCost(c *gin.Context) {
	resourceID, err := uuid.Parse(c.Param("resourceId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid resource ID"})
		return
	}

	var resource domain.Resource
	if err := h.db.WithContext(c.Request.Context()).First(&resource, resourceID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
		return
	}

	cost := domain.ComputeRewardCost(resource.FileSizeBytes)
	c.JSON(http.StatusOK, gin.H{"cost": cost, "file_size_bytes": resource.FileSizeBytes})
}
