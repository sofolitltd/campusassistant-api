package handler

import (
	"net/http"
	"strconv"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ProductHandler struct {
	repo         domain.ProductRepository
	merchantRepo domain.MerchantRepository
	wishlist     *service.WishlistService // optional; nil skips saved-product alerts
}

// SetWishlist enables back-in-stock and price-drop alerts to users who saved a product.
func (h *ProductHandler) SetWishlist(w *service.WishlistService) { h.wishlist = w }

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
	before := *product
	if err := c.ShouldBindJSON(product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	product.ID = id
	if err := h.repo.UpdateProduct(c.Request.Context(), product); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update product"})
		return
	}
	h.wishlist.NotifyProductChange(c.Request.Context(), before, *product)
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
// university/department. Optional filters: ?category_id=, ?merchant_id=,
// ?q= (title/description), ?min_price=, ?max_price=, ?in_stock=true,
// ?featured=true (admin-featured only), ?sort=new|price_asc|price_desc|top_rated|popular and ?limit=&offset= for
// paging. The body is always a bare JSON array (older app builds rely on
// that); the match count ignoring paging is in the X-Total-Count header.
func (h *ProductHandler) GetProductsByLocation(c *gin.Context) {
	f := domain.ProductFilter{Query: c.Query("q"), Sort: domain.ProductSort(c.Query("sort"))}
	f.UniversityID, _ = uuid.Parse(c.Query("university_id"))
	f.DepartmentID, _ = uuid.Parse(c.Query("department_id"))
	f.CategoryID, _ = uuid.Parse(c.Query("category_id"))
	f.MerchantID, _ = uuid.Parse(c.Query("merchant_id"))
	f.MinPrice, _ = strconv.Atoi(c.Query("min_price"))
	f.MaxPrice, _ = strconv.Atoi(c.Query("max_price"))
	f.InStock = c.Query("in_stock") == "true"
	f.FeaturedOnly = c.Query("featured") == "true"
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		if l > 100 {
			l = 100
		}
		f.Limit = l
	}
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o > 0 {
		f.Offset = o
	}

	products, total, err := h.repo.SearchProducts(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch products"})
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, products)
}

// resolveOwnedMerchant resolves the merchant named by the :id URL param,
// verifying the JWT user actually owns it and it's approved. Scoped by ID
// rather than derived from the JWT user alone (via GetMerchantByUserID)
// because a user can own several merchants — resolving "the" merchant for
// a user is ambiguous; the caller must say which one they're acting as.
func (h *ProductHandler) resolveOwnedMerchant(c *gin.Context) (*domain.Merchant, bool) {
	userID := c.MustGet("user_id").(uuid.UUID)
	merchantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return nil, false
	}
	merchant, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), merchantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return nil, false
	}
	if merchant.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return nil, false
	}
	if merchant.Status != domain.MerchantStatusApproved {
		c.JSON(http.StatusForbidden, gin.H{"error": "Merchant is not approved yet"})
		return nil, false
	}
	return merchant, true
}

// GetMyProducts lists a merchant's own products (self-service).
func (h *ProductHandler) GetMyProducts(c *gin.Context) {
	merchant, ok := h.resolveOwnedMerchant(c)
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

// CreateMyProduct creates a product owned by the given merchant.
func (h *ProductHandler) CreateMyProduct(c *gin.Context) {
	merchant, ok := h.resolveOwnedMerchant(c)
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

// UpdateMyProduct updates a product, only if it's owned by the given merchant.
func (h *ProductHandler) UpdateMyProduct(c *gin.Context) {
	merchant, ok := h.resolveOwnedMerchant(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("productId"))
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
	before := *existing
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
	h.wishlist.NotifyProductChange(c.Request.Context(), before, *existing)
	c.JSON(http.StatusOK, existing)
}

// DeleteMyProduct deletes a product, only if it's owned by the given merchant.
func (h *ProductHandler) DeleteMyProduct(c *gin.Context) {
	merchant, ok := h.resolveOwnedMerchant(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("productId"))
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
