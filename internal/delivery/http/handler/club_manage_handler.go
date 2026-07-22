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

// ClubManageHandler backs the JWT-protected "/my/clubs" self-service
// surface — a club's own requester/officers editing their club without
// admin-panel access. Distinct from ClubHandler (public reads + the
// simple Follow/Join relations) and from the admin-only API-key-gated
// /clubs CRUD.
type ClubManageHandler struct {
	repo                domain.ClubManagementRepository
	clubRepo            domain.ClubRepository
	db                  *gorm.DB
	notificationService *service.NotificationService
}

func NewClubManageHandler(repo domain.ClubManagementRepository, clubRepo domain.ClubRepository, db *gorm.DB, notificationService *service.NotificationService) *ClubManageHandler {
	return &ClubManageHandler{repo: repo, clubRepo: clubRepo, db: db, notificationService: notificationService}
}

// requireManager 403s and returns false if userID has no ClubManager row
// for clubID, or (when ownerOnly) isn't specifically the owner.
func (h *ClubManageHandler) requireManager(c *gin.Context, clubID, userID uuid.UUID, ownerOnly bool) bool {
	role, err := h.repo.GetManagerRole(c.Request.Context(), clubID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if role == "" || (ownerOnly && role != domain.ClubManagerRoleOwner) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to manage this club"})
		return false
	}
	return true
}

// GET /my/clubs
func (h *ClubManageHandler) GetMyClubs(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	clubs, err := h.repo.GetMyClubs(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, clubs)
}

// clubUpdatableFields is the whitelist UpdateMyClub binds onto the existing
// row — deliberately excludes IsActive/IsVerified, which stay
// admin-controlled via the separate /clubs/:id admin route.
type clubUpdatableFields struct {
	Name         *string `json:"name"`
	Description  *string                `json:"description"`
	LogoURL      *string                `json:"logo_url"`
	BannerURL    *string                `json:"banner_url"`
	FoundedYear  *int                   `json:"founded_year"`
	Category     *string                `json:"category"`
	ContactEmail *string                `json:"contact_email"`
	ContactPhone *string                `json:"contact_phone"`
	SocialLinks  map[string]interface{} `json:"social_links"`
}

// PUT /my/clubs/:id
func (h *ClubManageHandler) UpdateMyClub(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, false) {
		return
	}

	var fields clubUpdatableFields
	if err := c.ShouldBindJSON(&fields); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	club, err := h.clubRepo.GetClubByID(c.Request.Context(), clubID, nil)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Club not found"})
		return
	}
	if fields.Name != nil {
		club.Name = *fields.Name
	}
	if fields.Description != nil {
		club.Description = *fields.Description
	}
	if fields.LogoURL != nil {
		club.LogoURL = fields.LogoURL
	}
	if fields.BannerURL != nil {
		club.BannerURL = fields.BannerURL
	}
	if fields.FoundedYear != nil {
		club.FoundedYear = fields.FoundedYear
	}
	if fields.Category != nil {
		club.Category = *fields.Category
	}
	if fields.ContactEmail != nil {
		club.ContactEmail = fields.ContactEmail
	}
	if fields.ContactPhone != nil {
		club.ContactPhone = fields.ContactPhone
	}
	if fields.SocialLinks != nil {
		raw, err := json.Marshal(fields.SocialLinks)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid social_links"})
			return
		}
		club.SocialLinks = datatypes.JSON(raw)
	}
	club.UpdatedByID = userID

	if err := h.repo.UpdateMyClub(c.Request.Context(), club); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, club)
}

// GET /my/clubs/:id/followers — the pool eligible for promotion.
func (h *ClubManageHandler) GetFollowersForPromotion(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, false) {
		return
	}
	followers, err := h.clubRepo.GetPublicClubFollowers(c.Request.Context(), clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, followers)
}

// GET /my/clubs/:id/managers
func (h *ClubManageHandler) GetManagers(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, false) {
		return
	}
	managers, err := h.repo.ListManagers(c.Request.Context(), clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, managers)
}

type promoteManagerRequest struct {
	UserID uuid.UUID `json:"user_id" binding:"required"`
	Role   string    `json:"role" binding:"required"`
}

// POST /my/clubs/:id/managers (owner-only) — target must currently follow
// the club (per product decision: managers are promoted from followers).
func (h *ClubManageHandler) PromoteManager(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, true) {
		return
	}
	var req promoteManagerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role != domain.ClubManagerRoleOwner && req.Role != domain.ClubManagerRoleManager {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'owner' or 'manager'"})
		return
	}

	followers, err := h.clubRepo.GetPublicClubFollowers(c.Request.Context(), clubID)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "User must be following the club before being promoted"})
		return
	}

	if err := h.repo.PromoteManager(c.Request.Context(), clubID, req.UserID, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Promoted"})
}

// DELETE /my/clubs/:id/managers/:userId (owner-only) — refuses to remove
// the last remaining owner, so a club can never end up with zero owners.
func (h *ClubManageHandler) RemoveManager(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, true) {
		return
	}

	managers, err := h.repo.ListManagers(c.Request.Context(), clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ownerCount := 0
	targetIsOwner := false
	for _, m := range managers {
		if m.Role == domain.ClubManagerRoleOwner {
			ownerCount++
			if m.UserID == targetUserID {
				targetIsOwner = true
			}
		}
	}
	if targetIsOwner && ownerCount <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove the club's only owner"})
		return
	}

	if err := h.repo.RemoveManager(c.Request.Context(), clubID, targetUserID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Removed"})
}

// POST /my/clubs/:id/events — same shape/notify-on-publish as the admin's
// ClubEventHandler.CreateClubEvent, gated by manager role instead of the
// API key.
func (h *ClubManageHandler) CreateMyClubEvent(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, false) {
		return
	}

	var event domain.ClubEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	event.ClubID = clubID
	event.CreatedByID = userID
	event.UpdatedByID = userID

	if err := h.db.WithContext(c.Request.Context()).Create(&event).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if event.IsPublished {
		club, err := h.clubRepo.GetClubByID(c.Request.Context(), clubID, nil)
		if err == nil {
			if err := notifyClubFollowers(c.Request.Context(), h.db, h.notificationService, clubID,
				club.Name, "New event: "+event.Title, userID); err != nil {
				log.Printf("[club event notify] failed to notify followers for event %s: %v", event.ID, err)
			}
		}
	}

	c.JSON(http.StatusCreated, event)
}

type createClubPostRequest struct {
	Title    string `json:"title" binding:"required"`
	Body     string `json:"body"`
	ImageURL string `json:"image_url"`
}

// POST /my/clubs/:id/posts — always notifies followers (unlike events,
// there's no draft/publish toggle for posts; posting is the publish
// action).
func (h *ClubManageHandler) CreateClubPost(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if !h.requireManager(c, clubID, userID, false) {
		return
	}

	var req createClubPostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	post := domain.ClubPost{
		ClubID:   clubID,
		AuthorID: userID,
		Title:    req.Title,
		Body:     req.Body,
		ImageURL: req.ImageURL,
	}
	post.CreatedByID = userID
	post.UpdatedByID = userID

	if err := h.repo.CreateClubPost(c.Request.Context(), &post); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	club, err := h.clubRepo.GetClubByID(c.Request.Context(), clubID, nil)
	if err == nil {
		if err := notifyClubFollowers(c.Request.Context(), h.db, h.notificationService, clubID,
			club.Name, post.Title, userID); err != nil {
			log.Printf("[club post notify] failed to notify followers for post %s: %v", post.ID, err)
		}
	}

	c.JSON(http.StatusCreated, post)
}

// GET /clubs/:id/posts — public, reverse-chronological feed for the
// Notifications tab.
func (h *ClubManageHandler) GetPublicClubPosts(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	posts, err := h.repo.GetPublicClubPosts(c.Request.Context(), clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, posts)
}
