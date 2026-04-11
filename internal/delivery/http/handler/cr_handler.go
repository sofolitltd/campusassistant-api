package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"net/http"

	"github.com/gin-gonic/gin"
)

type CrHandler struct {
	*GenericHandler[domain.CR]
}

func NewCrHandler(u usecase.Usecase[domain.CR]) *CrHandler {
	return &CrHandler{
		GenericHandler: NewGenericHandler(u),
	}
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

	c.JSON(http.StatusCreated, cr)
}
