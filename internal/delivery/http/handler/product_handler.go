package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ProductHandler struct {
	repo         domain.ProductRepository
	merchantRepo domain.MerchantRepository
}

func NewProductHandler(repo domain.ProductRepository, merchantRepo domain.MerchantRepository) *ProductHandler {
	return &ProductHandler{repo: repo, merchantRepo: merchantRepo}
}

// GetAllProducts is the admin listing — every product, any publish status,
// optionally filtered by ?merchant_id=.
func (h *ProductHandler) GetAllProducts(c *gin.Context) {
	merchantID, _ := uuid.Parse(c.Query("merchant_id"))
	products, err := h.repo.GetAllProducts(c.Request.Context(), merchantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch products"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": products})
}

func (h *ProductHandler) GetProductByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product id"})
		return
	}
	product, err := h.repo.GetProductByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	c.JSON(http.StatusOK, product)
}

// CreateProduct is the admin-panel create (any merchant_id, including the
// synthetic platform merchant for in-house products).
func (h *ProductHandler) CreateProduct(c *gin.Context) {
	var product domain.Product
	if err := c.ShouldBindJSON(&product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreateProduct(c.Request.Context(), &product); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create product"})
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *ProductHandler) UpdateProduct(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product id"})
		return
	}
	// Load the existing row and bind JSON onto it (not a blank struct) so a
	// partial payload doesn't zero out fields the caller omitted — same
	// reasoning as MerchantHandler.UpdateMerchant.
	product, err := h.repo.GetProductByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	if err := c.ShouldBindJSON(product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	product.ID = id
	if err := h.repo.UpdateProduct(c.Request.Context(), product); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update product"})
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *ProductHandler) DeleteProduct(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product id"})
		return
	}
	if err := h.repo.DeleteProduct(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete product"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Product deleted"})
}

// GetProductsByLocation is the app-facing browse endpoint: published
// products that are global (no targets) or targeted to this
// university/department. Optional ?category_id= filters by category.
func (h *ProductHandler) GetProductsByLocation(c *gin.Context) {
	universityID, _ := uuid.Parse(c.Query("university_id"))
	departmentID, _ := uuid.Parse(c.Query("department_id"))
	categoryID, _ := uuid.Parse(c.Query("category_id"))

	products, err := h.repo.GetProductsByLocation(c.Request.Context(), universityID, departmentID, categoryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch products"})
		return
	}
	c.JSON(http.StatusOK, products)
}

// currentMerchant resolves the JWT user's own approved merchant profile, or
// aborts the request with an appropriate error.
func (h *ProductHandler) currentMerchant(c *gin.Context) (*domain.Merchant, bool) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchant, err := h.merchantRepo.GetMerchantByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "No merchant profile found"})
		return nil, false
	}
	if merchant.Status != domain.MerchantStatusApproved {
		c.JSON(http.StatusForbidden, gin.H{"error": "Merchant is not approved yet"})
		return nil, false
	}
	return merchant, true
}

// GetMyProducts lists the current merchant's own products (self-service).
func (h *ProductHandler) GetMyProducts(c *gin.Context) {
	merchant, ok := h.currentMerchant(c)
	if !ok {
		return
	}
	products, err := h.repo.GetAllProducts(c.Request.Context(), merchant.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch products"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": products})
}

// CreateMyProduct creates a product owned by the current merchant.
func (h *ProductHandler) CreateMyProduct(c *gin.Context) {
	merchant, ok := h.currentMerchant(c)
	if !ok {
		return
	}
	var product domain.Product
	if err := c.ShouldBindJSON(&product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	product.MerchantID = merchant.ID
	if err := h.repo.CreateProduct(c.Request.Context(), &product); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create product"})
		return
	}
	c.JSON(http.StatusOK, product)
}

// UpdateMyProduct updates a product, only if it's owned by the current merchant.
func (h *ProductHandler) UpdateMyProduct(c *gin.Context) {
	merchant, ok := h.currentMerchant(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product id"})
		return
	}
	existing, err := h.repo.GetProductByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	if existing.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this product"})
		return
	}

	// Bind JSON onto the already-fetched row (not a blank struct) so a
	// partial payload doesn't zero out fields the caller omitted.
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing.ID = id
	existing.MerchantID = merchant.ID
	if err := h.repo.UpdateProduct(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update product"})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DeleteMyProduct deletes a product, only if it's owned by the current merchant.
func (h *ProductHandler) DeleteMyProduct(c *gin.Context) {
	merchant, ok := h.currentMerchant(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid product id"})
		return
	}
	existing, err := h.repo.GetProductByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	if existing.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this product"})
		return
	}
	if err := h.repo.DeleteProduct(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete product"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Product deleted"})
}
