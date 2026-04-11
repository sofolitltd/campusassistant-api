package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SubscriptionHandler struct {
	repo domain.SubscriptionRepository
}

func NewSubscriptionHandler(repo domain.SubscriptionRepository) *SubscriptionHandler {
	return &SubscriptionHandler{repo: repo}
}

func (h *SubscriptionHandler) GetPlans(c *gin.Context) {
	universityID, _ := uuid.Parse(c.Query("university_id"))
	departmentID, _ := uuid.Parse(c.Query("department_id"))

	plans, err := h.repo.GetPlansByLocation(c.Request.Context(), universityID, departmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch plans"})
		return
	}

	c.JSON(http.StatusOK, plans)
}

func (h *SubscriptionHandler) GetFeatures(c *gin.Context) {
	universityID, _ := uuid.Parse(c.Query("university_id"))
	departmentID, _ := uuid.Parse(c.Query("department_id"))

	features, err := h.repo.GetFeaturesByLocation(c.Request.Context(), universityID, departmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch features"})
		return
	}

	c.JSON(http.StatusOK, features)
}

func (h *SubscriptionHandler) GetUserSubscription(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("uid"))

	sub, err := h.repo.GetUserSubscription(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusOK, nil) // Return null if no subscription found
		return
	}

	c.JSON(http.StatusOK, sub)
}
