package handler

import (
	"errors"
	"net/http"
	"strings"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// WishlistHandler serves the signed-in user's saved products.
type WishlistHandler struct {
	wishlist *service.WishlistService
	products domain.ProductRepository
}

func NewWishlistHandler(w *service.WishlistService, products domain.ProductRepository) *WishlistHandler {
	return &WishlistHandler{wishlist: w, products: products}
}

// List: GET /my/wishlist — the saved products, newest first.
func (h *WishlistHandler) List(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	products, err := h.wishlist.List(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load wishlist"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": products})
}

// IDs: GET /my/wishlist/ids — just the saved product ids.
func (h *WishlistHandler) IDs(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	ids, err := h.wishlist.IDs(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load wishlist"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ids})
}

// Add: PUT /my/wishlist/:id
func (h *WishlistHandler) Add(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	productID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.wishlist.Add(c.Request.Context(), userID, productID); err != nil {
		if errors.Is(err, service.ErrProductNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save product"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Saved"})
}

// Remove: DELETE /my/wishlist/:id
func (h *WishlistHandler) Remove(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	productID, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.wishlist.Remove(c.Request.Context(), userID, productID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove product"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Removed"})
}

// Lookup: GET /products-lookup?ids=a,b,c — current data for up to 50 products,
// so the app can revalidate a saved cart (price, stock, still on sale). Ids
// that are missing or unpublished are simply absent from the result.
func (h *WishlistHandler) Lookup(c *gin.Context) {
	var ids []uuid.UUID
	for _, part := range strings.Split(c.Query("ids"), ",") {
		if id, err := uuid.Parse(strings.TrimSpace(part)); err == nil {
			ids = append(ids, id)
		}
		if len(ids) == 50 {
			break
		}
	}
	products, err := h.products.GetPublishedByIDs(c.Request.Context(), ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch products"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": products})
}
