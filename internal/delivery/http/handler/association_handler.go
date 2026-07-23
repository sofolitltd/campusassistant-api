package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AssociationHandler is dedicated (not GenericHandler[T]) — mirrors
// ClubHandler for the same reasons (follow/join/suggest, IsFollowing state).
type AssociationHandler struct {
	repo domain.AssociationRepository
}

func NewAssociationHandler(repo domain.AssociationRepository) *AssociationHandler {
	return &AssociationHandler{repo: repo}
}

// GetAllAssociations powers the admin panel: unfiltered by default.
// GET /associations
func (h *AssociationHandler) GetAllAssociations(c *gin.Context) {
	filters := domain.AssociationFilters{
		AssociationType:  c.Query("association_type"),
		DistrictID:       c.Query("district_id"),
		SubDistrictID:    c.Query("sub_district_id"),
		Category:         c.Query("category"),
		RequestingUserID: requestingUserID(c),
	}
	if v := c.Query("university_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			filters.UniversityID = &id
		}
	}
	if v := c.Query("is_active"); v == "true" {
		filters.ActiveOnly = true
	}

	associations, err := h.repo.GetAllAssociations(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": associations})
}

// GetPublicAssociations is the app-facing listing: always active-only, raw
// array response. GET /associations-by-location
func (h *AssociationHandler) GetPublicAssociations(c *gin.Context) {
	filters := domain.AssociationFilters{
		AssociationType:  c.Query("association_type"),
		DistrictID:       c.Query("district_id"),
		SubDistrictID:    c.Query("sub_district_id"),
		Category:         c.Query("category"),
		ActiveOnly:       true,
		RequestingUserID: requestingUserID(c),
	}
	if v := c.Query("university_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			filters.UniversityID = &id
		}
	}

	associations, err := h.repo.GetAllAssociations(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, associations)
}

// GET /associations/:id
func (h *AssociationHandler) GetAssociationByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	association, err := h.repo.GetAssociationByID(c.Request.Context(), id, requestingUserID(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Association not found"})
		return
	}
	c.JSON(http.StatusOK, association)
}

// POST /associations (admin-only, API-key group)
func (h *AssociationHandler) CreateAssociation(c *gin.Context) {
	var association domain.Association
	if err := c.ShouldBindJSON(&association); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.CreateAssociation(c.Request.Context(), &association); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, association)
}

// PUT /associations/:id (admin-only, API-key group)
func (h *AssociationHandler) UpdateAssociation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	existing, err := h.repo.GetAssociationByID(c.Request.Context(), id, nil)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Association not found"})
		return
	}
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing.ID = id
	if err := h.repo.UpdateAssociation(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DELETE /associations/:id (admin-only, API-key group)
func (h *AssociationHandler) DeleteAssociation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	if err := h.repo.DeleteAssociation(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Association deleted"})
}

// POST /associations/:id/follow (JWT-protected)
func (h *AssociationHandler) FollowAssociation(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.FollowAssociation(c.Request.Context(), associationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to follow association"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Followed"})
}

// DELETE /associations/:id/follow (JWT-protected)
func (h *AssociationHandler) UnfollowAssociation(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.UnfollowAssociation(c.Request.Context(), associationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unfollow association"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Unfollowed"})
}

// POST /associations/suggest (JWT-protected) — creates an association
// pending admin review (forced IsActive=false regardless of the request
// body).
func (h *AssociationHandler) SuggestAssociation(c *gin.Context) {
	var association domain.Association
	if err := c.ShouldBindJSON(&association); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	association.CreatedByID = userID
	association.UpdatedByID = userID
	if err := h.repo.SuggestAssociation(c.Request.Context(), &association, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, association)
}

// POST /associations/:id/join (JWT-protected) — creates a pending request on
// the formal Members roster (independent of Follow); an admin must approve
// it before the user is actually counted as a member.
func (h *AssociationHandler) JoinAssociation(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.JoinAssociation(c.Request.Context(), associationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to join association"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Request submitted, pending approval"})
}

// DELETE /associations/:id/join (JWT-protected)
func (h *AssociationHandler) LeaveAssociation(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	if err := h.repo.LeaveAssociation(c.Request.Context(), associationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to leave association"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Left"})
}

// GET /associations/:id/members — public roster for the Members tab.
func (h *AssociationHandler) GetPublicAssociationMembers(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	members, err := h.repo.GetPublicAssociationMembers(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, members)
}

// GetPendingAssociationMembers powers the admin Members-tab approval queue.
// GET /associations/:id/members/pending (admin-only, API-key group)
func (h *AssociationHandler) GetPendingAssociationMembers(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	members, err := h.repo.GetPendingAssociationMembers(c.Request.Context(), associationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, members)
}

// ApproveAssociationMember admits a pending requester as a formal member.
// POST /associations/:id/members/:userId/approve (admin-only, API-key group)
func (h *AssociationHandler) ApproveAssociationMember(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	if err := h.repo.ApproveAssociationMember(c.Request.Context(), associationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Approved"})
}

// RejectAssociationMember discards a pending join request.
// POST /associations/:id/members/:userId/reject (admin-only, API-key group)
func (h *AssociationHandler) RejectAssociationMember(c *gin.Context) {
	associationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid association ID"})
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	if err := h.repo.RejectAssociationMember(c.Request.Context(), associationID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Rejected"})
}

// GetJoinedAssociations powers the app's "My Joined Associations" screen —
// every association the requesting user has formally joined. Distinct from
// AssociationManageHandler.GetMyAssociations, which lists associations the
// user owns/co-manages.
// GET /my/associations/joined (JWT-protected)
func (h *AssociationHandler) GetJoinedAssociations(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	associations, err := h.repo.GetJoinedAssociations(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, associations)
}
