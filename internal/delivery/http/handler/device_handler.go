package handler

import (
	"net/http"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeviceHandler registers/unregisters FCM push tokens for the current user,
// and (via jwtManager) handles session-wide logout actions — see
// SESSION_MANAGEMENT.md.
type DeviceHandler struct {
	db         *gorm.DB
	jwtManager *auth.JWTManager
}

func NewDeviceHandler(db *gorm.DB, jwtManager *auth.JWTManager) *DeviceHandler {
	return &DeviceHandler{db: db, jwtManager: jwtManager}
}

type registerDeviceRequest struct {
	FCMToken string `json:"fcm_token" binding:"required"`
	Platform string `json:"platform" binding:"required"` // "android" | "ios" | "web"
}

// RegisterDevice upserts by fcm_token — the token belongs to one app install,
// so re-registering it (e.g. a different user logging into the same device)
// reassigns ownership instead of creating a duplicate row.
// POST /devices
func (h *DeviceHandler) RegisterDevice(c *gin.Context) {
	var req registerDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	device := domain.UserDevice{
		UserID:     userID,
		FCMToken:   req.FCMToken,
		Platform:   req.Platform,
		LastSeenAt: time.Now(),
	}
	device.ID = uuid.New()
	device.CreatedByID = userID
	device.UpdatedByID = userID

	err := h.db.WithContext(c.Request.Context()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "fcm_token"}},
			DoUpdates: clause.AssignmentColumns([]string{"user_id", "platform", "last_seen_at", "updated_by_id", "updated_at"}),
		}).
		Create(&device).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Device registered"})
}

type deviceResponse struct {
	ID         uuid.UUID `json:"id"`
	Platform   string    `json:"platform"`
	LastSeenAt time.Time `json:"last_seen_at"`
	IsCurrent  bool      `json:"is_current"`
}

// ListDevices returns all devices registered to the current user, most
// recently seen first. Pass ?current_fcm_token= (the app's own FCM token)
// so the UI can flag which row is this device.
// GET /devices
func (h *DeviceHandler) ListDevices(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	currentToken := c.Query("current_fcm_token")

	var devices []domain.UserDevice
	if err := h.db.WithContext(c.Request.Context()).
		Where("user_id = ?", userID).
		Order("last_seen_at DESC").
		Find(&devices).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows := make([]deviceResponse, len(devices))
	for i, d := range devices {
		rows[i] = deviceResponse{
			ID:         d.ID,
			Platform:   d.Platform,
			LastSeenAt: d.LastSeenAt,
			IsCurrent:  currentToken != "" && d.FCMToken == currentToken,
		}
	}

	c.JSON(http.StatusOK, rows)
}

// RemoveDevice deletes one of the caller's own device rows, scoped to
// user_id so a client can't remove another user's device by guessing an id.
// DELETE /devices/:id
func (h *DeviceHandler) RemoveDevice(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid device id"})
		return
	}

	if err := h.db.WithContext(c.Request.Context()).
		Unscoped().
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&domain.UserDevice{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Device removed"})
}

type unregisterDeviceRequest struct {
	FCMToken string `json:"fcm_token" binding:"required"`
}

// UnregisterDevice deletes the caller's own device row for a token — scoped to
// user_id so a client can't unregister another user's device by guessing a token.
// POST /devices/unregister
func (h *DeviceHandler) UnregisterDevice(c *gin.Context) {
	var req unregisterDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.db.WithContext(c.Request.Context()).
		Unscoped().
		Where("fcm_token = ? AND user_id = ?", req.FCMToken, userID).
		Delete(&domain.UserDevice{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Device unregistered"})
}

// LogoutAll bumps the user's TokenVersion (instantly invalidating every
// access token issued so far, on every device) and wipes all their device
// rows so push stops everywhere too.
// POST /devices/logout-all
func (h *DeviceHandler) LogoutAll(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.User{}).Where("id = ?", userID).
			UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&domain.UserDevice{}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Logged out of all devices"})
}

type logoutOthersRequest struct {
	// CurrentFCMToken is optional — if provided, this device's own UserDevice
	// row survives the wipe so it keeps receiving push without needing to
	// re-register. Omit it to wipe every device including this one.
	CurrentFCMToken string `json:"current_fcm_token,omitempty"`
}

// LogoutOthers bumps TokenVersion (invalidating every existing access token,
// including this request's) then immediately issues a fresh one so the
// caller's own session survives — every other device's token silently stops
// working the moment it's next used.
// POST /devices/logout-others
func (h *DeviceHandler) LogoutOthers(c *gin.Context) {
	var req logoutOthersRequest
	_ = c.ShouldBindJSON(&req) // body is optional

	userID := c.MustGet("user_id").(uuid.UUID)

	var user domain.User
	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.User{}).Where("id = ?", userID).
			UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
			return err
		}

		devices := tx.Where("user_id = ?", userID)
		if req.CurrentFCMToken != "" {
			devices = devices.Where("fcm_token != ?", req.CurrentFCMToken)
		}
		if err := devices.Delete(&domain.UserDevice{}).Error; err != nil {
			return err
		}

		return tx.First(&user, "id = ?", userID).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	accessToken, err := h.jwtManager.GenerateAccessToken(
		user.ID, user.Email, string(user.Role), user.UniversityID, user.DepartmentID, user.TokenVersion,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Logged out of all other devices",
		"access_token": accessToken,
	})
}
