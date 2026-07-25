package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type MerchantHandler struct {
	db              *gorm.DB
	repo            domain.MerchantRepository
	productRepo     domain.ProductRepository
	notificationSvc *service.NotificationService
}

func NewMerchantHandler(db *gorm.DB, repo domain.MerchantRepository, productRepo domain.ProductRepository, notificationSvc *service.NotificationService) *MerchantHandler {
	return &MerchantHandler{db: db, repo: repo, productRepo: productRepo, notificationSvc: notificationSvc}
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

// redactContactInfo strips Phone/Email/verification-document/payout fields
// and the owning User before a merchant is serialized to a public
// (non-admin, non-owner) response — those are for admin/self use only,
// unlike BusinessType which is shown on the public storefront.
func redactContactInfo(merchant *domain.Merchant) *domain.Merchant {
	public := *merchant
	public.Phone = ""
	public.Email = ""
	public.StudentIDProofURL = ""
	public.NIDProofURL = ""
	public.PayoutMethod = ""
	public.PayoutAccount = ""
	public.User = nil
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
// GetAllMerchants instead, which returns them in full. Preloads the owning
// User (read-only path) so the admin panel can cross-check applicant
// identity; redacted out for the public response.
func (h *MerchantHandler) GetMerchantByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	merchant, err := h.repo.GetMerchantWithUserByID(c.Request.Context(), id)
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

// notifyMerchantStatus pushes a notification to the applicant when their
// merchant application is approved/rejected — best-effort: a failure here
// never fails the approve/reject request itself.
func (h *MerchantHandler) notifyMerchantStatus(c *gin.Context, id uuid.UUID, approved bool) {
	if h.notificationSvc == nil {
		return
	}
	merchant, err := h.repo.GetMerchantByID(c.Request.Context(), id)
	if err != nil {
		return
	}
	title := "Merchant application approved"
	body := merchant.BusinessName + " is now live on the Campus Marketplace."
	if !approved {
		title = "Merchant application rejected"
		body = merchant.BusinessName + "'s application was not approved."
		if merchant.RejectionReason != "" {
			body += " Reason: " + merchant.RejectionReason
		}
	}
	recipients, err := service.FilterMutedRecipients(c.Request.Context(), h.db, []uuid.UUID{merchant.UserID}, "marketplace", "MERCHANT_STATUS")
	if err != nil || len(recipients) == 0 {
		return
	}

	n := domain.Notification{Title: title, Body: body, Type: "MERCHANT_STATUS", Scope: "user"}
	_, _, _ = h.notificationSvc.SendToUsers(c.Request.Context(), n, recipients, uuid.Nil)
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
	h.notifyMerchantStatus(c, id, true)
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
	h.notifyMerchantStatus(c, id, false)
	c.JSON(http.StatusOK, gin.H{"message": "Merchant rejected"})
}

// deleteMerchantGuarded refuses to delete a merchant that still has
// products — deleting one today would silently orphan those Product rows
// (no FK cascade is defined). Returns ok=false when it has already written
// an HTTP response itself (either the guard tripped, or a lookup failed)
// and the caller should just return without writing another one.
func (h *MerchantHandler) deleteMerchantGuarded(c *gin.Context, id uuid.UUID) (ok bool) {
	products, err := h.productRepo.GetAllProducts(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing products"})
		return false
	}
	if len(products) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Cannot delete a business that still has products. Remove or reassign its products first."})
		return false
	}
	if err := h.repo.DeleteMerchant(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete merchant"})
		return false
	}
	return true
}

func (h *MerchantHandler) DeleteMerchant(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	if !h.deleteMerchantGuarded(c, id) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant deleted"})
}

// maxMerchantsPerUser caps how many businesses a single student can register
// — a student may run more than one storefront (e.g. a food stall and a
// tutoring service), so this is a generous abuse guard rather than a
// one-business-per-user rule.
const maxMerchantsPerUser = 5

// ApplyForMerchant is the app-facing endpoint: the current JWT user applies
// to become a merchant, starting in "pending" status. A user may submit
// multiple applications for separate businesses, up to maxMerchantsPerUser.
func (h *MerchantHandler) ApplyForMerchant(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	existing, err := h.repo.GetMerchantsByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing applications"})
		return
	}
	if len(existing) >= maxMerchantsPerUser {
		c.JSON(http.StatusConflict, gin.H{"error": "Maximum number of businesses reached"})
		return
	}

	var body struct {
		BusinessName      string `json:"business_name" binding:"required"`
		Description       string `json:"description"`
		LogoURL           string `json:"logo_url"`
		BusinessType      string `json:"business_type"`
		Phone             string `json:"phone"`
		Email             string `json:"email"`
		Website           string `json:"website"`
		SocialMediaLink   string `json:"social_media_link"`
		StudentIDProofURL string `json:"student_id_proof_url"`
		NIDProofURL       string `json:"nid_proof_url"`
		PayoutMethod      string `json:"payout_method"`
		PayoutAccount     string `json:"payout_account"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	nameTaken, err := h.repo.ExistsByBusinessName(c.Request.Context(), body.BusinessName, uuid.Nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate business name"})
		return
	}
	if nameTaken {
		c.JSON(http.StatusConflict, gin.H{"error": "A business with this name already exists"})
		return
	}

	merchant := domain.Merchant{
		UserID:            userID,
		BusinessName:      body.BusinessName,
		Description:       body.Description,
		LogoURL:           body.LogoURL,
		BusinessType:      body.BusinessType,
		Phone:             body.Phone,
		Email:             body.Email,
		Website:           body.Website,
		SocialMediaLink:   body.SocialMediaLink,
		StudentIDProofURL: body.StudentIDProofURL,
		NIDProofURL:       body.NIDProofURL,
		PayoutMethod:      body.PayoutMethod,
		PayoutAccount:     body.PayoutAccount,
		Status:            domain.MerchantStatusPending,
	}
	if err := h.repo.CreateMerchant(c.Request.Context(), &merchant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create merchant application"})
		return
	}
	c.JSON(http.StatusOK, merchant)
}

// GetMyMerchant returns the current JWT user's own merchant profile, if any.
// Deprecated: a user may own multiple merchants now; prefer GetMyMerchants.
// Kept for compatibility with older app builds.
func (h *MerchantHandler) GetMyMerchant(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchant, err := h.repo.GetMerchantByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No merchant profile found"})
		return
	}
	c.JSON(http.StatusOK, merchant)
}

// GetMyMerchants returns every business the current JWT user owns.
func (h *MerchantHandler) GetMyMerchants(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchants, err := h.repo.GetMerchantsByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch merchants"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": merchants})
}

// UpdateMyMerchant lets the owner edit their own business's public/contact
// details. Admin-only fields (commission_rate, status, rejection_reason)
// are deliberately not bindable here — those stay behind UpdateMerchant.
func (h *MerchantHandler) UpdateMyMerchant(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
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
	if merchant.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return
	}

	var body struct {
		BusinessName      string `json:"business_name" binding:"required"`
		Description       string `json:"description"`
		LogoURL           string `json:"logo_url"`
		BusinessType      string `json:"business_type"`
		Phone             string `json:"phone"`
		Email             string `json:"email"`
		Website           string `json:"website"`
		SocialMediaLink   string `json:"social_media_link"`
		StudentIDProofURL string `json:"student_id_proof_url"`
		NIDProofURL       string `json:"nid_proof_url"`
		PayoutMethod      string `json:"payout_method"`
		PayoutAccount     string `json:"payout_account"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if body.BusinessName != merchant.BusinessName {
		nameTaken, err := h.repo.ExistsByBusinessName(c.Request.Context(), body.BusinessName, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate business name"})
			return
		}
		if nameTaken {
			c.JSON(http.StatusConflict, gin.H{"error": "A business with this name already exists"})
			return
		}
	}

	merchant.BusinessName = body.BusinessName
	merchant.Description = body.Description
	merchant.LogoURL = body.LogoURL
	merchant.BusinessType = body.BusinessType
	merchant.Phone = body.Phone
	merchant.Email = body.Email
	merchant.Website = body.Website
	merchant.SocialMediaLink = body.SocialMediaLink
	merchant.StudentIDProofURL = body.StudentIDProofURL
	merchant.NIDProofURL = body.NIDProofURL
	merchant.PayoutMethod = body.PayoutMethod
	merchant.PayoutAccount = body.PayoutAccount
	if err := h.repo.UpdateMerchant(c.Request.Context(), merchant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update merchant"})
		return
	}
	c.JSON(http.StatusOK, merchant)
}

// DeleteMyMerchant lets the owner close/delete their own business.
func (h *MerchantHandler) DeleteMyMerchant(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
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
	if merchant.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return
	}
	if !h.deleteMerchantGuarded(c, id) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant deleted"})
}
