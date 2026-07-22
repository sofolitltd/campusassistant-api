package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClubHandler is dedicated (not GenericHandler[T]) because Club needs the
// follow/unfollow/suggest actions and per-request IsFollowing state that the
// generic CRUD handler has no concept of.
type ClubHandler struct {
	repo domain.ClubRepository
}

func NewClubHandler(repo domain.ClubRepository) *ClubHandler {
	return &ClubHandler{repo: repo}
}

func requestingUserID(c *gin.Context) *uuid.UUID {
	if v, exists := c.Get("user_id"); exists {
		if uid, ok := v.(uuid.UUID); ok {
			return &uid
		}
	}
	return nil
}

// GetAllClubs powers the admin panel: unfiltered by default (including
// inactive/pending-approval clubs) so admins can find and review them.
// GET /clubs
func (h *ClubHandler) GetAllClubs(c *gin.Context) {
	filters := domain.ClubFilters{ClubType: c.Query("club_type"), Category: c.Query("category"), RequestingUserID: requestingUserID(c)}
	if v := c.Query("university_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			filters.UniversityID = &id
		}
	}
	if v := c.Query("department_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			filters.DepartmentID = &id
		}
	}
	if v := c.Query("is_active"); v == "true" {
		filters.ActiveOnly = true
	}

	clubs, err := h.repo.GetAllClubs(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": clubs})
}

// GetPublicClubs is the app-facing listing: always active-only, raw array
// response (matches the Skill/GetSkillsByLocation precedent).
// GET /clubs-by-location
func (h *ClubHandler) GetPublicClubs(c *gin.Context) {
	filters := domain.ClubFilters{
		ClubType:         c.Query("club_type"),
		Category:         c.Query("category"),
		ActiveOnly:       true,
		RequestingUserID: requestingUserID(c),
	}
	if v := c.Query("university_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			filters.UniversityID = &id
		}
	}
	if v := c.Query("department_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			filters.DepartmentID = &id
		}
	}

	clubs, err := h.repo.GetAllClubs(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, clubs)
}

// GET /clubs/:id
func (h *ClubHandler) GetClubByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	club, err := h.repo.GetClubByID(c.Request.Context(), id, requestingUserID(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Club not found"})
		return
	}
	c.JSON(http.StatusOK, club)
}

// POST /clubs (admin-only, API-key group)
func (h *ClubHandler) CreateClub(c *gin.Context) {
	var club domain.Club
	if err := c.ShouldBindJSON(&club); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreateClub(c.Request.Context(), &club); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, club)
}

// PUT /clubs/:id (admin-only, API-key group)
func (h *ClubHandler) UpdateClub(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	existing, err := h.repo.GetClubByID(c.Request.Context(), id, nil)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Club not found"})
		return
	}
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing.ID = id
	if err := h.repo.UpdateClub(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DELETE /clubs/:id (admin-only, API-key group)
func (h *ClubHandler) DeleteClub(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	if err := h.repo.DeleteClub(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Club deleted"})
}

// POST /clubs/:id/follow (JWT-protected)
func (h *ClubHandler) FollowClub(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.FollowClub(c.Request.Context(), clubID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to follow club"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Followed"})
}

// DELETE /clubs/:id/follow (JWT-protected)
func (h *ClubHandler) UnfollowClub(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.UnfollowClub(c.Request.Context(), clubID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unfollow club"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Unfollowed"})
}

// POST /clubs/suggest (JWT-protected) — creates a club pending admin review
// (forced IsActive=false regardless of the request body).
func (h *ClubHandler) SuggestClub(c *gin.Context) {
	var club domain.Club
	if err := c.ShouldBindJSON(&club); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	club.CreatedByID = userID
	club.UpdatedByID = userID
	if err := h.repo.SuggestClub(c.Request.Context(), &club, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, club)
}

// POST /clubs/:id/join (JWT-protected) — the formal Members roster,
// independent of Follow.
func (h *ClubHandler) JoinClub(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.JoinClub(c.Request.Context(), clubID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to join club"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Joined"})
}

// DELETE /clubs/:id/join (JWT-protected)
func (h *ClubHandler) LeaveClub(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.LeaveClub(c.Request.Context(), clubID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to leave club"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Left"})
}

// GET /clubs/:id/members — public roster for the Members tab.
func (h *ClubHandler) GetPublicClubMembers(c *gin.Context) {
	clubID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid club ID"})
		return
	}
	members, err := h.repo.GetPublicClubMembers(c.Request.Context(), clubID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, members)
}
