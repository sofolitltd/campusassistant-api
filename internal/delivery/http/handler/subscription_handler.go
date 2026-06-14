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

func (h *SubscriptionHandler) GetUserSubscription(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("uid"))

	sub, err := h.repo.GetUserSubscription(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusOK, nil) // Return null if no subscription found
		return
	}

	c.JSON(http.StatusOK, sub)
}

// Admin Methods
func (h *SubscriptionHandler) GetAllPlans(c *gin.Context) {
	plans, err := h.repo.GetAllPlans(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch all plans"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": plans})
}

func (h *SubscriptionHandler) CreatePlan(c *gin.Context) {
	var plan domain.SubscriptionPlan
	if err := c.ShouldBindJSON(&plan); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreatePlan(c.Request.Context(), &plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create plan"})
		return
	}
	c.JSON(http.StatusOK, plan)
}

func (h *SubscriptionHandler) UpdatePlan(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	var plan domain.SubscriptionPlan
	if err := c.ShouldBindJSON(&plan); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	plan.ID = id
	if err := h.repo.UpdatePlan(c.Request.Context(), &plan); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update plan"})
		return
	}
	c.JSON(http.StatusOK, plan)
}

func (h *SubscriptionHandler) DeletePlan(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	if err := h.repo.DeletePlan(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete plan"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Plan deleted"})
}

func (h *SubscriptionHandler) GetAllSubscriptions(c *gin.Context) {
	subs, err := h.repo.GetAllSubscriptions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch subscriptions"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": subs})
}

func (h *SubscriptionHandler) CreateSubscription(c *gin.Context) {
	var sub domain.UserSubscription
	if err := c.ShouldBindJSON(&sub); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreateUserSubscription(c.Request.Context(), &sub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create subscription"})
		return
	}
	c.JSON(http.StatusOK, sub)
}
