package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/storage"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ResourceHandler adds review-specific actions on top of GenericHandler.
type ResourceHandler struct {
	*GenericHandler[domain.Resource]
	Usecase             usecase.Usecase[domain.Resource]
	storage             *storage.R2Storage
	db                  *gorm.DB
	notificationService *service.NotificationService
}

func NewResourceHandler(u usecase.Usecase[domain.Resource], s *storage.R2Storage, db *gorm.DB, notificationService *service.NotificationService) *ResourceHandler {
	return &ResourceHandler{
		GenericHandler:      NewGenericHandler[domain.Resource](u),
		Usecase:             u,
		storage:             s,
		db:                  db,
		notificationService: notificationService,
	}
}

// Create binds and persists a resource, then (unless opted out via Notify=false)
// notifies its selected batches once it's actually published. Shadows the
// embedded GenericHandler.Create so the notification side effect can hook in
// without changing that generic bind/audit/respond contract for other entities.
func (h *ResourceHandler) Create(c *gin.Context) {
	var resource domain.Resource
	if err := c.ShouldBindJSON(&resource); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var adminID uuid.UUID
	if userID, exists := c.Get("user_id"); exists {
		if uid, ok := userID.(uuid.UUID); ok {
			adminID = uid
		}
	}
	resource.CreatedByID = adminID
	resource.UpdatedByID = adminID

	// Capture batch IDs before Create — resourceRepository.Create nils out
	// entity.BatchIDs on the same pointer as a side effect once it's used them
	// to populate the resource_batches association.
	notify := resource.Notify == nil || *resource.Notify
	batchIDs := resource.BatchIDs

	if err := h.Usecase.Create(c.Request.Context(), &resource); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if notify && resource.Status == domain.ResourceStatusPublished && len(batchIDs) > 0 {
		if err := h.notifyBatches(c.Request.Context(), resource, batchIDs, adminID); err != nil {
			log.Printf("[resource notify] failed to notify batches for resource %s: %v", resource.ID, err)
		}
	}

	c.JSON(http.StatusCreated, resource)
}

// notifyBatches sends a "new resource" notification to every claimed student
// account in batchIDs. Errors are the caller's to decide whether to surface —
// resource creation/approval should not fail because of this.
func (h *ResourceHandler) notifyBatches(ctx context.Context, resource domain.Resource, batchIDs []string, adminID uuid.UUID) error {
	var userIDs []uuid.UUID
	if err := h.db.WithContext(ctx).
		Table("students").
		Where("batch_id IN ?", batchIDs).
		Where("user_id IS NOT NULL").
		Distinct().
		Pluck("user_id", &userIDs).Error; err != nil {
		return err
	}

	userIDs, err := service.FilterMutedRecipients(ctx, h.db, userIDs, "study_material", "studyMaterial")
	if err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return nil
	}

	actionRoute := fmt.Sprintf("/study/courses/%s/%d?resourceId=%s&universityId=%s&departmentId=%s",
		resource.CourseCode, resource.LessonNo, resource.ID, resource.UniversityID, resource.DepartmentID)
	raw, err := json.Marshal(map[string]interface{}{
		"action_route": actionRoute,
		"resource_id":  resource.ID,
	})
	if err != nil {
		return err
	}
	dataJSON := datatypes.JSON(raw)

	n := domain.Notification{
		Title: resource.Title,
		Body:  fmt.Sprintf("A new %s has been added to %s.", resource.Type, resource.CourseCode),
		Type:  "studyMaterial",
		Scope: "batch",
		Data:  &dataJSON,
	}
	_, _, err = h.notificationService.SendToUsers(ctx, n, userIDs, adminID)
	return err
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

	wasPublished := resource.Status == domain.ResourceStatusPublished
	notify := resource.Notify == nil || *resource.Notify

	// GetByID's Preload(clause.Associations) already populated resource.Batches;
	// capture the IDs now — Update nils out entity.Batches on this same pointer
	// as a side effect once it's done using it.
	batchIDs := make([]string, len(resource.Batches))
	for i, b := range resource.Batches {
		batchIDs[i] = b.ID.String()
	}

	resource.Status = domain.ResourceStatusPublished
	resource.RejectedNote = ""
	now := time.Now()
	resource.ReviewedAt = &now

	var adminID uuid.UUID
	if userID, exists := c.Get("user_id"); exists {
		if uid, ok := userID.(uuid.UUID); ok {
			resource.ReviewedByID = &uid
			adminID = uid
		}
	}

	if err := h.Usecase.Update(c.Request.Context(), resource); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// TODO: FCM — send push notification to resource.UploaderUID
	// notificationService.Send(resource.UploaderUID, "Your submission was approved! 🎉", resource.Title)

	if notify && !wasPublished && len(batchIDs) > 0 {
		if err := h.notifyBatches(c.Request.Context(), *resource, batchIDs, adminID); err != nil {
			log.Printf("[resource notify] failed to notify batches for resource %s: %v", resource.ID, err)
		}
	}

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
	h.incrementCounter(c, "download_count")
}

// IncrementView bumps the view counter atomically.
// POST /resources/:id/view
func (h *ResourceHandler) IncrementView(c *gin.Context) {
	h.incrementCounter(c, "view_count")
}

// incrementCounter does a single atomic SQL increment on the named column and
// returns its new value — avoids the GetByID+Save race of a read-modify-write.
func (h *ResourceHandler) incrementCounter(c *gin.Context, column string) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	if err := h.db.WithContext(c.Request.Context()).
		Model(&domain.Resource{}).Where("id = ?", id).
		UpdateColumn(column, gorm.Expr(column+" + 1")).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var newValue int64
	if err := h.db.WithContext(c.Request.Context()).
		Model(&domain.Resource{}).Where("id = ?", id).
		Select(column).Scan(&newValue).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{column: newValue})
}

// RateResource upserts the caller's 1-5 star rating and recomputes the
// resource's rating_avg/rating_count aggregate from all ratings.
// POST /resources/:id/rate  { "rating": 1-5 }
func (h *ResourceHandler) RateResource(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	userID, exists := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !exists || !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	var body struct {
		Rating int `json:"rating" binding:"required,min=1,max=5"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rating must be an integer between 1 and 5"})
		return
	}

	var ratingAvg float64
	var ratingCount int

	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		rating := domain.ResourceRating{ResourceID: id, UserID: uid, Rating: body.Rating}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "resource_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"rating", "updated_at"}),
		}).Create(&rating).Error; err != nil {
			return err
		}

		if err := tx.Model(&domain.ResourceRating{}).
			Where("resource_id = ?", id).
			Select("COALESCE(AVG(rating), 0)", "COUNT(*)").
			Row().Scan(&ratingAvg, &ratingCount); err != nil {
			return err
		}

		return tx.Model(&domain.Resource{}).Where("id = ?", id).
			Updates(map[string]interface{}{"rating_avg": ratingAvg, "rating_count": ratingCount}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rating_avg":   ratingAvg,
		"rating_count": ratingCount,
		"your_rating":  body.Rating,
	})
}

// GetMyRating returns the caller's own rating for a resource, if any — used
// to pre-fill a star-rating widget. Returns your_rating: null when unrated.
// GET /resources/:id/rating
func (h *ResourceHandler) GetMyRating(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	userID, exists := c.Get("user_id")
	uid, ok := userID.(uuid.UUID)
	if !exists || !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	var rating domain.ResourceRating
	err = h.db.WithContext(c.Request.Context()).
		Where("resource_id = ? AND user_id = ?", id, uid).
		First(&rating).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"your_rating": nil})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"your_rating": rating.Rating})
}
