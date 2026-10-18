package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AccessGrantHandler is the admin CRUD for access grants (launch offers,
// partnerships, ambassador perks). Resolution lives in
// service.ResolveGrantPerks.
type AccessGrantHandler struct {
	db *gorm.DB
}

func NewAccessGrantHandler(db *gorm.DB) *AccessGrantHandler {
	return &AccessGrantHandler{db: db}
}

type accessGrantRequest struct {
	Label       string     `json:"label"`
	ScopeType   string     `json:"scope_type"`
	ScopeID     *string    `json:"scope_id"`
	AdFree      bool       `json:"ad_free"`
	CoinFree    bool       `json:"coin_free"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	NewUserDays int        `json:"new_user_days"`
}

// accessGrantView adds display fields the admin list needs.
type accessGrantView struct {
	domain.AccessGrant
	ScopeName    string `json:"scope_name"`
	UsersInScope int64  `json:"users_in_scope"`
	IsActive     bool   `json:"is_active"`
}

func (h *AccessGrantHandler) build(c *gin.Context, req accessGrantRequest, g *domain.AccessGrant) error {
	g.Label = strings.TrimSpace(req.Label)
	if g.Label == "" || len(g.Label) > 120 {
		return errors.New("label is required (max 120 characters)")
	}
	if !req.AdFree && !req.CoinFree {
		return errors.New("pick at least one perk")
	}
	if req.NewUserDays < 0 {
		return errors.New("new_user_days must be 0 or more")
	}

	g.ScopeType, g.ScopeID = req.ScopeType, nil
	switch req.ScopeType {
	case domain.GrantScopeAll:
	case domain.GrantScopeUniversity, domain.GrantScopeDepartment, domain.GrantScopeBatch, domain.GrantScopeUser:
		if req.ScopeID == nil {
			return errors.New("scope_id is required for this scope")
		}
		id, err := uuid.Parse(*req.ScopeID)
		if err != nil {
			return errors.New("invalid scope_id")
		}
		if h.scopeName(c, req.ScopeType, id) == "" {
			return errors.New("scope_id not found")
		}
		g.ScopeID = &id
	default:
		return errors.New("scope_type must be all, university, department, batch or user")
	}

	g.AdFree, g.CoinFree, g.NewUserDays = req.AdFree, req.CoinFree, req.NewUserDays
	g.StartsAt = time.Now()
	if req.StartsAt != nil {
		g.StartsAt = *req.StartsAt
	}
	g.EndsAt = req.EndsAt
	if g.EndsAt != nil && !g.EndsAt.After(g.StartsAt) {
		return errors.New("ends_at must be after starts_at")
	}
	return nil
}

// scopeName resolves a scope id to a display name ("" when not found).
func (h *AccessGrantHandler) scopeName(c *gin.Context, scopeType string, id uuid.UUID) string {
	db := h.db.WithContext(c.Request.Context())
	switch scopeType {
	case domain.GrantScopeUniversity:
		var u domain.University
		if db.Select("id", "name").First(&u, "id = ?", id).Error == nil {
			return u.Name
		}
	case domain.GrantScopeDepartment:
		var d domain.Department
		if db.Preload("University").First(&d, "id = ?", id).Error == nil {
			if d.University != nil {
				return d.Name + " · " + d.University.Name
			}
			return d.Name
		}
	case domain.GrantScopeBatch:
		var b domain.Batch
		if db.Preload("Department").First(&b, "id = ?", id).Error == nil {
			if b.Department != nil {
				return b.Name + " · " + b.Department.Name
			}
			return b.Name
		}
	case domain.GrantScopeUser:
		var u domain.User
		if db.Select("id", "first_name", "last_name", "email").First(&u, "id = ?", id).Error == nil {
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if name == "" {
				return u.Email
			}
			return name + " (" + u.Email + ")"
		}
	}
	return ""
}

// usersInScope counts registered accounts the scope covers, ignoring
// new_user_days.
func (h *AccessGrantHandler) usersInScope(c *gin.Context, g domain.AccessGrant) int64 {
	db := h.db.WithContext(c.Request.Context()).Model(&domain.User{})
	var n int64
	switch g.ScopeType {
	case domain.GrantScopeAll:
		db.Count(&n)
	case domain.GrantScopeUser:
		n = 1
	case domain.GrantScopeUniversity:
		db.Where("users.university_id = ? OR EXISTS (SELECT 1 FROM students s WHERE s.user_id = users.id AND s.university_id = ? AND s.deleted_at IS NULL)", g.ScopeID, g.ScopeID).Count(&n)
	case domain.GrantScopeDepartment:
		db.Where("users.department_id = ? OR EXISTS (SELECT 1 FROM students s WHERE s.user_id = users.id AND s.department_id = ? AND s.deleted_at IS NULL)", g.ScopeID, g.ScopeID).Count(&n)
	case domain.GrantScopeBatch:
		db.Where("EXISTS (SELECT 1 FROM students s WHERE s.user_id = users.id AND s.batch_id = ? AND s.deleted_at IS NULL)", g.ScopeID).Count(&n)
	}
	return n
}

func (h *AccessGrantHandler) view(c *gin.Context, g domain.AccessGrant, now time.Time) accessGrantView {
	v := accessGrantView{
		AccessGrant:  g,
		ScopeName:    "Everyone",
		UsersInScope: h.usersInScope(c, g),
		IsActive:     !g.StartsAt.After(now) && (g.EndsAt == nil || g.EndsAt.After(now)),
	}
	if g.ScopeID != nil {
		v.ScopeName = h.scopeName(c, g.ScopeType, *g.ScopeID)
	}
	return v
}

// GET /access-grants
func (h *AccessGrantHandler) List(c *gin.Context) {
	var grants []domain.AccessGrant
	if err := h.db.WithContext(c.Request.Context()).Order("created_at desc").Find(&grants).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load access grants"})
		return
	}
	now := time.Now()
	out := make([]accessGrantView, 0, len(grants))
	for _, g := range grants {
		out = append(out, h.view(c, g, now))
	}
	c.JSON(http.StatusOK, out)
}

// POST /access-grants
func (h *AccessGrantHandler) Create(c *gin.Context) {
	var req accessGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var g domain.AccessGrant
	if err := h.build(c, req, &g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if adminID, ok := c.Get("user_id"); ok {
		if id, ok := adminID.(uuid.UUID); ok {
			g.CreatedBy = &id
		}
	}
	if err := h.db.WithContext(c.Request.Context()).Create(&g).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create access grant"})
		return
	}
	c.JSON(http.StatusCreated, h.view(c, g, time.Now()))
}

// PUT /access-grants/:id — full replace of the editable fields.
func (h *AccessGrantHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	db := h.db.WithContext(c.Request.Context())
	var g domain.AccessGrant
	if err := db.First(&g, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Access grant not found"})
		return
	}
	var req accessGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Keep the original start unless the admin sends a new one.
	if req.StartsAt == nil {
		req.StartsAt = &g.StartsAt
	}
	if err := h.build(c, req, &g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Select("*") so clearing ends_at (nil) and false perks are written.
	if err := db.Model(&g).Select("*").Omit("id", "created_at", "created_by").Updates(&g).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update access grant"})
		return
	}
	c.JSON(http.StatusOK, h.view(c, g, time.Now()))
}

// POST /access-grants/:id/end — ends a grant now, keeping it for history.
func (h *AccessGrantHandler) End(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	now := time.Now()
	res := h.db.WithContext(c.Request.Context()).Model(&domain.AccessGrant{}).
		Where("id = ? AND (ends_at IS NULL OR ends_at > ?)", id, now).
		Update("ends_at", now)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to end access grant"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ended": res.RowsAffected > 0})
}

// DELETE /access-grants/:id
func (h *AccessGrantHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if err := h.db.WithContext(c.Request.Context()).Delete(&domain.AccessGrant{}, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete access grant"})
		return
	}
	// A JSON body, not 204: the admin client always parses the response.
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
