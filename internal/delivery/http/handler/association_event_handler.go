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

// AssociationEventHandler mirrors ClubEventHandler — publishing an event
// fans out a push notification to the association's followers.
type AssociationEventHandler struct {
	db                  *gorm.DB
	notificationService *service.NotificationService
}

func NewAssociationEventHandler(db *gorm.DB, notificationService *service.NotificationService) *AssociationEventHandler {
	return &AssociationEventHandler{db: db, notificationService: notificationService}
}

// GetAllAssociationEvents powers the admin panel's per-association event
// manager. GET /association-events?association_id=X
func (h *AssociationEventHandler) GetAllAssociationEvents(c *gin.Context) {
	associationID, err := uuid.Parse(c.Query("association_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "association_id is required"})
		return
	}
	var events []domain.AssociationEvent
	if err := h.db.WithContext(c.Request.Context()).
		Where("association_id = ?", associationID).
		Order("start_at asc").
		Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": events})
}

// GetPublicAssociationEvents is the app-facing listing for a single
// association's details page. GET /associations/:id/events
func (h *AssociationEventHandler) GetPublicAssociationEvents(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	var events []domain.AssociationEvent
	if err := h.db.WithContext(c.Request.Context()).
		Where("association_id = ? AND is_published = ? AND start_at >= ?", associationID, true, time.Now()).
		Order("start_at asc").
		Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, events)
}

// POST /association-events
func (h *AssociationEventHandler) CreateAssociationEvent(c *gin.Context) {
	var event domain.AssociationEvent
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
			log.Printf("[association event notify] failed to notify followers for event %s: %v", event.ID, err)
		}
	}

	c.JSON(http.StatusCreated, event)
}

// notifyFollowers sends a "new association event" push to every user
// following event.AssociationID.
func (h *AssociationEventHandler) notifyFollowers(ctx context.Context, event domain.AssociationEvent, adminID uuid.UUID) error {
	var association domain.Association
	if err := h.db.WithContext(ctx).First(&association, "id = ?", event.AssociationID).Error; err != nil {
		return err
	}
	return notifyAssociationFollowers(ctx, h.db, h.notificationService, event.AssociationID,
		association.Name, fmt.Sprintf("New event: %s", event.Title), adminID)
}

// PUT /association-events/:id — plain update, republishing an
// already-published event does not re-notify.
func (h *AssociationEventHandler) UpdateAssociationEvent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}
	var event domain.AssociationEvent
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

// DELETE /association-events/:id
func (h *AssociationEventHandler) DeleteAssociationEvent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event ID"})
		return
	}
	if err := h.db.WithContext(c.Request.Context()).Delete(&domain.AssociationEvent{}, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Event deleted"})
}
