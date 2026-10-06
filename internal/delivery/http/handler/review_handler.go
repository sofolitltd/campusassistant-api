package handler

import (
	"errors"
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReviewHandler serves product reviews: buyer create/edit/delete, the public
// list, seller replies and admin moderation.
type ReviewHandler struct {
	reviews      *service.ReviewService
	merchantRepo domain.MerchantRepository
}

func NewReviewHandler(reviews *service.ReviewService, merchantRepo domain.MerchantRepository) *ReviewHandler {
	return &ReviewHandler{reviews: reviews, merchantRepo: merchantRepo}
}

type upsertReviewRequest struct {
	Rating  int    `json:"rating" binding:"required"`
	Comment string `json:"comment"`
}

type replyRequest struct {
	Reply string `json:"reply"`
}

type hideRequest struct {
	Hidden bool `json:"hidden"`
}

func writeReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotPurchased):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrReviewNotFound), errors.Is(err, service.ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "Not allowed"})
	case errors.Is(err, service.ErrInvalidRating), errors.Is(err, service.ErrReplyTooLong):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not process the review"})
	}
}

func idParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid " + name})
		return uuid.Nil, false
	}
	return id, true
}

// ListProductReviews: GET /my/products/:id/reviews
func (h *ReviewHandler) ListProductReviews(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	productID, ok := idParam(c, "id")
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	res, err := h.reviews.ListForProduct(c.Request.Context(), userID, productID, limit, offset)
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// UpsertReview: PUT /my/products/:id/review
func (h *ReviewHandler) UpsertReview(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	productID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var req upsertReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r, err := h.reviews.Upsert(c.Request.Context(), userID, productID, req.Rating, req.Comment)
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reviews.Views(c.Request.Context(), []domain.ProductReview{*r}, userID)[0])
}

// DeleteReview: DELETE /my/products/:id/review
func (h *ReviewHandler) DeleteReview(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	productID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.reviews.Delete(c.Request.Context(), userID, productID); err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Review deleted"})
}

// ReviewableItems: GET /my/orders/:id/reviewable
func (h *ReviewHandler) ReviewableItems(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	orderID, ok := idParam(c, "id")
	if !ok {
		return
	}
	items, err := h.reviews.ReviewableForOrder(c.Request.Context(), userID, orderID)
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ReviewHandler) ownedMerchant(c *gin.Context) (*domain.Merchant, bool) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, ok := idParam(c, "id")
	if !ok {
		return nil, false
	}
	m, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return nil, false
	}
	if m.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return nil, false
	}
	return m, true
}

// MerchantReviews: GET /my/merchants/:id/reviews
func (h *ReviewHandler) MerchantReviews(c *gin.Context) {
	m, ok := h.ownedMerchant(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	rows, total, err := h.reviews.ListForMerchant(c.Request.Context(), m.ID, limit, offset)
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reviews":      h.reviews.Views(c.Request.Context(), rows, uuid.Nil),
		"total":        total,
		"rating_avg":   m.RatingAvg,
		"rating_count": m.RatingCount,
	})
}

// ReplyToReview: POST /my/merchants/:id/reviews/:rid/reply
func (h *ReviewHandler) ReplyToReview(c *gin.Context) {
	m, ok := h.ownedMerchant(c)
	if !ok {
		return
	}
	reviewID, ok := idParam(c, "rid")
	if !ok {
		return
	}
	var req replyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r, err := h.reviews.Reply(c.Request.Context(), m.ID, reviewID, req.Reply)
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reviews.Views(c.Request.Context(), []domain.ProductReview{*r}, uuid.Nil)[0])
}

// AdminList: GET /reviews?hidden=true|false
func (h *ReviewHandler) AdminList(c *gin.Context) {
	var hidden *bool
	switch c.Query("hidden") {
	case "true":
		t := true
		hidden = &t
	case "false":
		f := false
		hidden = &f
	}
	limit, offset := pageParams(c)
	rows, total, err := h.reviews.ListAll(c.Request.Context(), hidden, limit, offset)
	if err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reviews": rows, "total": total})
}

// AdminSetHidden: PUT /reviews/:id/hide
func (h *ReviewHandler) AdminSetHidden(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var req hideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.reviews.SetHidden(c.Request.Context(), id, req.Hidden); err != nil {
		writeReviewError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Review updated"})
}
