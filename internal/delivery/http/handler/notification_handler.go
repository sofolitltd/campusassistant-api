package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/delivery/http/websocket"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type NotificationHandler struct {
	db                  *gorm.DB
	notificationService *service.NotificationService
	notifHub            *websocket.NotificationHub
}

func NewNotificationHandler(db *gorm.DB, notificationService *service.NotificationService, notifHub *websocket.NotificationHub) *NotificationHandler {
	return &NotificationHandler{db: db, notificationService: notificationService, notifHub: notifHub}
}

// notificationTargetRequest is one row of a scope=custom audience: the
// deepest ID set on it wins — BatchID if present, else DepartmentID, else
// UniversityID — same convention as domain.BannerTarget/ContactTarget.
type notificationTargetRequest struct {
	UniversityID string `json:"university_id"`
	DepartmentID string `json:"department_id,omitempty"`
	BatchID      string `json:"batch_id,omitempty"`
}

type createNotificationRequest struct {
	Title    string                      `json:"title" binding:"required"`
	Body     string                      `json:"body" binding:"required"`
	Type     string                      `json:"type" binding:"required"`
	ImageURL string                      `json:"image_url,omitempty"` // optional big-picture image shown in the push notification
	Data     map[string]interface{}      `json:"data,omitempty"`
	Scope    string                      `json:"scope" binding:"required"` // "user" | "batch" | "department" | "university" | "custom"
	TargetID string                      `json:"target_id"`                // user_id, batch_id, department_id, or university_id; omit with scope=university to broadcast to every user
	Targets  []notificationTargetRequest `json:"targets,omitempty"`        // scope=custom only — an arbitrary mix of universities/departments/batches
}

// validationError marks a recipient-resolution failure as caused by bad request input (HTTP 400),
// as opposed to an underlying DB error (HTTP 500).
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

// resolveRecipients returns the user IDs a notification should be created for based on req.Scope/TargetID.
// For scope=university, an empty TargetID broadcasts to every claimed student account.
func (h *NotificationHandler) resolveRecipients(ctx context.Context, req createNotificationRequest) ([]uuid.UUID, error) {
	switch req.Scope {
	case "user":
		if req.TargetID == "" {
			return nil, &validationError{"target_id is required when scope is user"}
		}
		targetID, err := uuid.Parse(req.TargetID)
		if err != nil {
			return nil, &validationError{"invalid target_id"}
		}
		return []uuid.UUID{targetID}, nil

	case "batch", "department":
		if req.TargetID == "" {
			return nil, &validationError{"target_id is required when scope is " + req.Scope}
		}
		var ids []uuid.UUID
		column := req.Scope + "_id"
		if err := h.db.WithContext(ctx).
			Table("students").
			Where(column+" = ?", req.TargetID).
			Where("user_id IS NOT NULL").
			Pluck("user_id", &ids).Error; err != nil {
			return nil, err
		}
		return ids, nil

	case "university":
		var ids []uuid.UUID
		query := h.db.WithContext(ctx).Table("students").Where("user_id IS NOT NULL")
		if req.TargetID != "" {
			query = query.Where("university_id = ?", req.TargetID)
		}
		if err := query.Pluck("user_id", &ids).Error; err != nil {
			return nil, err
		}
		return ids, nil

	case "custom":
		if len(req.Targets) == 0 {
			return nil, &validationError{"targets is required when scope is custom"}
		}
		conds := make([]string, 0, len(req.Targets))
		args := make([]interface{}, 0, len(req.Targets))
		for _, t := range req.Targets {
			switch {
			case t.BatchID != "":
				conds = append(conds, "batch_id = ?")
				args = append(args, t.BatchID)
			case t.DepartmentID != "":
				conds = append(conds, "department_id = ?")
				args = append(args, t.DepartmentID)
			case t.UniversityID != "":
				conds = append(conds, "university_id = ?")
				args = append(args, t.UniversityID)
			default:
				return nil, &validationError{"each target needs at least university_id"}
			}
		}
		var ids []uuid.UUID
		if err := h.db.WithContext(ctx).
			Table("students").
			Where("user_id IS NOT NULL").
			Where(strings.Join(conds, " OR "), args...).
			Pluck("user_id", &ids).Error; err != nil {
			return nil, err
		}
		return ids, nil

	default:
		return nil, &validationError{"invalid scope. Use: user, batch, department, university, or custom"}
	}
}

// broadcastTopics returns the FCM topics a notification should be published
// to for req, or nil if this scope should keep using per-token push
// (scope=user — low volume, wants per-recipient delivery info) rather than a
// topic. "university" with an empty target_id means "every user" and maps to
// the "all" topic every device is always subscribed to; scope=custom maps
// each target row to its own topic and dedupes. See
// DeviceTopicService.desiredTopics for the matching subscribe-side logic.
func broadcastTopics(req createNotificationRequest) []string {
	switch req.Scope {
	case "batch", "department":
		if req.TargetID == "" {
			return nil
		}
		return []string{req.Scope + "_" + req.TargetID}
	case "university":
		if req.TargetID == "" {
			return []string{"all"}
		}
		return []string{"university_" + req.TargetID}
	case "custom":
		seen := make(map[string]struct{}, len(req.Targets))
		topics := make([]string, 0, len(req.Targets))
		for _, t := range req.Targets {
			var topic string
			switch {
			case t.BatchID != "":
				topic = "batch_" + t.BatchID
			case t.DepartmentID != "":
				topic = "department_" + t.DepartmentID
			case t.UniversityID != "":
				topic = "university_" + t.UniversityID
			default:
				continue
			}
			if _, ok := seen[topic]; !ok {
				seen[topic] = struct{}{}
				topics = append(topics, topic)
			}
		}
		return topics
	default:
		return nil
	}
}

// createNotifications resolves recipients for req and fans the notification out to them via
// NotificationService, recording scope/target_id on the content row for admin-list display.
func (h *NotificationHandler) createNotifications(c *gin.Context, req createNotificationRequest, adminID uuid.UUID) {
	userIDs, err := h.resolveRecipients(c.Request.Context(), req)
	if err != nil {
		var verr *validationError
		status := http.StatusInternalServerError
		if errors.As(err, &verr) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	if len(userIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "No users found for the given scope", "count": 0})
		return
	}

	var dataJSON *datatypes.JSON
	if req.Data != nil {
		raw, err := json.Marshal(req.Data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize data"})
			return
		}
		j := datatypes.JSON(raw)
		dataJSON = &j
	}

	var targetID *uuid.UUID
	if req.TargetID != "" {
		parsed, err := uuid.Parse(req.TargetID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target_id"})
			return
		}
		targetID = &parsed
	}

	n := domain.Notification{
		Title:    req.Title,
		Body:     req.Body,
		Type:     req.Type,
		Scope:    req.Scope,
		TargetID: targetID,
		ImageURL: req.ImageURL,
		Data:     dataJSON,
	}

	// Note: userIDs is intentionally NOT run through
	// service.FilterMutedRecipients here. scope=user admin sends are a
	// targeted 1:1 message (support-style DM), not one of the 9 subscribable
	// notification_preference categories — muting must never silently
	// swallow direct admin communication. Broadcast scopes (batch/department/
	// university/custom) are filtered at the FCM topic layer instead (see
	// DeviceTopicService.desiredTopics), and every other push path
	// (club/association/lost_found/career/marketplace/study_material) is
	// filtered at its own call site before reaching NotificationService.
	var sent *domain.Notification
	var recipients []domain.NotificationRecipient
	if topics := broadcastTopics(req); len(topics) > 0 {
		sent, recipients, err = h.notificationService.SendToUsersViaTopics(c.Request.Context(), n, userIDs, adminID, topics)
	} else {
		sent, recipients, err = h.notificationService.SendToUsers(c.Request.Context(), n, userIDs, adminID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	count := len(recipients)

	// Broadcast to connected WebSocket clients. Each user gets their OWN
	// NotificationRecipient.ID (not the shared Notification content-row ID)
	// as "id", because that's what the REST API actually operates on
	// (GetNotifications/MarkAsRead/DeleteNotification are all scoped to
	// NotificationRecipient rows) — sending the content-row ID here used to
	// make mark-as-read/delete 404 for any client acting on a WS-delivered id.
	if h.notifHub != nil && sent != nil {
		go func() {
			for _, r := range recipients {
				payload := map[string]interface{}{
					"type":    "new_notification",
					"channel": "notifications",
					"data": map[string]interface{}{
						"id":         r.ID.String(),
						"title":      sent.Title,
						"body":       sent.Body,
						"type":       sent.Type,
						"image_url":  sent.ImageURL,
						"data":       sent.Data,
						"created_at": sent.CreatedAt,
						"is_read":    false,
					},
				}
				raw, _ := json.Marshal(payload)
				h.notifHub.SendToUser(r.UserID, raw)
			}
		}()
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Notifications sent",
		"count":   count,
	})
}

func (h *NotificationHandler) CreateNotification(c *gin.Context) {
	var req createNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	adminID := c.MustGet("user_id").(uuid.UUID)
	h.createNotifications(c, req, adminID)
}

// Admin endpoints — no JWT required, just API key

func (h *NotificationHandler) CreateAdminNotification(c *gin.Context) {
	var req createNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.createNotifications(c, req, uuid.Nil)
}

// PreviewAdminNotification resolves the audience for req without sending
// anything — same recipient-resolution logic as the real send, so the
// admin dashboard can show "this will reach N users" before an admin
// commits to what is otherwise an irreversible mass broadcast.
func (h *NotificationHandler) PreviewAdminNotification(c *gin.Context) {
	var req createNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userIDs, err := h.resolveRecipients(c.Request.Context(), req)
	if err != nil {
		var verr *validationError
		status := http.StatusInternalServerError
		if errors.As(err, &verr) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"count": len(userIDs)})
}

// flatNotification is the shape the mobile app expects from GET /notifications:
// content fields flattened together with this user's own read state, keyed by
// their own NotificationRecipient row id (so mark-as-read/delete stay scoped to them).
type flatNotification struct {
	ID        uuid.UUID       `json:"id"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Type      string          `json:"type"`
	ImageURL  string          `json:"image_url,omitempty"`
	IsRead    bool            `json:"is_read"`
	Data      *datatypes.JSON `json:"data,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// GetNotifications is paginated (?limit=&offset=, via the shared
// parseLimitOffset default of 20/0, capped at 100) — a user's inbox
// otherwise grows unbounded (one row per broadcast they were ever a
// recipient of) and this endpoint would eventually return their entire
// history on every app open.
func (h *NotificationHandler) GetNotifications(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	limit, offset := parseLimitOffset(c)

	var count int64
	if err := h.db.WithContext(c.Request.Context()).
		Table("notification_recipients").
		Where("user_id = ?", userID).
		Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	results := make([]flatNotification, 0)
	if err := h.db.WithContext(c.Request.Context()).
		Table("notification_recipients AS nr").
		Select("nr.id AS id, n.title, n.body, n.type, n.image_url, nr.is_read, n.data, n.created_at").
		Joins("JOIN notifications n ON n.id = nr.notification_id").
		Where("nr.user_id = ?", userID).
		Order("n.created_at DESC").
		Limit(limit).
		Offset(offset).
		Scan(&results).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": results, "count": count, "limit": limit, "offset": offset})
}

func (h *NotificationHandler) MarkAsRead(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	notifID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notification ID"})
		return
	}

	result := h.db.WithContext(c.Request.Context()).
		Model(&domain.NotificationRecipient{}).
		Where("id = ? AND user_id = ?", notifID, userID).
		Updates(map[string]interface{}{"is_read": true, "read_at": time.Now()})

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Marked as read"})
}

func (h *NotificationHandler) MarkAllAsRead(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	if err := h.db.WithContext(c.Request.Context()).
		Model(&domain.NotificationRecipient{}).
		Where("user_id = ? AND is_read = ?", userID, false).
		Updates(map[string]interface{}{"is_read": true, "read_at": time.Now()}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "All marked as read"})
}

func (h *NotificationHandler) DeleteNotification(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	notifID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notification ID"})
		return
	}

	result := h.db.WithContext(c.Request.Context()).
		Unscoped().
		Where("id = ? AND user_id = ?", notifID, userID).
		Delete(&domain.NotificationRecipient{})

	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Notification deleted"})
}

// adminNotificationRow is a Notification content row plus its recipient/read
// counts, so the admin list shows one row per "send" with real reach numbers
// instead of one row per recipient.
type adminNotificationRow struct {
	domain.Notification
	RecipientCount int64 `json:"recipient_count"`
	ReadCount      int64 `json:"read_count"`
}

// GetAllAdminNotifications is paginated (?limit=&offset=) — both the content
// row list and its recipient/read-count aggregation used to be unbounded
// full-table scans that grew with total historical send volume; scoping the
// count query to just the current page's notification IDs (instead of a
// GROUP BY over the entire notification_recipients table every load) is a
// real efficiency win on top of the pagination itself.
func (h *NotificationHandler) GetAllAdminNotifications(c *gin.Context) {
	limit, offset := parseLimitOffset(c)

	var total int64
	if err := h.db.WithContext(c.Request.Context()).
		Model(&domain.Notification{}).
		Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var notifications []domain.Notification
	if err := h.db.WithContext(c.Request.Context()).
		Order("created_at desc").
		Limit(limit).
		Offset(offset).
		Find(&notifications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	notificationIDs := make([]uuid.UUID, len(notifications))
	for i, n := range notifications {
		notificationIDs[i] = n.ID
	}

	type recipientCount struct {
		NotificationID uuid.UUID
		Total          int64
		Read           int64
	}
	var counts []recipientCount
	if len(notificationIDs) > 0 {
		if err := h.db.WithContext(c.Request.Context()).
			Table("notification_recipients").
			Select("notification_id, COUNT(*) AS total, COUNT(*) FILTER (WHERE is_read) AS read").
			Where("notification_id IN ?", notificationIDs).
			Group("notification_id").
			Scan(&counts).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	countByID := make(map[uuid.UUID]recipientCount, len(counts))
	for _, rc := range counts {
		countByID[rc.NotificationID] = rc
	}

	rows := make([]adminNotificationRow, 0, len(notifications))
	for _, n := range notifications {
		rc := countByID[n.ID]
		rows = append(rows, adminNotificationRow{
			Notification:   n,
			RecipientCount: rc.Total,
			ReadCount:      rc.Read,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": rows, "count": total, "limit": limit, "offset": offset})
}

func (h *NotificationHandler) DeleteAdminNotification(c *gin.Context) {
	notifID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid notification ID"})
		return
	}

	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("notification_id = ?", notifID).Delete(&domain.NotificationRecipient{}).Error; err != nil {
			return err
		}
		result := tx.Unscoped().Where("id = ?", notifID).Delete(&domain.Notification{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "Notification deleted"})
	}
}
