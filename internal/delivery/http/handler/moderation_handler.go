package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"campusassistant-api/internal/authz"
	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ModerationHandler struct {
	db             *gorm.DB
	studentHandler *StudentHandler
	verification   *StudentVerificationHandler
	resource       *ResourceHandler
	storage        *storage.R2Storage
}

func NewModerationHandler(db *gorm.DB, students *StudentHandler, verifications *StudentVerificationHandler, resources *ResourceHandler, files *storage.R2Storage) *ModerationHandler {
	return &ModerationHandler{db: db, studentHandler: students, verification: verifications, resource: resources, storage: files}
}

func (h *ModerationHandler) Roles(c *gin.Context) {
	userID := callerID(c)
	roles, err := authz.Scopes(c.Request.Context(), h.db, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load moderator roles"})
		return
	}
	isAdmin := callerIsAdmin(c)
	codeScopes, err := h.authorizedScopes(c, authz.CodesView)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count pending codes"})
		return
	}
	verificationScopes, err := h.authorizedScopes(c, authz.VerificationsReview)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count pending verifications"})
		return
	}
	resourceScopes, err := h.authorizedScopes(c, authz.ResourcesReview)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count pending resources"})
		return
	}
	codeQuery := h.db.WithContext(c.Request.Context()).Model(&domain.Student{}).
		Where("is_claimed = false AND user_id IS NULL")
	verificationQuery := h.db.WithContext(c.Request.Context()).Model(&domain.StudentVerificationRequest{}).
		Where("status = ?", domain.StudentVerificationPending)
	resourceQuery := h.db.WithContext(c.Request.Context()).Model(&domain.Resource{}).
		Where("status = ?", domain.ResourceStatusPending)
	if !isAdmin {
		applyStudentScopeFilter(codeQuery, codeScopes)
		applyVerificationScopeFilter(verificationQuery, verificationScopes)
		applyResourceScopeFilter(resourceQuery, resourceScopes)
		resourceQuery = resourceQuery.Where("created_by_id <> ?", userID)
	}
	var codeCount, verificationCount, resourceCount int64
	if err := codeQuery.Count(&codeCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count pending codes"})
		return
	}
	if err := verificationQuery.Count(&verificationCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count pending verifications"})
		return
	}
	if err := resourceQuery.Count(&resourceCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count pending resources"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"roles": roles,
		"pending_counts": gin.H{
			"codes": codeCount, "verifications": verificationCount, "resources": resourceCount,
		},
	}})
}

func (h *ModerationHandler) Students(c *gin.Context) {
	isAdmin := callerIsAdmin(c)
	assignments, err := h.authorizedScopes(c, authz.CodesView)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return
	}
	selected := filterAssignments(assignments, c.Query("scope_type"), c.Query("scope_id"))
	if !isAdmin && len(selected) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not have access to this scope"})
		return
	}
	query := h.db.WithContext(c.Request.Context()).Model(&domain.Student{}).
		Where("is_claimed = false AND user_id IS NULL")
	if !isAdmin {
		applyStudentScopeFilter(query, selected)
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		query = query.Where("(name ILIKE ? OR student_id ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}
	page, limit, ok := pageAndLimit(c)
	if !ok {
		return
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search unclaimed students"})
		return
	}
	var students []domain.Student
	if err := query.Order("name ASC").Limit(limit).Offset((page - 1) * limit).Find(&students).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list unclaimed students"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": students, "count": count, "page": page, "limit": limit})
}

func (h *ModerationHandler) GenerateCode(c *gin.Context) {
	id, ok := parsePathUUID(c)
	if !ok {
		return
	}
	var student domain.Student
	if err := h.db.WithContext(c.Request.Context()).First(&student, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student not found"})
		return
	}
	allowed, err := authz.Can(c.Request.Context(), h.db, actorFromContext(c), authz.CodesRegenerate, authz.Target{
		UniversityID: student.UniversityID, DepartmentID: student.DepartmentID, BatchID: student.BatchID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot regenerate codes for this student"})
		return
	}
	if !allowCodeRegeneration(callerID(c), time.Now()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Code regeneration limit is 30 per hour"})
		return
	}
	h.studentHandler.GenerateCode(c)
}

func (h *ModerationHandler) Verifications(c *gin.Context) {
	if status := c.Query("status"); status != "" && status != string(domain.StudentVerificationPending) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Moderators can only list pending requests"})
		return
	}
	isAdmin := callerIsAdmin(c)
	assignments, err := h.authorizedScopes(c, authz.VerificationsReview)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return
	}
	if !isAdmin && len(assignments) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not have permission to review verifications"})
		return
	}
	query := h.db.WithContext(c.Request.Context()).Model(&domain.StudentVerificationRequest{}).
		Where("status = ?", domain.StudentVerificationPending)
	if !isAdmin {
		applyVerificationScopeFilter(query, assignments)
	}
	page, limit, ok := pageAndLimit(c)
	if !ok {
		return
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list verification requests"})
		return
	}
	var requests []domain.StudentVerificationRequest
	if err := query.Order("created_at ASC").Limit(limit).Offset((page - 1) * limit).Find(&requests).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list verification requests"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": requests, "count": count, "page": page, "limit": limit})
}

func (h *ModerationHandler) Verification(c *gin.Context) {
	v, ok := h.loadVerification(c)
	if !ok {
		return
	}
	if !callerIsAdmin(c) && v.UserID == callerID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot review your own submission"})
		return
	}
	if h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Private document storage is unavailable"})
		return
	}
	idCard, err := h.presignedAttachment(c, v.IDCardAttachmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create a private document link"})
		return
	}
	secondDoc, err := h.presignedAttachment(c, v.SecondDocAttachmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create a private document link"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"request": v,
		"documents": gin.H{
			"id_card_url":        idCard,
			"second_doc_type":    v.SecondDocType,
			"second_doc_url":     secondDoc,
			"expires_in_seconds": 600,
		},
	})
}

func (h *ModerationHandler) ApproveVerification(c *gin.Context) {
	v, ok := h.loadVerification(c)
	if !ok {
		return
	}
	if !callerIsAdmin(c) && v.UserID == callerID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot review your own submission"})
		return
	}
	c.Set("moderator_review", true)
	h.verification.Approve(c)
}

func (h *ModerationHandler) RejectVerification(c *gin.Context) {
	v, ok := h.loadVerification(c)
	if !ok {
		return
	}
	if !callerIsAdmin(c) && v.UserID == callerID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot review your own submission"})
		return
	}
	c.Set("moderator_review", true)
	h.verification.Reject(c)
}

func (h *ModerationHandler) Resources(c *gin.Context) {
	if status := c.Query("status"); status != "" && status != string(domain.ResourceStatusPending) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Moderators can only list pending resources"})
		return
	}
	isAdmin := callerIsAdmin(c)
	assignments, err := h.authorizedScopes(c, authz.ResourcesReview)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return
	}
	if !isAdmin && len(assignments) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not have permission to review resources"})
		return
	}
	query := h.db.WithContext(c.Request.Context()).Model(&domain.Resource{}).
		Where("status = ?", domain.ResourceStatusPending)
	if !isAdmin {
		query = query.Where("created_by_id <> ?", callerID(c))
		applyResourceScopeFilter(query, assignments)
	}
	page, limit, ok := pageAndLimit(c)
	if !ok {
		return
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list resources"})
		return
	}
	var resources []domain.Resource
	if err := query.Order("created_at ASC").Limit(limit).Offset((page - 1) * limit).Find(&resources).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list resources"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": resources, "count": count, "page": page, "limit": limit})
}

func (h *ModerationHandler) ApproveResource(c *gin.Context) {
	if !h.authorizeResource(c) {
		return
	}
	c.Set("moderator_review", true)
	h.resource.ApproveResource(c)
}

func (h *ModerationHandler) RejectResource(c *gin.Context) {
	if !h.authorizeResource(c) {
		return
	}
	c.Set("moderator_review", true)
	h.resource.RejectResource(c)
}

func (h *ModerationHandler) authorizeResource(c *gin.Context) bool {
	id, ok := parsePathUUID(c)
	if !ok {
		return false
	}
	var resource domain.Resource
	if err := h.db.WithContext(c.Request.Context()).First(&resource, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resource not found"})
		return false
	}
	if !callerIsAdmin(c) && resource.CreatedByID == callerID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot review your own submission"})
		return false
	}
	allowed, err := authz.Can(c.Request.Context(), h.db, actorFromContext(c), authz.ResourcesReview, authz.Target{
		UniversityID: resource.UniversityID, DepartmentID: resource.DepartmentID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot review resources in this department"})
		return false
	}
	return true
}

func (h *ModerationHandler) loadVerification(c *gin.Context) (*domain.StudentVerificationRequest, bool) {
	id, ok := parsePathUUID(c)
	if !ok {
		return nil, false
	}
	var v domain.StudentVerificationRequest
	if err := h.db.WithContext(c.Request.Context()).First(&v, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Verification request not found"})
		return nil, false
	}
	allowed, err := authz.Can(c.Request.Context(), h.db, actorFromContext(c), authz.VerificationsReview, authz.Target{
		UniversityID: v.UniversityID, DepartmentID: v.DepartmentID, BatchID: v.BatchID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return nil, false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot review this verification request"})
		return nil, false
	}
	return &v, true
}

func (h *ModerationHandler) presignedAttachment(c *gin.Context, id uuid.UUID) (string, error) {
	var attachment domain.Attachment
	if err := h.db.WithContext(c.Request.Context()).First(&attachment, "id = ?", id).Error; err != nil {
		return "", err
	}
	if attachment.Visibility != "private" || !strings.HasPrefix(attachment.StorageKey, domain.StudentVerificationUploadFolder+"/") {
		return "", fmt.Errorf("attachment is not a private verification document")
	}
	return h.storage.GetPresignedURL(c.Request.Context(), attachment.StorageKey, 10*time.Minute)
}

func (h *ModerationHandler) authorizedScopes(c *gin.Context, permission authz.Permission) ([]domain.Moderator, error) {
	if callerIsAdmin(c) {
		return nil, nil
	}
	var assignments []domain.Moderator
	err := domain.ActiveModerator(h.db.WithContext(c.Request.Context())).
		Where("user_id = ?", callerID(c)).
		Find(&assignments).Error
	if err != nil {
		return nil, err
	}
	result := make([]domain.Moderator, 0, len(assignments))
	for _, assignment := range assignments {
		target := authz.Target{UniversityID: assignment.UniversityID}
		switch assignment.ScopeType {
		case domain.ModeratorScopeBatch:
			target.BatchID = assignment.ScopeID
			if assignment.DepartmentID != nil {
				target.DepartmentID = *assignment.DepartmentID
			}
		case domain.ModeratorScopeDepartment:
			target.DepartmentID = assignment.ScopeID
		}
		allowed, err := authz.Can(c.Request.Context(), h.db, authz.Actor{UserID: callerID(c)}, permission, target)
		if err != nil {
			return nil, err
		}
		if allowed {
			result = append(result, assignment)
		}
	}
	return result, nil
}

func filterAssignments(assignments []domain.Moderator, scopeType, rawScopeID string) []domain.Moderator {
	if scopeType == "" && rawScopeID == "" {
		return assignments
	}
	scopeID, err := uuid.Parse(rawScopeID)
	if err != nil || scopeType == "" {
		return nil
	}
	result := make([]domain.Moderator, 0, 1)
	for _, assignment := range assignments {
		if assignment.ScopeType == domain.ModeratorScope(scopeType) && assignment.ScopeID == scopeID {
			result = append(result, assignment)
		}
	}
	return result
}

func applyStudentScopeFilter(query *gorm.DB, assignments []domain.Moderator) {
	addScopeOr(query, assignments, "batch_id", "department_id", "university_id")
}

func applyVerificationScopeFilter(query *gorm.DB, assignments []domain.Moderator) {
	addScopeOr(query, assignments, "batch_id", "department_id", "university_id")
}

func applyResourceScopeFilter(query *gorm.DB, assignments []domain.Moderator) {
	conditions := make([]string, 0, len(assignments))
	values := make([]any, 0, len(assignments))
	for _, assignment := range assignments {
		switch assignment.ScopeType {
		case domain.ModeratorScopeDepartment:
			conditions = append(conditions, "department_id = ?")
			values = append(values, assignment.ScopeID)
		case domain.ModeratorScopeUniversity:
			conditions = append(conditions, "university_id = ?")
			values = append(values, assignment.ScopeID)
		}
	}
	if len(conditions) > 0 {
		query.Where("("+strings.Join(conditions, " OR ")+")", values...)
	} else {
		query.Where("1 = 0")
	}
}

func addScopeOr(query *gorm.DB, assignments []domain.Moderator, batchField, departmentField, universityField string) {
	conditions := make([]string, 0, len(assignments))
	values := make([]any, 0, len(assignments))
	for _, assignment := range assignments {
		switch assignment.ScopeType {
		case domain.ModeratorScopeBatch:
			conditions = append(conditions, batchField+" = ?")
			values = append(values, assignment.ScopeID)
		case domain.ModeratorScopeDepartment:
			conditions = append(conditions, departmentField+" = ?")
			values = append(values, assignment.ScopeID)
		case domain.ModeratorScopeUniversity:
			conditions = append(conditions, universityField+" = ?")
			values = append(values, assignment.ScopeID)
		}
	}
	if len(conditions) > 0 {
		query.Where("("+strings.Join(conditions, " OR ")+")", values...)
	} else {
		query.Where("1 = 0")
	}
}

func actorFromContext(c *gin.Context) authz.Actor {
	return authz.Actor{UserID: callerID(c), IsAdmin: callerIsAdmin(c)}
}

func moderatorAuditRole(c *gin.Context, db *gorm.DB, universityID, departmentID, batchID uuid.UUID) string {
	if callerIsAdmin(c) {
		return "admin"
	}
	var assignments []domain.Moderator
	if err := domain.ActiveModerator(db.WithContext(c.Request.Context())).
		Where("user_id = ?", callerID(c)).
		Find(&assignments).Error; err != nil {
		return "user"
	}
	for _, scope := range []struct {
		kind domain.ModeratorScope
		id   uuid.UUID
	}{{domain.ModeratorScopeBatch, batchID}, {domain.ModeratorScopeDepartment, departmentID}, {domain.ModeratorScopeUniversity, universityID}} {
		if scope.id == uuid.Nil {
			continue
		}
		for _, assignment := range assignments {
			if assignment.ScopeType == scope.kind && assignment.ScopeID == scope.id {
				return string(scope.kind) + "_moderator"
			}
		}
	}
	return "user"
}

func parsePathUUID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return uuid.Nil, false
	}
	return id, true
}

func pageAndLimit(c *gin.Context) (int, int, bool) {
	page := 1
	if raw := c.Query("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
			return 0, 0, false
		}
		page = value
	}
	limit := 30
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return 0, 0, false
		}
		if value > 100 {
			value = 100
		}
		limit = value
	}
	return page, limit, true
}

func countModerationRows(db *gorm.DB, model any, condition string, args ...any) (int64, error) {
	var count int64
	err := db.Model(model).Where(condition, args...).Count(&count).Error
	return count, err
}

var codeRateLimit = struct {
	sync.Mutex
	byUser map[uuid.UUID][]time.Time
}{byUser: make(map[uuid.UUID][]time.Time)}

func allowCodeRegeneration(userID uuid.UUID, now time.Time) bool {
	codeRateLimit.Lock()
	defer codeRateLimit.Unlock()
	cutoff := now.Add(-time.Hour)
	recent := codeRateLimit.byUser[userID][:0]
	for _, at := range codeRateLimit.byUser[userID] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	if len(recent) >= 30 {
		codeRateLimit.byUser[userID] = recent
		return false
	}
	codeRateLimit.byUser[userID] = append(recent, now)
	return true
}
