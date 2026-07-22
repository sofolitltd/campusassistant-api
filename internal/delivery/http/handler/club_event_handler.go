package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClubEventHandler is dedicated (not GenericHandler[T]) because publishing
// an event needs to fan out a push notification to the club's followers —
// the same "inline call right after the write" pattern ResourceHandler uses
// for study-material notifications.
type ClubEventHandler struct {
	db                  *gorm.DB
	notificationService *service.NotificationService
}

func NewClubEventHandler(db *gorm.DB, notificationService *service.NotificationService) *ClubEventHandler {
	return &ClubEventHandler{db: db, notificationService: notificationService}
}

// GetAllClubEvents powers the admin panel's per-club event manager.
// GET /club-events?club_id=X — every event regardless of publish status.
func (h *ClubEventHandler) GetAllClubEvents(c *gin.Context) {
	clubID, err := uuid.Parse(c.Query("club_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "club_id is required"})
		return
	}
	var events []domain.ClubEvent
	if err := h.db.WithContext(c.Request.Context()).
		Where("club_id = ?", clubID).
		Order("start_at asc").
		Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": events})
}

// GetPublicClubEvents is the app-facing listing for a single club's details
// page: published, upcoming-first only. GET /clubs/:id/events
func (h *ClubEventHandler) GetPublicClubEvents(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	var events []domain.ClubEvent
	if err := h.db.WithContext(c.Request.Context()).
		Where("club_id = ? AND is_published = ? AND start_at >= ?", clubID, true, time.Now()).
		Order("start_at asc").
		Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, events)
}

// POST /club-events
func (h *ClubEventHandler) CreateClubEvent(c *gin.Context) {
	var event domain.ClubEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var adminID uuid.UUID
	if userID, exists := c.Get("user_id"); exists {
		if uid, ok := userID.(uuid.UUID); ok {
			adminID = uid
		}
	}
	event.CreatedByID = adminID
	event.UpdatedByID = adminID

	if err := h.db.WithContext(c.Request.Context()).Create(&event).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if event.IsPublished {
		if err := h.notifyFollowers(c.Request.Context(), event, adminID); err != nil {
			log.Printf("[club event notify] failed to notify followers for event %s: %v", event.ID, err)
		}
	}

	c.JSON(http.StatusCreated, event)
}

// notifyFollowers sends a "new club event" push to every user following
// event.ClubID.
func (h *ClubEventHandler) notifyFollowers(ctx context.Context, event domain.ClubEvent, adminID uuid.UUID) error {
	var club domain.Club
	if err := h.db.WithContext(ctx).First(&club, "id = ?", event.ClubID).Error; err != nil {
		return err
	}
	return notifyClubFollowers(ctx, h.db, h.notificationService, event.ClubID,
		club.Name, fmt.Sprintf("New event: %s", event.Title), adminID)
}

// PUT /club-events/:id — plain update, republishing an already-published
// event does not re-notify (only the initial Create path does).
func (h *ClubEventHandler) UpdateClubEvent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}
	var event domain.ClubEvent
	if err := h.db.WithContext(c.Request.Context()).First(&event, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Event not found"})
		return
	}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	event.ID = id
	if err := h.db.WithContext(c.Request.Context()).Save(&event).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, event)
}

// DELETE /club-events/:id
func (h *ClubEventHandler) DeleteClubEvent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}
	if err := h.db.WithContext(c.Request.Context()).Delete(&domain.ClubEvent{}, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Event deleted"})
}
