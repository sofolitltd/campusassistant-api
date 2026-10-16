package handler

import (
	"errors"
	"net/http"
	"strconv"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ReferralHandler serves the app's "Invite & Earn" screen. Rewards themselves
// are paid from the verification paths (see postgres.GrantReferralReward).
type ReferralHandler struct {
	db *gorm.DB
}

func NewReferralHandler(db *gorm.DB) *ReferralHandler {
	return &ReferralHandler{db: db}
}

// GET /my/referral
func (h *ReferralHandler) GetMine(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	summary, err := postgres.GetReferralSummary(c.Request.Context(), h.db, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load referral info"})
		return
	}
	c.JSON(http.StatusOK, summary)
}

type applyReferralRequest struct {
	Code string `json:"code" binding:"required"`
}

// POST /my/referral/apply {code}
func (h *ReferralHandler) Apply(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var req applyReferralRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Referral code is required"})
		return
	}
	if err := postgres.ApplyReferralCode(c.Request.Context(), h.db, userID, req.Code); err != nil {
		c.JSON(referralErrorStatus(err), gin.H{"error": referralErrorMessage(err)})
		return
	}
	summary, err := postgres.GetReferralSummary(c.Request.Context(), h.db, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load referral info"})
		return
	}
	c.JSON(http.StatusOK, summary)
}

func referralErrorStatus(err error) int {
	switch {
	case errors.Is(err, domain.ErrReferralInvalidCode):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrReferralSelf),
		errors.Is(err, domain.ErrReferralCircular),
		errors.Is(err, domain.ErrReferralWindowClosed):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrReferralAlreadyApplied):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func referralErrorMessage(err error) string {
	if referralErrorStatus(err) == http.StatusInternalServerError {
		return "Failed to apply referral code"
	}
	return err.Error()
}

// GET /referrals/stats?days=30 — admin only. days=0 means all time.
func (h *ReferralHandler) AdminStats(c *gin.Context) {
	days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
	if err != nil || days < 0 {
		days = 30
	}
	stats, err := postgres.GetReferralAdminStats(c.Request.Context(), h.db, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load referral stats"})
		return
	}
	c.JSON(http.StatusOK, stats)
}
