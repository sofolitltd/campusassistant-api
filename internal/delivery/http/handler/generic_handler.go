package handler

import (
	"net/http"
	"strconv"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type GenericHandler[T any] struct {
	Usecase usecase.Usecase[T]
}

func NewGenericHandler[T any](u usecase.Usecase[T]) *GenericHandler[T] {
	return &GenericHandler[T]{Usecase: u}
}

func (h *GenericHandler[T]) Create(c *gin.Context) {
	var entity T
	if err := c.ShouldBindJSON(&entity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Usecase.Create(c.Request.Context(), &entity); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, entity)
}

func (h *GenericHandler[T]) GetByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	entity, err := h.Usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	c.JSON(http.StatusOK, entity)
}

func (h *GenericHandler[T]) GetAll(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "10")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	filter := make(map[string]interface{})
	if uid := c.Query("university_id"); uid != "" {
		filter["university_id"] = uid
	}
	if did := c.Query("department_id"); did != "" {
		filter["department_id"] = did
	}
	// Add other common filters if needed (e.g. session_id, batch_id)
	if sid := c.Query("session_id"); sid != "" {
		filter["session_id"] = sid
	}
	if bid := c.Query("batch_id"); bid != "" {
		filter["batch_id"] = bid
	}

	entities, count, err := h.Usecase.GetAll(c.Request.Context(), filter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":   entities,
		"count":  count,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *GenericHandler[T]) Update(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	var entity T
	if err := c.ShouldBindJSON(&entity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set ID if supported
	if setter, ok := any(&entity).(domain.Entity); ok {
		setter.SetID(id)
	}

	if err := h.Usecase.Update(c.Request.Context(), &entity); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, entity)
}

func (h *GenericHandler[T]) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if err := h.Usecase.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Deleted successfully"})
}
