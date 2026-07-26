package handler

import (
	"net/http"
	"strconv"
	"strings"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
)

// SearchHandler is dedicated (not GenericHandler[T]) since it fans a single
// request out across many entity types rather than mapping to one
// domain.Repository[T].
type SearchHandler struct {
	repo domain.SearchRepository
}

func NewSearchHandler(repo domain.SearchRepository) *SearchHandler {
	return &SearchHandler{repo: repo}
}

// Search powers global search across resources, notices, courses, clubs,
// associations, teachers, staff, marketplace products, lost & found items,
// and career circulars.
// GET /search
func (h *SearchHandler) Search(c *gin.Context) {
	query := c.Query("q")

	var types []string
	if raw := c.Query("types"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
	}

	limitPerType := 5
	if raw := c.Query("limit_per_type"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limitPerType = v
		}
	}

	results, err := h.repo.Search(
		c.Request.Context(),
		query,
		types,
		c.Query("university_id"),
		c.Query("department_id"),
		limitPerType,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}
