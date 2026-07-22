package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
)

// BDLocationHandler serves the static BD district/upazila list — no repo,
// since domain.BDDistricts is an in-memory constant, not a DB table.
type BDLocationHandler struct{}

func NewBDLocationHandler() *BDLocationHandler {
	return &BDLocationHandler{}
}

// GET /bd-districts
func (h *BDLocationHandler) GetAll(c *gin.Context) {
	c.JSON(http.StatusOK, domain.BDDistricts)
}
