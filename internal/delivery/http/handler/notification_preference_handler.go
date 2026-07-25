package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type NotificationPreferenceHandler struct {
	db           *gorm.DB
	topicService *service.DeviceTopicService
}

func NewNotificationPreferenceHandler(db *gorm.DB, topicService *service.DeviceTopicService) *NotificationPreferenceHandler {
	return &NotificationPreferenceHandler{db: db, topicService: topicService}
}

// GetMyPreferences returns all 9 notification categories, true unless the
// caller has an explicit NotificationMute row for it (absence = subscribed).
func (h *NotificationPreferenceHandler) GetMyPreferences(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var mutedCategories []string
	if err := h.db.WithContext(c.Request.Context()).
		Model(&domain.NotificationMute{}).
		Where("user_id = ?", userID).
		Pluck("category", &mutedCategories).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	muted := make(map[string]struct{}, len(mutedCategories))
	for _, cat := range mutedCategories {
		muted[cat] = struct{}{}
	}

	prefs := make(map[string]bool, len(domain.NotificationCategories))
	for _, cat := range domain.NotificationCategories {
		_, isMuted := muted[cat]
		prefs[cat] = !isMuted
	}

	c.JSON(http.StatusOK, prefs)
}

type updateNotificationPreferenceRequest struct {
	Enabled bool `json:"enabled"`
}

// UpdatePreference toggles one category on/off for the caller. Disabling a
// geographic category (university/department/batch) also synchronously
// re-reconciles FCM topic subscriptions on every one of the user's devices,
// so the change takes effect immediately rather than on next app resume.
func (h *NotificationPreferenceHandler) UpdatePreference(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	category := c.Param("category")

	if !domain.IsValidNotificationCategory(category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category"})
		return
	}

	var req updateNotificationPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	var err error
	if req.Enabled {
		err = h.db.WithContext(ctx).
			Where("user_id = ? AND category = ?", userID, category).
			Delete(&domain.NotificationMute{}).Error
	} else {
		mute := domain.NotificationMute{UserID: userID, Category: category}
		mute.ID = uuid.New()
		mute.CreatedByID = userID
		mute.UpdatedByID = userID
		err = h.db.WithContext(ctx).
			Where("user_id = ? AND category = ?", userID, category).
			FirstOrCreate(&mute).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if domain.IsGeographicNotificationCategory(category) {
		var devices []domain.UserDevice
		if err := h.db.WithContext(ctx).Where("user_id = ?", userID).Find(&devices).Error; err == nil {
			for _, d := range devices {
				h.topicService.ReconcileTopics(ctx, userID, d.FCMToken)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"category": category, "enabled": req.Enabled})
}
