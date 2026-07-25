package handler

import (
	"net/http"
	"strconv"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type LostFoundHandler struct {
	db                  *gorm.DB
	repo                domain.LostFoundRepository
	chatUsecase         domain.ChatUseCase
	notificationService *service.NotificationService
}

func NewLostFoundHandler(db *gorm.DB, repo domain.LostFoundRepository, chatUsecase domain.ChatUseCase, notificationService *service.NotificationService) *LostFoundHandler {
	return &LostFoundHandler{db: db, repo: repo, chatUsecase: chatUsecase, notificationService: notificationService}
}

// ---- Admin (moderation-only; items are always student-generated) ----

// GetAllItems is the admin listing — every item, any status, optionally
// filtered by ?status=&type=&category_id=&search=&limit=&offset=.
func (h *LostFoundHandler) GetAllItems(c *gin.Context) {
	categoryID, _ := uuid.Parse(c.Query("category_id"))
	limit, offset := parseLimitOffset(c)
	filter := domain.LostFoundFilter{
		Status:     domain.LostFoundStatus(c.Query("status")),
		Type:       domain.LostFoundType(c.Query("type")),
		CategoryID: categoryID,
		Search:     c.Query("search"),
		Limit:      limit,
		Offset:     offset,
	}
	items, count, err := h.repo.GetAllItems(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch items"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "count": count, "limit": limit, "offset": offset})
}

func (h *LostFoundHandler) GetItemByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid item id"})
		return
	}
	item, err := h.repo.GetItemByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

// SetStatus is the admin moderation action: remove (with a reason) or
// restore an item, e.g. { "status": "removed", "removal_reason": "spam" }.
func (h *LostFoundHandler) SetStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid item id"})
		return
	}
	var body struct {
		Status        domain.LostFoundStatus `json:"status" binding:"required"`
		RemovalReason string                 `json:"removal_reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.SetItemStatus(c.Request.Context(), id, body.Status, body.RemovalReason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update item status"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Item status updated"})
}

func (h *LostFoundHandler) DeleteItem(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid item id"})
		return
	}
	if err := h.repo.DeleteItem(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete item"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Item deleted"})
}

// GetAllReports lists moderation reports, optionally filtered by ?status=.
func (h *LostFoundHandler) GetAllReports(c *gin.Context) {
	reports, err := h.repo.GetAllReports(c.Request.Context(), domain.LostFoundReportStatus(c.Query("status")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch reports"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": reports})
}

// ResolveReport sets a report's status; body: { "status": "resolved"|"dismissed" }.
func (h *LostFoundHandler) ResolveReport(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid report id"})
		return
	}
	var body struct {
		Status domain.LostFoundReportStatus `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.SetReportStatus(c.Request.Context(), id, body.Status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update report"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Report updated"})
}

// ---- Public browse (app-facing) ----

// GetItemsByLocation is the app-facing browse endpoint: open items that are
// global (no targets) or targeted to this university/department. Optional
// ?type=&category_id=&search= narrow further.
func (h *LostFoundHandler) GetItemsByLocation(c *gin.Context) {
	universityID, _ := uuid.Parse(c.Query("university_id"))
	departmentID, _ := uuid.Parse(c.Query("department_id"))
	categoryID, _ := uuid.Parse(c.Query("category_id"))

	items, err := h.repo.GetItemsByLocation(c.Request.Context(), universityID, departmentID, categoryID, c.Query("type"), c.Query("search"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch items"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// ---- Self-service (JWT) ----

func currentUserID(c *gin.Context) uuid.UUID {
	return c.MustGet("user_id").(uuid.UUID)
}

func (h *LostFoundHandler) GetMyItems(c *gin.Context) {
	items, err := h.repo.GetItemsByPoster(c.Request.Context(), currentUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch items"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *LostFoundHandler) CreateMyItem(c *gin.Context) {
	var item domain.LostFoundItem
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item.PosterID = currentUserID(c)
	item.Status = domain.LostFoundStatusOpen
	if err := h.repo.CreateItem(c.Request.Context(), &item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create item"})
		return
	}
	c.JSON(http.StatusOK, item)
}

// ownedItem loads the item and verifies the requester is its poster.
func (h *LostFoundHandler) ownedItem(c *gin.Context) (*domain.LostFoundItem, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid item id"})
		return nil, false
	}
	item, err := h.repo.GetItemByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
		return nil, false
	}
	if item.PosterID != currentUserID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not own this item"})
		return nil, false
	}
	return item, true
}

func (h *LostFoundHandler) UpdateMyItem(c *gin.Context) {
	existing, ok := h.ownedItem(c)
	if !ok {
		return
	}
	// Bind JSON onto the already-fetched row (not a blank struct) so a
	// partial payload doesn't zero out fields the caller omitted — same
	// reasoning as ProductHandler.UpdateMyProduct.
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.repo.UpdateItem(c.Request.Context(), existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update item"})
		return
	}
	c.JSON(http.StatusOK, existing)
}

func (h *LostFoundHandler) DeleteMyItem(c *gin.Context) {
	existing, ok := h.ownedItem(c)
	if !ok {
		return
	}
	if err := h.repo.DeleteItem(c.Request.Context(), existing.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete item"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Item deleted"})
}

// ResolveMyItem lets the poster mark their own item as resolved (item was
// returned to its owner / found item was picked up).
func (h *LostFoundHandler) ResolveMyItem(c *gin.Context) {
	existing, ok := h.ownedItem(c)
	if !ok {
		return
	}
	if err := h.repo.SetItemStatus(c.Request.Context(), existing.ID, domain.LostFoundStatusResolved, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve item"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Item marked as resolved"})
}

// ---- Claims (JWT) ----

// CreateClaim lets any authenticated user (other than the poster) claim an
// item, and notifies the poster.
func (h *LostFoundHandler) CreateClaim(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid item id"})
		return
	}
	item, err := h.repo.GetItemByID(c.Request.Context(), itemID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
		return
	}
	userID := currentUserID(c)
	if item.PosterID == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot claim your own item"})
		return
	}

	var body struct {
		Message string `json:"message"`
	}
	_ = c.ShouldBindJSON(&body)

	claim := domain.LostFoundClaim{
		ItemID:    itemID,
		ClaimerID: userID,
		Message:   body.Message,
		Status:    domain.LostFoundClaimPending,
	}
	if err := h.repo.CreateClaim(c.Request.Context(), &claim); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit claim"})
		return
	}
	_ = h.repo.SetItemStatus(c.Request.Context(), itemID, domain.LostFoundStatusClaimed, "")

	_ = notifyLostFoundUser(c.Request.Context(), h.db, h.notificationService, item.PosterID,
		"New claim on your item", "Someone has responded to your \""+item.Title+"\" post.", itemID, userID)

	c.JSON(http.StatusOK, claim)
}

// GetClaimsForMyItem lists claims on an item the caller posted.
func (h *LostFoundHandler) GetClaimsForMyItem(c *gin.Context) {
	item, ok := h.ownedItem(c)
	if !ok {
		return
	}
	claims, err := h.repo.GetClaimsByItem(c.Request.Context(), item.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch claims"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": claims})
}

// AcceptClaim accepts a claim on the caller's own item. It opens (or reuses)
// a 1:1 chat conversation between poster and claimant so they can coordinate
// handover without exposing phone numbers, and returns that conversation.
func (h *LostFoundHandler) AcceptClaim(c *gin.Context) {
	item, ok := h.ownedItem(c)
	if !ok {
		return
	}
	claimID, err := uuid.Parse(c.Param("claimId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid claim id"})
		return
	}
	claim, err := h.repo.GetClaimByID(c.Request.Context(), claimID)
	if err != nil || claim.ItemID != item.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Claim not found"})
		return
	}
	if err := h.repo.SetClaimStatus(c.Request.Context(), claimID, domain.LostFoundClaimAccepted); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to accept claim"})
		return
	}

	conversation, err := h.chatUsecase.GetOrCreateConversation(c.Request.Context(), item.PosterID, claim.ClaimerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Claim accepted, but failed to open chat"})
		return
	}

	_ = notifyLostFoundUser(c.Request.Context(), h.db, h.notificationService, claim.ClaimerID,
		"Your claim was accepted", "The poster of \""+item.Title+"\" accepted your claim. Open the chat to coordinate.", item.ID, item.PosterID)

	c.JSON(http.StatusOK, gin.H{"claim": claim, "conversation": conversation})
}

func (h *LostFoundHandler) RejectClaim(c *gin.Context) {
	item, ok := h.ownedItem(c)
	if !ok {
		return
	}
	claimID, err := uuid.Parse(c.Param("claimId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid claim id"})
		return
	}
	claim, err := h.repo.GetClaimByID(c.Request.Context(), claimID)
	if err != nil || claim.ItemID != item.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Claim not found"})
		return
	}
	if err := h.repo.SetClaimStatus(c.Request.Context(), claimID, domain.LostFoundClaimRejected); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject claim"})
		return
	}

	_ = notifyLostFoundUser(c.Request.Context(), h.db, h.notificationService, claim.ClaimerID,
		"Your claim was declined", "The poster of \""+item.Title+"\" declined your claim.", item.ID, item.PosterID)

	c.JSON(http.StatusOK, gin.H{"message": "Claim rejected"})
}

// ---- Reports (JWT) ----

// ReportItem lets any authenticated user flag an item for admin review.
func (h *LostFoundHandler) ReportItem(c *gin.Context) {
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid item id"})
		return
	}
	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report := domain.LostFoundReport{
		ItemID:     itemID,
		ReporterID: currentUserID(c),
		Reason:     body.Reason,
		Status:     domain.LostFoundReportPending,
	}
	if err := h.repo.CreateReport(c.Request.Context(), &report); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit report"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Report submitted"})
}

func parseLimitOffset(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}
