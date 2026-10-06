package handler

import (
	"net/http"
	"strconv"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type FeedbackHandler struct {
	repo domain.FeedbackRepository
}

func NewFeedbackHandler(repo domain.FeedbackRepository) *FeedbackHandler {
	return &FeedbackHandler{repo: repo}
}

type createFeedbackRequest struct {
	Category string `json:"category"`
	Subject  string `json:"subject" binding:"required"`
	Message  string `json:"message" binding:"required"`
}

func (h *FeedbackHandler) Create(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var req createFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	category := domain.FeedbackCategory(req.Category)
	if category == "" {
		category = domain.FeedbackGeneral
	}

	f := &domain.Feedback{
		UserID:   userID,
		Category: category,
		Subject:  req.Subject,
		Message:  req.Message,
		Status:   domain.FeedbackPending,
	}

	if err := h.repo.Create(c.Request.Context(), f); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit feedback: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, f)
}

func (h *FeedbackHandler) GetMyFeedbacks(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	items, err := h.repo.GetByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Feedback{}
	}
	c.JSON(http.StatusOK, items)
}

// Admin endpoints

func (h *FeedbackHandler) GetAll(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	filter := map[string]interface{}{}
	if status := c.Query("status"); status != "" {
		filter["status"] = status
	}
	if category := c.Query("category"); category != "" {
		filter["category"] = category
	}

	items, count, err := h.repo.GetAll(c.Request.Context(), filter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Feedback{}
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "count": count, "limit": limit, "offset": offset})
}

func (h *FeedbackHandler) GetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	f, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Feedback not found"})
		return
	}
	c.JSON(http.StatusOK, f)
}

type updateFeedbackRequest struct {
	Status    *string `json:"status"`
	AdminReply *string `json:"admin_reply"`
}

func (h *FeedbackHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	f, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Feedback not found"})
		return
	}

	if req.Status != nil {
		f.Status = domain.FeedbackStatus(*req.Status)
	}
	if req.AdminReply != nil {
		adminID := c.MustGet("user_id").(uuid.UUID)
		f.AdminReply = *req.AdminReply
		f.RepliedByID = &adminID
		now := time.Now()
		f.RepliedAt = &now
	}

	if err := h.repo.Update(c.Request.Context(), f); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, f)
}
