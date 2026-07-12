package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/storage"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ResourceHandler adds review-specific actions on top of GenericHandler.
type ResourceHandler struct {
	*GenericHandler[domain.Resource]
	Usecase usecase.Usecase[domain.Resource]
	storage *storage.R2Storage
}

func NewResourceHandler(u usecase.Usecase[domain.Resource], s *storage.R2Storage) *ResourceHandler {
	return &ResourceHandler{
		GenericHandler: NewGenericHandler[domain.Resource](u),
		Usecase:        u,
		storage:        s,
	}
}

// Delete handles both soft delete (default) and permanent delete (?permanent=true).
// Permanent delete removes files from R2 and hard-deletes the DB row.
// DELETE /resources/:id
func (h *ResourceHandler) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if c.Query("permanent") == "true" {
		resource, err := h.Usecase.GetByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
			return
		}

		if h.storage != nil {
			if resource.FileURL != "" {
				if err := h.storage.DeleteFile(c.Request.Context(), resource.FileURL); err != nil {
					c.Error(err)
				}
			}
			if resource.ThumbnailURL != "" {
				if err := h.storage.DeleteFile(c.Request.Context(), resource.ThumbnailURL); err != nil {
					c.Error(err)
				}
			}
		}

		if err := h.Usecase.HardDelete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Permanently deleted"})
	} else {
		if err := h.Usecase.Delete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Deleted successfully"})
	}
}

// ApproveResource sets status to "published".
// PATCH /resources/:id/approve
func (h *ResourceHandler) ApproveResource(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	resource, err := h.Usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
		return
	}

	resource.Status = domain.ResourceStatusPublished
	resource.RejectedNote = ""
	now := time.Now()
	resource.ReviewedAt = &now

	// Set reviewer if JWT user_id is present
	if userID, exists := c.Get("user_id"); exists {
		if uid, ok := userID.(uuid.UUID); ok {
			resource.ReviewedByID = &uid
		}
	}

	if err := h.Usecase.Update(c.Request.Context(), resource); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// TODO: FCM — send push notification to resource.UploaderUID
	// notificationService.Send(resource.UploaderUID, "Your submission was approved! 🎉", resource.Title)

	c.JSON(http.StatusOK, resource)
}

// RejectResource sets status to "rejected" with an admin-provided reason.
// PATCH /resources/:id/reject
func (h *ResourceHandler) RejectResource(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Rejection reason is required"})
		return
	}

	resource, err := h.Usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
		return
	}

	resource.Status = domain.ResourceStatusRejected
	resource.RejectedNote = body.Reason
	now := time.Now()
	resource.ReviewedAt = &now

	if userID, exists := c.Get("user_id"); exists {
		if uid, ok := userID.(uuid.UUID); ok {
			resource.ReviewedByID = &uid
		}
	}

	if err := h.Usecase.Update(c.Request.Context(), resource); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// TODO: FCM — send push notification to resource.UploaderUID
	// notificationService.Send(resource.UploaderUID, "Your submission needs revision ❌", body.Reason)

	c.JSON(http.StatusOK, resource)
}

// IncrementDownload bumps the download counter atomically.
// POST /resources/:id/download
func (h *ResourceHandler) IncrementDownload(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	resource, err := h.Usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
		return
	}

	resource.DownloadCount++

	if err := h.Usecase.Update(c.Request.Context(), resource); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"download_count": resource.DownloadCount})
}
