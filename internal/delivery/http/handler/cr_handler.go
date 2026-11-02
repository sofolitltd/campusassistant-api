package handler

import (
	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CrHandler struct {
	*GenericHandler[domain.CR]
}

func NewCrHandler(u usecase.Usecase[domain.CR], db *gorm.DB) *CrHandler {
	generic := NewGenericHandler(u)
	generic.DB = db
	return &CrHandler{GenericHandler: generic}
}

func (h *CrHandler) Create(c *gin.Context) {
	var cr domain.CR
	if err := c.ShouldBindJSON(&cr); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Usecase.Create(c.Request.Context(), &cr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	audit.RecordRequest(c, h.DB, audit.Event{
		Action: "cr.create", EntityType: "cr", EntityID: cr.ID,
		UniversityID: &cr.UniversityID, DepartmentID: &cr.DepartmentID, BatchID: &cr.BatchID,
		Changes: map[string]any{"created": cr},
	})
	c.JSON(http.StatusCreated, cr)
}
