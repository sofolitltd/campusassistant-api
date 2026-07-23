package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AddressHandler struct {
	repo domain.AddressRepository
}

func NewAddressHandler(repo domain.AddressRepository) *AddressHandler {
	return &AddressHandler{repo: repo}
}

// ListMyAddresses returns the current user's saved addresses.
func (h *AddressHandler) ListMyAddresses(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	addresses, err := h.repo.GetByUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch addresses"})
		return
	}
	c.JSON(http.StatusOK, addresses)
}

// CreateAddress saves a new address for the current user.
func (h *AddressHandler) CreateAddress(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	var address domain.Address
	if err := c.ShouldBindJSON(&address); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	address.UserID = userID
	if err := h.repo.Create(c.Request.Context(), &address); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create address"})
		return
	}
	c.JSON(http.StatusOK, address)
}

// UpdateAddress updates an address, ownership-checked.
func (h *AddressHandler) UpdateAddress(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid address id"})
		return
	}

	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Address not found"})
		return
	}
	if existing.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this address"})
		return
	}

	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing.ID = id
	existing.UserID = userID

	if err := h.repo.Update(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update address"})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DeleteAddress deletes an address, ownership-checked.
func (h *AddressHandler) DeleteAddress(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid address id"})
		return
	}

	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Address not found"})
		return
	}
	if existing.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this address"})
		return
	}

	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete address"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Address deleted"})
}

// SetDefaultAddress makes an address the user's default, transactionally
// unsetting any other default first.
func (h *AddressHandler) SetDefaultAddress(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid address id"})
		return
	}

	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Address not found"})
		return
	}
	if existing.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this address"})
		return
	}

	if err := h.repo.SetDefault(c.Request.Context(), userID, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to set default address"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Default address updated"})
}
