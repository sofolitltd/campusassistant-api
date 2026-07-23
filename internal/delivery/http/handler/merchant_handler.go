package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type MerchantHandler struct {
	repo domain.MerchantRepository
}

func NewMerchantHandler(repo domain.MerchantRepository) *MerchantHandler {
	return &MerchantHandler{repo: repo}
}

// GetAllMerchants is the admin listing, optionally filtered by ?status=.
func (h *MerchantHandler) GetAllMerchants(c *gin.Context) {
	status := domain.MerchantStatus(c.Query("status"))
	merchants, err := h.repo.GetAllMerchants(c.Request.Context(), status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch merchants"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": merchants})
}

// redactContactInfo strips Phone/Email before a merchant is serialized to a
// public (non-admin, non-owner) response — those are for admin/self use
// only, unlike BusinessType which is shown on the public storefront.
func redactContactInfo(merchant *domain.Merchant) *domain.Merchant {
	public := *merchant
	public.Phone = ""
	public.Email = ""
	return &public
}

// GetPlatformMerchant returns (creating on first use) the synthetic
// Merchant row that owns Campus Assistant's own in-house products.
func (h *MerchantHandler) GetPlatformMerchant(c *gin.Context) {
	merchant, err := h.repo.GetOrCreatePlatformMerchant(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch platform merchant"})
		return
	}
	c.JSON(http.StatusOK, redactContactInfo(merchant))
}

// GetMerchantByID is the public storefront lookup (used by the app's
// merchant profile screen) — Phone/Email are redacted; admins use
// GetAllMerchants instead, which returns them in full.
func (h *MerchantHandler) GetMerchantByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	merchant, err := h.repo.GetMerchantByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return
	}
	c.JSON(http.StatusOK, redactContactInfo(merchant))
}

func (h *MerchantHandler) UpdateMerchant(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	// Load the existing row and bind JSON onto it (not a blank struct) so a
	// partial payload — e.g. the admin panel's commission-rate-only edit —
	// doesn't zero out every other field.
	merchant, err := h.repo.GetMerchantByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return
	}
	if err := c.ShouldBindJSON(merchant); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	merchant.ID = id
	if err := h.repo.UpdateMerchant(c.Request.Context(), merchant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update merchant"})
		return
	}
	c.JSON(http.StatusOK, merchant)
}

func (h *MerchantHandler) ApproveMerchant(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	if err := h.repo.SetMerchantStatus(c.Request.Context(), id, domain.MerchantStatusApproved, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to approve merchant"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant approved"})
}

func (h *MerchantHandler) RejectMerchant(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.repo.SetMerchantStatus(c.Request.Context(), id, domain.MerchantStatusRejected, body.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject merchant"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant rejected"})
}

func (h *MerchantHandler) DeleteMerchant(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	if err := h.repo.DeleteMerchant(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete merchant"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant deleted"})
}

// ApplyForMerchant is the app-facing endpoint: the current JWT user applies
// to become a merchant, starting in "pending" status.
func (h *MerchantHandler) ApplyForMerchant(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	if existing, err := h.repo.GetMerchantByUserID(c.Request.Context(), userID); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Merchant application already exists", "data": existing})
		return
	} else if err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing application"})
		return
	}

	var body struct {
		BusinessName string `json:"business_name" binding:"required"`
		Description  string `json:"description"`
		LogoURL      string `json:"logo_url"`
		BusinessType string `json:"business_type"`
		Phone        string `json:"phone"`
		Email        string `json:"email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	merchant := domain.Merchant{
		UserID:       userID,
		BusinessName: body.BusinessName,
		Description:  body.Description,
		LogoURL:      body.LogoURL,
		BusinessType: body.BusinessType,
		Phone:        body.Phone,
		Email:        body.Email,
		Status:       domain.MerchantStatusPending,
	}
	if err := h.repo.CreateMerchant(c.Request.Context(), &merchant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create merchant application"})
		return
	}
	c.JSON(http.StatusOK, merchant)
}

// GetMyMerchant returns the current JWT user's own merchant profile, if any.
func (h *MerchantHandler) GetMyMerchant(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchant, err := h.repo.GetMerchantByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No merchant profile found"})
		return
	}
	c.JSON(http.StatusOK, merchant)
}
