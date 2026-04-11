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

	// Set Audit fields if supported and user_id exists
	if auditable, ok := any(&entity).(domain.Auditable); ok {
		if userID, exists := c.Get("user_id"); exists {
			if id, ok := userID.(uuid.UUID); ok {
				auditable.SetCreatedBy(id)
				auditable.SetUpdatedBy(id)
			}
		}
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
	limitStr := c.DefaultQuery("limit", "100")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	// In Go, 0 usually means no limit in many contexts, but we'll set a high number
	if limit <= 0 {
		limit = 1000
	}

	filter := make(map[string]interface{})

	// Add filters
	uuidFilters := []string{"university_id", "department_id", "session_id", "user_id", "uploader_id", "semester_id", "course_category_id", "batch_id"}
	for _, f := range uuidFilters {
		if val := c.Query(f); val != "" {
			filter[f] = val
		}
	}

	// Add string filters
	stringFilters := []string{"course_year", "course_category", "course_code", "name", "slug", "mode", "type", "status", "batch", "year", "blood_group", "scope", "category"}
	for _, f := range stringFilters {
		if val := c.Query(f); val != "" {
			filter[f] = val
		}
	}

	// Add integer filters
	intFilters := []string{"lesson_no", "chapter_no"}
	for _, f := range intFilters {
		if val := c.Query(f); val != "" {
			if intVal, err := strconv.Atoi(val); err == nil {
				filter[f] = intVal
			}
		}
	}

	if search := c.Query("search"); search != "" {
		filter["search"] = search
	}

	// DEBUG: Print filter map
	// fmt.Printf("DEBUG: GetAll Filter for %T: %+v\n", *new(T), filter)

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

	// Set Audit fields if supported and user_id exists
	if auditable, ok := any(&entity).(domain.Auditable); ok {
		if userID, exists := c.Get("user_id"); exists {
			if id, ok := userID.(uuid.UUID); ok {
				auditable.SetUpdatedBy(id)
			}
		}
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
