package handler

import (
	"errors"
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type EntitlementHandler struct {
	svc *service.EntitlementService
}

func NewEntitlementHandler(svc *service.EntitlementService) *EntitlementHandler {
	return &EntitlementHandler{svc: svc}
}

// MyEntitlements returns what the current user may use — the app should
// gate UI from this rather than from a bare is_pro flag.
func (h *EntitlementHandler) MyEntitlements(c *gin.Context) {
	e, err := h.svc.ForUser(c.Request.Context(), c.MustGet("user_id").(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load entitlements"})
		return
	}
	c.JSON(http.StatusOK, e)
}

// ─── Admin: per-plan entitlements ────────────────────────────────────────

func (h *EntitlementHandler) GetPlanEntitlements(c *gin.Context) {
	planID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid plan id"})
		return
	}
	rows, err := h.svc.PlanEntitlements(c.Request.Context(), planID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load entitlements"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entitlements": rows, "default_when_empty": domain.DefaultProFeatures})
}

type setPlanEntitlementsBody struct {
	Entitlements []struct {
		Feature string `json:"feature"`
		Limit   int    `json:"limit"`
		Period  string `json:"period"`
	} `json:"entitlements"`
}

// SetPlanEntitlements replaces the plan's entitlements ({"entitlements": []}
// restores the default of every Pro feature, unlimited).
func (h *EntitlementHandler) SetPlanEntitlements(c *gin.Context) {
	planID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid plan id"})
		return
	}
	var body setPlanEntitlementsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	items := make([]domain.PlanEntitlement, 0, len(body.Entitlements))
	for _, e := range body.Entitlements {
		items = append(items, domain.PlanEntitlement{Feature: e.Feature, Limit: e.Limit, Period: e.Period})
	}
	saved, err := h.svc.SetPlanEntitlements(c.Request.Context(), planID, items)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPlanNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Plan not found"})
		case errors.Is(err, service.ErrInvalidEntitlement):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save entitlements"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"entitlements": saved})
}
