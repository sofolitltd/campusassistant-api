package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AssociationManageHandler backs the JWT-protected "/my/associations"
// self-service surface — mirrors ClubManageHandler.
type AssociationManageHandler struct {
	repo                domain.AssociationManagementRepository
	associationRepo     domain.AssociationRepository
	db                  *gorm.DB
	notificationService *service.NotificationService
}

func NewAssociationManageHandler(repo domain.AssociationManagementRepository, associationRepo domain.AssociationRepository, db *gorm.DB, notificationService *service.NotificationService) *AssociationManageHandler {
	return &AssociationManageHandler{repo: repo, associationRepo: associationRepo, db: db, notificationService: notificationService}
}

// requireManager 403s and returns false if userID has no AssociationManager
// row for associationID, or (when ownerOnly) isn't specifically the owner.
func (h *AssociationManageHandler) requireManager(c *gin.Context, associationID, userID uuid.UUID, ownerOnly bool) bool {
	role, err := h.repo.GetManagerRole(c.Request.Context(), associationID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if role == "" || (ownerOnly && role != domain.AssociationManagerRoleOwner) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to manage this association"})
		return false
	}
	return true
}

// GET /my/associations
func (h *AssociationManageHandler) GetMyAssociations(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	associations, err := h.repo.GetMyAssociations(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, associations)
}

// associationUpdatableFields is the whitelist UpdateMyAssociation binds onto
// the existing row — deliberately excludes IsActive/IsVerified/UniversityID/
// DistrictID/SubDistrictID/AssociationType, which stay admin-controlled via
// the separate /associations/:id admin route.
type associationUpdatableFields struct {
	Name         *string                `json:"name"`
	Description  *string                `json:"description"`
	LogoURL      *string                `json:"logo_url"`
	BannerURL    *string                `json:"banner_url"`
	FoundedYear  *int                   `json:"founded_year"`
	Category     *string                `json:"category"`
	ContactEmail *string                `json:"contact_email"`
	ContactPhone *string                `json:"contact_phone"`
	SocialLinks  map[string]interface{} `json:"social_links"`
}

// PUT /my/associations/:id
func (h *AssociationManageHandler) UpdateMyAssociation(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, false) {
		return
	}

	var fields associationUpdatableFields
	if err := c.ShouldBindJSON(&fields); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	association, err := h.associationRepo.GetAssociationByID(c.Request.Context(), associationID, nil)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Association not found"})
		return
	}
	if fields.Name != nil {
		association.Name = *fields.Name
	}
	if fields.Description != nil {
		association.Description = *fields.Description
	}
	if fields.LogoURL != nil {
		association.LogoURL = fields.LogoURL
	}
	if fields.BannerURL != nil {
		association.BannerURL = fields.BannerURL
	}
	if fields.FoundedYear != nil {
		association.FoundedYear = fields.FoundedYear
	}
	if fields.Category != nil {
		association.Category = *fields.Category
	}
	if fields.ContactEmail != nil {
		association.ContactEmail = fields.ContactEmail
	}
	if fields.ContactPhone != nil {
		association.ContactPhone = fields.ContactPhone
	}
	if fields.SocialLinks != nil {
		raw, err := json.Marshal(fields.SocialLinks)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid social_links"})
			return
		}
		association.SocialLinks = datatypes.JSON(raw)
	}
	association.UpdatedByID = userID

	if err := h.repo.UpdateMyAssociation(c.Request.Context(), association); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, association)
}

// GET /my/associations/:id/followers — the pool eligible for promotion.
func (h *AssociationManageHandler) GetFollowersForPromotion(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, false) {
		return
	}
	followers, err := h.associationRepo.GetPublicAssociationFollowers(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, followers)
}

// GET /my/associations/:id/managers
func (h *AssociationManageHandler) GetManagers(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, false) {
		return
	}
	managers, err := h.repo.ListManagers(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, managers)
}

type promoteAssociationManagerRequest struct {
	UserID uuid.UUID `json:"user_id" binding:"required"`
	Role   string    `json:"role" binding:"required"`
}

// POST /my/associations/:id/managers (owner-only) — target must currently
// follow the association (per product decision: managers are promoted from
// followers).
func (h *AssociationManageHandler) PromoteManager(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, true) {
		return
	}
	var req promoteAssociationManagerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role != domain.AssociationManagerRoleOwner && req.Role != domain.AssociationManagerRoleManager {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'owner' or 'manager'"})
		return
	}

	followers, err := h.associationRepo.GetPublicAssociationFollowers(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	isFollower := false
	for _, f := range followers {
		if f.UserID == req.UserID {
			isFollower = true
			break
		}
	}
	if !isFollower {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User must be following the association before being promoted"})
		return
	}

	if err := h.repo.PromoteManager(c.Request.Context(), associationID, req.UserID, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Promoted"})
}

// DELETE /my/associations/:id/managers/:userId (owner-only) — refuses to
// remove the last remaining owner.
func (h *AssociationManageHandler) RemoveManager(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, true) {
		return
	}

	managers, err := h.repo.ListManagers(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ownerCount := 0
	targetIsOwner := false
	for _, m := range managers {
		if m.Role == domain.AssociationManagerRoleOwner {
			ownerCount++
			if m.UserID == targetUserID {
				targetIsOwner = true
			}
		}
	}
	if targetIsOwner && ownerCount <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove the association's only owner"})
		return
	}

	if err := h.repo.RemoveManager(c.Request.Context(), associationID, targetUserID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Removed"})
}

// POST /my/associations/:id/events — same shape/notify-on-publish as the
// admin's AssociationEventHandler.CreateAssociationEvent, gated by manager
// role instead of the API key.
func (h *AssociationManageHandler) CreateMyAssociationEvent(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, false) {
		return
	}

	var event domain.AssociationEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	event.AssociationID = associationID
	event.CreatedByID = userID
	event.UpdatedByID = userID

	if err := h.db.WithContext(c.Request.Context()).Create(&event).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if event.IsPublished {
		association, err := h.associationRepo.GetAssociationByID(c.Request.Context(), associationID, nil)
		if err == nil {
			if err := notifyAssociationFollowers(c.Request.Context(), h.db, h.notificationService, associationID,
				association.Name, "New event: "+event.Title, userID); err != nil {
				log.Printf("[association event notify] failed to notify followers for event %s: %v", event.ID, err)
			}
		}
	}

	c.JSON(http.StatusCreated, event)
}

type createAssociationPostRequest struct {
	Title    string `json:"title" binding:"required"`
	Body     string `json:"body"`
	ImageURL string `json:"image_url"`
}

// POST /my/associations/:id/posts — always notifies followers.
func (h *AssociationManageHandler) CreateAssociationPost(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, associationID, userID, false) {
		return
	}

	var req createAssociationPostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	post := domain.AssociationPost{
		AssociationID: associationID,
		AuthorID:      userID,
		Title:         req.Title,
		Body:          req.Body,
		ImageURL:      req.ImageURL,
	}
	post.CreatedByID = userID
	post.UpdatedByID = userID

	if err := h.repo.CreateAssociationPost(c.Request.Context(), &post); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	association, err := h.associationRepo.GetAssociationByID(c.Request.Context(), associationID, nil)
	if err == nil {
		if err := notifyAssociationFollowers(c.Request.Context(), h.db, h.notificationService, associationID,
			association.Name, post.Title, userID); err != nil {
			log.Printf("[association post notify] failed to notify followers for post %s: %v", post.ID, err)
		}
	}

	c.JSON(http.StatusCreated, post)
}

// GET /associations/:id/posts — public, reverse-chronological feed.
func (h *AssociationManageHandler) GetPublicAssociationPosts(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	posts, err := h.repo.GetPublicAssociationPosts(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, posts)
}
