package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ModeratorAdminHandler struct {
	db              *gorm.DB
	notificationSvc *service.NotificationService
}

type moderatorListItem struct {
	domain.Moderator
	ScopeName      string             `json:"scope_name"`
	User           *moderatorListUser `json:"user,omitempty"`
	University     *domain.University `json:"university,omitempty"`
	Department     *domain.Department `json:"department,omitempty"`
	Batch          *domain.Batch      `json:"batch,omitempty"`
	GrantedByAdmin *domain.Admin      `json:"granted_by_admin,omitempty"`
}

type moderatorListUser struct {
	ID        uuid.UUID       `json:"id"`
	Email     string          `json:"email"`
	Name      string          `json:"name"`
	FirstName string          `json:"first_name"`
	LastName  string          `json:"last_name"`
	Student   *domain.Student `json:"student,omitempty"`
}

func NewModeratorAdminHandler(db *gorm.DB, notificationSvc *service.NotificationService) *ModeratorAdminHandler {
	return &ModeratorAdminHandler{db: db, notificationSvc: notificationSvc}
}

func (h *ModeratorAdminHandler) List(c *gin.Context) {
	query := h.db.WithContext(c.Request.Context()).Model(&domain.Moderator{}).Preload("User.Student")
	for _, field := range []string{"university_id", "department_id", "batch_id", "user_id"} {
		raw := c.Query(field)
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + field})
			return
		}
		column := field
		if field == "batch_id" {
			column = "scope_id"
			query = query.Where("scope_type = ?", domain.ModeratorScopeBatch)
		}
		query = query.Where(column+" = ?", id)
	}
	if raw := c.Query("scope_type"); raw != "" {
		scope := domain.ModeratorScope(raw)
		if !validModeratorScope(scope) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid scope_type"})
			return
		}
		query = query.Where("scope_type = ?", scope)
	}
	if raw := c.Query("active"); raw != "" {
		active, err := strconv.ParseBool(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "active must be true or false"})
			return
		}
		if active {
			query = domain.ActiveModerator(query)
		} else {
			now := time.Now()
			query = query.Where("is_active = false OR term_start > ? OR term_end <= ?", now, now)
		}
	}

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		if value > 100 {
			value = 100
		}
		limit = value
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid offset"})
			return
		}
		offset = value
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list moderators"})
		return
	}
	var assignments []domain.Moderator
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&assignments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list moderators"})
		return
	}
	result := make([]moderatorListItem, 0, len(assignments))
	for _, assignment := range assignments {
		name, err := moderatorScopeName(h.db.WithContext(c.Request.Context()), assignment)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve moderator scope"})
			return
		}
		item := moderatorListItem{Moderator: assignment, ScopeName: name}
		if assignment.User != nil {
			linkedStudent := assignment.User.Student
			displayName := strings.TrimSpace(assignment.User.FirstName + " " + assignment.User.LastName)
			if linkedStudent != nil && linkedStudent.Name != "" {
				displayName = linkedStudent.Name
			}
			item.User = &moderatorListUser{
				ID: assignment.User.ID, Email: assignment.User.Email,
				Name: displayName, FirstName: assignment.User.FirstName,
				LastName: assignment.User.LastName, Student: linkedStudent,
			}
		}
		var university domain.University
		if err := h.db.WithContext(c.Request.Context()).First(&university, "id = ?", assignment.UniversityID).Error; err == nil {
			item.University = &university
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load moderator university"})
			return
		}
		if assignment.DepartmentID != nil {
			var department domain.Department
			if err := h.db.WithContext(c.Request.Context()).First(&department, "id = ?", *assignment.DepartmentID).Error; err == nil {
				item.Department = &department
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load moderator department"})
				return
			}
		}
		if assignment.ScopeType == domain.ModeratorScopeBatch {
			var batch domain.Batch
			if err := h.db.WithContext(c.Request.Context()).First(&batch, "id = ?", assignment.ScopeID).Error; err == nil {
				item.Batch = &batch
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load moderator batch"})
				return
			}
		}
		if assignment.GrantedBy != nil {
			var admin domain.Admin
			if err := h.db.WithContext(c.Request.Context()).First(&admin, "id = ?", *assignment.GrantedBy).Error; err == nil {
				item.GrantedByAdmin = &admin
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load granting admin"})
				return
			}
		}
		result = append(result, item)
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "count": count, "limit": limit, "offset": offset})
}

type createModeratorRequest struct {
	UserID    uuid.UUID             `json:"user_id" binding:"required"`
	ScopeType domain.ModeratorScope `json:"scope_type" binding:"required"`
	TermStart *time.Time            `json:"term_start"`
	TermEnd   *time.Time            `json:"term_end"`
	Note      string                `json:"note"`
}

func (h *ModeratorAdminHandler) Create(c *gin.Context) {
	var req createModeratorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validModeratorScope(req.ScopeType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope_type must be batch, department, or university"})
		return
	}
	if req.TermStart != nil && req.TermEnd != nil && !req.TermStart.Before(*req.TermEnd) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "term_end must be after term_start"})
		return
	}

	db := h.db.WithContext(c.Request.Context())
	var user domain.User
	if err := db.First(&user, "id = ?", req.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student account not found"})
		return
	}
	if user.Role != domain.RoleStudent || !user.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only active student accounts can be moderators"})
		return
	}
	var student domain.Student
	if err := db.First(&student, "user_id = ?", user.ID).Error; err != nil || !student.IsClaimed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A claimed student profile is required"})
		return
	}
	var batch domain.Batch
	if err := db.First(&batch, "id = ?", student.BatchID).Error; err != nil ||
		batch.DepartmentID != student.DepartmentID || batch.UniversityID != student.UniversityID {
		c.JSON(http.StatusConflict, gin.H{"error": "Student profile has an invalid campus scope"})
		return
	}

	scopeID := uuid.Nil
	departmentID := student.DepartmentID
	switch req.ScopeType {
	case domain.ModeratorScopeBatch:
		scopeID = student.BatchID
	case domain.ModeratorScopeDepartment:
		scopeID = student.DepartmentID
	case domain.ModeratorScopeUniversity:
		scopeID = student.UniversityID
		departmentID = uuid.Nil
	}
	assignment := domain.Moderator{
		UserID:       user.ID,
		ScopeType:    req.ScopeType,
		ScopeID:      scopeID,
		UniversityID: student.UniversityID,
		TermStart:    req.TermStart,
		TermEnd:      req.TermEnd,
		IsActive:     true,
		Note:         strings.TrimSpace(req.Note),
	}
	if departmentID != uuid.Nil {
		assignment.DepartmentID = &departmentID
	}
	if adminID, ok := adminIDFromContext(c); ok {
		assignment.GrantedBy = &adminID
	}
	if len(assignment.Note) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note is too long"})
		return
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assignment)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrDuplicatedKey
		}
		event := audit.FromRequest(c, audit.Event{
			ViaRole: "admin", Action: "moderator.assign", EntityType: "moderator", EntityID: assignment.ID,
			UniversityID: &assignment.UniversityID, DepartmentID: assignment.DepartmentID,
			Changes: map[string]any{"scope_type": assignment.ScopeType, "scope_id": assignment.ScopeID, "term_start": assignment.TermStart, "term_end": assignment.TermEnd},
		})
		return audit.Record(tx, event)
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusConflict, gin.H{"error": "This student already has an active assignment for that scope"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign moderator"})
		return
	}
	h.notifyRole(c, user.ID, assignment, false)
	c.JSON(http.StatusCreated, assignment)
}

type updateModeratorRequest struct {
	TermStart *time.Time `json:"term_start"`
	TermEnd   *time.Time `json:"term_end"`
	Note      *string    `json:"note"`
}

func (h *ModeratorAdminHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid moderator id"})
		return
	}
	var req updateModeratorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TermStart == nil && req.TermEnd == nil && req.Note == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "At least one editable field is required"})
		return
	}

	db := h.db.WithContext(c.Request.Context())
	var before domain.Moderator
	if err := db.First(&before, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Moderator assignment not found"})
		return
	}
	updates := map[string]any{}
	if req.TermStart != nil {
		updates["term_start"] = req.TermStart
	}
	if req.TermEnd != nil {
		updates["term_end"] = req.TermEnd
	}
	if req.Note != nil {
		note := strings.TrimSpace(*req.Note)
		if len(note) > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "note is too long"})
			return
		}
		updates["note"] = note
	}
	termStart := before.TermStart
	termEnd := before.TermEnd
	if req.TermStart != nil {
		termStart = req.TermStart
	}
	if req.TermEnd != nil {
		termEnd = req.TermEnd
	}
	if termStart != nil && termEnd != nil && !termStart.Before(*termEnd) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "term_end must be after term_start"})
		return
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&before).Updates(updates).Error; err != nil {
			return err
		}
		var after domain.Moderator
		if err := tx.First(&after, "id = ?", id).Error; err != nil {
			return err
		}
		event := audit.FromRequest(c, audit.Event{
			ViaRole: "admin", Action: "moderator.update", EntityType: "moderator", EntityID: after.ID,
			UniversityID: &after.UniversityID, DepartmentID: after.DepartmentID,
			Changes: audit.Diff(before, after),
		})
		return audit.Record(tx, event)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update moderator assignment"})
		return
	}
	var after domain.Moderator
	if err := db.First(&after, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Moderator assignment was updated but could not be reloaded"})
		return
	}
	c.JSON(http.StatusOK, after)
}

func (h *ModeratorAdminHandler) Revoke(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid moderator id"})
		return
	}
	db := h.db.WithContext(c.Request.Context())
	var assignment domain.Moderator
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&assignment, "id = ?", id).Error; err != nil {
			return err
		}
		if !assignment.IsActive {
			return gorm.ErrRecordNotFound
		}
		now := time.Now()
		adminID, _ := adminIDFromContext(c)
		updates := map[string]any{"is_active": false, "revoked_at": now, "revoked_by": adminID}
		if err := tx.Model(&assignment).Updates(updates).Error; err != nil {
			return err
		}
		assignment.IsActive = false
		assignment.RevokedAt = &now
		assignment.RevokedBy = &adminID
		event := audit.FromRequest(c, audit.Event{
			ViaRole: "admin", Action: "moderator.revoke", EntityType: "moderator", EntityID: assignment.ID,
			UniversityID: &assignment.UniversityID, DepartmentID: assignment.DepartmentID,
			Changes: map[string]any{"is_active": [2]any{true, false}},
		})
		return audit.Record(tx, event)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Active moderator assignment not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke moderator assignment"})
		return
	}
	h.notifyRole(c, assignment.UserID, assignment, true)
	c.JSON(http.StatusOK, assignment)
}

func (h *ModeratorAdminHandler) notifyRole(c *gin.Context, userID uuid.UUID, assignment domain.Moderator, revoked bool) {
	if h.notificationSvc == nil {
		return
	}
	name, err := moderatorScopeName(h.db.WithContext(c.Request.Context()), assignment)
	if err != nil {
		log.Printf("[moderator notification] failed to resolve scope %s: %v", assignment.ScopeID, err)
		return
	}
	role := strings.Title(string(assignment.ScopeType)) // User-facing term, not an identifier.
	title, body := "Moderator role assigned", "You're now "+role+" Moderator of "+name+"."
	if revoked {
		title, body = "Moderator role revoked", "Your "+role+" Moderator role for "+name+" was revoked."
	}
	notification := domain.Notification{Title: title, Body: body, Type: "moderator_role", Scope: "user"}
	if _, _, err := h.notificationSvc.SendToUsers(c.Request.Context(), notification, []uuid.UUID{userID}, uuid.Nil); err != nil {
		log.Printf("[moderator notification] failed to notify user %s: %v", userID, err)
	}
}

func validModeratorScope(scope domain.ModeratorScope) bool {
	return scope == domain.ModeratorScopeBatch || scope == domain.ModeratorScopeDepartment || scope == domain.ModeratorScopeUniversity
}

func moderatorScopeName(db *gorm.DB, assignment domain.Moderator) (string, error) {
	table := map[domain.ModeratorScope]string{
		domain.ModeratorScopeBatch:      "batches",
		domain.ModeratorScopeDepartment: "departments",
		domain.ModeratorScopeUniversity: "universities",
	}[assignment.ScopeType]
	if table == "" {
		return "", errors.New("invalid moderator scope")
	}
	var name string
	if err := db.Table(table).Select("name").Where("id = ?", assignment.ScopeID).Scan(&name).Error; err != nil {
		return "", err
	}
	return name, nil
}

func adminIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	id, ok := c.Get("user_id")
	if !ok {
		return uuid.Nil, false
	}
	value, ok := id.(uuid.UUID)
	return value, ok && value != uuid.Nil
}
