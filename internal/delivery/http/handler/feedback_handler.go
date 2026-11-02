package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/telegram"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type FeedbackHandler struct {
	repo           domain.FeedbackRepository
	telegramClient *telegram.Client
}

func NewFeedbackHandler(repo domain.FeedbackRepository, telegramClient *telegram.Client) *FeedbackHandler {
	return &FeedbackHandler{repo: repo, telegramClient: telegramClient}
}

type createFeedbackRequest struct {
	Category  string   `json:"category"`
	Subject   string   `json:"subject" binding:"required"`
	Message   string   `json:"message" binding:"required"`
	ImageURLs []string `json:"image_urls"`
}

// validFeedbackImageURLs checks the screenshot URLs the app got back from
// /upload. Returns a user-facing message, or "" when valid.
func validFeedbackImageURLs(urls []string) string {
	if len(urls) > domain.FeedbackMaxImages {
		return fmt.Sprintf("At most %d images are allowed", domain.FeedbackMaxImages)
	}
	for _, u := range urls {
		if len(u) > 500 || !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
			return "Invalid image URL"
		}
	}
	return ""
}

func (h *FeedbackHandler) Create(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var req createFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if msg := validFeedbackImageURLs(req.ImageURLs); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	category := domain.FeedbackCategory(req.Category)
	if category == "" {
		category = domain.FeedbackGeneral
	}

	f := &domain.Feedback{
		UserID:    userID,
		Category:  category,
		Subject:   req.Subject,
		Message:   req.Message,
		ImageURLs: pq.StringArray(req.ImageURLs),
		Status:    domain.FeedbackPending,
	}
	if f.ImageURLs == nil {
		f.ImageURLs = pq.StringArray{}
	}

	if err := h.repo.Create(c.Request.Context(), f); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit feedback: " + err.Error()})
		return
	}
	h.notifyTelegram(f.ID)
	c.JSON(http.StatusCreated, f)
}

// notifyTelegram alerts admins about new feedback. Fire-and-forget like the
// other Telegram alerts: a slow or unreachable Telegram must never delay or
// fail the submission.
func (h *FeedbackHandler) notifyTelegram(id uuid.UUID) {
	if h.telegramClient == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Re-read with the user preloaded so the alert can say who sent it.
		f, err := h.repo.GetByID(ctx, id)
		if err != nil {
			log.Printf("[feedback] telegram notify: load %s: %v", id, err)
			return
		}

		from := "unknown user"
		if u := f.User; u != nil {
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			from = fmt.Sprintf("%s <%s> (%s)", name, u.Email, u.Role)
		}
		text := fmt.Sprintf(
			"New feedback [%s]\nFrom: %s\nSubject: %s\n\n%s",
			f.Category, from, f.Subject, f.Message,
		)
		if n := len(f.ImageURLs); n > 0 {
			text += fmt.Sprintf("\n\nScreenshots: %d", n)
		}
		// Telegram rejects messages over 4096 characters.
		if r := []rune(text); len(r) > 4000 {
			text = string(r[:4000]) + "…"
		}
		if err := h.telegramClient.SendMessage(ctx, text); err != nil {
			log.Printf("[feedback] telegram notify failed: %v", err)
			return
		}

		if err := h.telegramClient.SendPhotos(ctx, f.ImageURLs); err != nil {
			// Telegram couldn't fetch the images (e.g. a non-public URL) —
			// fall back to sending the links so they're not lost.
			log.Printf("[feedback] telegram photos failed: %v", err)
			_ = h.telegramClient.SendMessage(ctx, "Screenshots:\n"+strings.Join(f.ImageURLs, "\n"))
		}
	}()
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
	Status     *string `json:"status"`
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
