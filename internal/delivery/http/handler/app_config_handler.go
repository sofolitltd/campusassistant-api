package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// appConfigSingletonID is the fixed row every read/write targets — there is
// only ever one AppConfig record.
const appConfigSingletonID = 1

type AppConfigHandler struct {
	db *gorm.DB
}

func NewAppConfigHandler(db *gorm.DB) *AppConfigHandler {
	return &AppConfigHandler{db: db}
}

func (h *AppConfigHandler) GetAppConfig(c *gin.Context) {
	var config domain.AppConfig
	if err := h.db.FirstOrCreate(&config, domain.AppConfig{ID: appConfigSingletonID}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch app config", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, config)
}

func (h *AppConfigHandler) UpdateAppConfig(c *gin.Context) {
	var input struct {
		MinSupportedVersionCode int    `json:"min_supported_version_code"`
		UpdateMessage           string `json:"update_message"`
		PlayStoreURL            string `json:"play_store_url"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	config := domain.AppConfig{
		ID:                      appConfigSingletonID,
		MinSupportedVersionCode: input.MinSupportedVersionCode,
		UpdateMessage:           input.UpdateMessage,
		PlayStoreURL:            input.PlayStoreURL,
	}
	if err := h.db.Save(&config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update app config", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, config)
}
