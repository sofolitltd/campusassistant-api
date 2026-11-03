package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type GenericHandler[T any] struct {
	Usecase       usecase.Usecase[T]
	DB            *gorm.DB
	BeforeRespond func(c *gin.Context, entity *T)
	// AfterUpdate, when set, runs after a successful Update with the record
	// as it was before the request and as saved.
	AfterUpdate func(c *gin.Context, before T, after *T)
	// ExtendFilter, when set, adds caller-dependent filters to GetAll.
	ExtendFilter func(c *gin.Context, filter map[string]interface{})
}

func NewGenericHandler[T any](u usecase.Usecase[T]) *GenericHandler[T] {
	return &GenericHandler[T]{Usecase: u}
}

func (h *GenericHandler[T]) Create(c *gin.Context) {
	var entity T
	if err := c.ShouldBindJSON(&entity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set Audit fields if supported and user_id exists
	if auditable, ok := any(&entity).(domain.Auditable); ok {
		if userID, exists := c.Get("user_id"); exists {
			if id, ok := userID.(uuid.UUID); ok {
				auditable.SetCreatedBy(id)
				auditable.SetUpdatedBy(id)
			}
		}
	}

	if err := h.Usecase.Create(c.Request.Context(), &entity); err != nil {
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			c.JSON(http.StatusConflict, gin.H{"error": "A record with this value already exists."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.DB != nil {
		audit.RecordRequest(c, h.DB, eventFor(c, "create", &entity, map[string]any{"created": &entity}))
	}
	c.JSON(http.StatusCreated, entity)
}

func (h *GenericHandler[T]) GetByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	entity, err := h.Usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	if h.BeforeRespond != nil {
		h.BeforeRespond(c, entity)
	}
	c.JSON(http.StatusOK, entity)
}

func (h *GenericHandler[T]) GetAll(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	// Default to 20 if zero or negative, cap at 500 max
	// (raised from 100: reference lists like universities/departments/batches
	// are fetched whole by clients and can exceed 100 rows)
	if limit <= 0 {
		limit = 20
	} else if limit > 500 {
		limit = 500
	}

	filter := make(map[string]interface{})

	// Add filters
	uuidFilters := []string{"university_id", "department_id", "faculty_id", "session_id", "user_id", "created_by_id", "level_id", "course_category_id", "batch_id", "student_profile_id", "organization_id", "skill_id"}
	for _, f := range uuidFilters {
		if val := c.Query(f); val != "" {
			filter[f] = val
		}
	}

	// Add string filters
	stringFilters := []string{"course_year", "course_category", "course_code", "name", "slug", "mode", "type", "club_type", "status", "batch", "year", "blood_group", "scope", "category", "target_scope", "role", "gender", "plan_status"}
	for _, f := range stringFilters {
		if val := c.Query(f); val != "" {
			filter[f] = val
		}
	}

	// Add boolean filters (only "true"/"false" are honoured). is_claimed
	// splits each profile pool into "claimed" (has a linked account) vs
	// "unclaimed" (still needs a claim-code email) for the admin dashboard.
	boolFilters := []string{"is_active", "is_verified", "is_pro", "is_claimed", "has_blood_group", "is_regular"}
	for _, f := range boolFilters {
		if val, err := strconv.ParseBool(c.Query(f)); err == nil {
			filter[f] = val
		}
	}

	// Add integer filters
	intFilters := []string{"lesson_no", "chapter_no", "file_size_bytes"}
	for _, f := range intFilters {
		if val := c.Query(f); val != "" {
			if intVal, err := strconv.Atoi(val); err == nil {
				filter[f] = intVal
			}
		}
	}

	// Course codes repeat across departments (PSY-101 exists in many), so a
	// course_code filter alone would return every department's rows.
	if _, hasCode := filter["course_code"]; hasCode {
		if _, hasDept := filter["department_id"]; !hasDept {
			c.JSON(http.StatusBadRequest, gin.H{"error": "department_id is required with course_code"})
			return
		}
	}

	if search := c.Query("search"); search != "" {
		filter["search"] = search
	}

	if h.ExtendFilter != nil {
		h.ExtendFilter(c, filter)
	}

	if includeDetails := c.Query("include_details"); includeDetails == "true" {
		filter["preload"] = true
	}
	if preload := c.Query("preload"); preload == "true" {
		filter["preload"] = true
	}

	// DEBUG: Print filter map
	// fmt.Printf("DEBUG: GetAll Filter for %T: %+v\n", *new(T), filter)

	entities, count, err := h.Usecase.GetAll(c.Request.Context(), filter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.BeforeRespond != nil {
		for i := range entities {
			h.BeforeRespond(c, &entities[i])
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"data":   entities,
		"count":  count,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *GenericHandler[T]) Update(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	existing, err := h.Usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	before := *existing
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if auditable, ok := any(existing).(domain.Auditable); ok {
		if userID, exists := c.Get("user_id"); exists {
			if id, ok := userID.(uuid.UUID); ok {
				auditable.SetUpdatedBy(id)
			}
		}
	}

	if err := h.Usecase.Update(c.Request.Context(), existing); err != nil {
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			c.JSON(http.StatusConflict, gin.H{"error": "A record with this value already exists."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.DB != nil {
		audit.RecordRequest(c, h.DB, eventFor(c, "update", existing, audit.Diff(before, existing)))
	}
	if h.AfterUpdate != nil {
		h.AfterUpdate(c, before, existing)
	}

	c.JSON(http.StatusOK, existing)
}

// Delete handles both soft delete (default) and permanent delete (?permanent=true).
func (h *GenericHandler[T]) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	before, _ := h.Usecase.GetByID(c.Request.Context(), id)
	if c.Query("permanent") == "true" {
		if !requireSuperAdmin(c) {
			return
		}
		if err := h.Usecase.HardDelete(c.Request.Context(), id); err != nil {
			c.JSON(deleteErrorStatus(err), gin.H{"error": err.Error()})
			return
		}
		h.recordDelete(c, before, true)
		c.JSON(http.StatusOK, gin.H{"message": "Deleted permanently"})
		return
	}

	if err := h.Usecase.Delete(c.Request.Context(), id); err != nil {
		c.JSON(deleteErrorStatus(err), gin.H{"error": err.Error()})
		return
	}

	h.recordDelete(c, before, false)
	c.JSON(http.StatusOK, gin.H{"message": "Deleted successfully"})
}

func (h *GenericHandler[T]) recordDelete(c *gin.Context, entity *T, permanent bool) {
	if h.DB == nil || entity == nil {
		return
	}
	audit.RecordRequest(c, h.DB, eventFor(c, deleteAction(permanent), entity, map[string]any{"deleted": entity}))
}

// deleteAction names a delete in the audit log: "delete" is a soft delete
// (restorable from the Trash), "purge" a permanent one.
func deleteAction(permanent bool) string {
	if permanent {
		return "purge"
	}
	return "delete"
}

// requireSuperAdmin answers 403 unless the caller is a super admin. Permanent
// deletes can't be undone (files are removed too), so only super admins may
// run them; everyone else soft-deletes and the row stays in the Trash.
func requireSuperAdmin(c *gin.Context) bool {
	if role, _ := c.Get("user_role"); role == "super_admin" {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "Only a super admin can permanently delete. Soft delete instead; it can be restored from the Trash."})
	return false
}

func eventFor[T any](c *gin.Context, action string, entity *T, changes any) audit.Event {
	event := audit.Event{
		Action:     audit.EntityName(entity) + "." + action,
		EntityType: audit.EntityName(entity),
		Changes:    changes,
	}
	if auditable, ok := any(entity).(domain.Entity); ok {
		event.EntityID = auditable.GetID()
	}
	var fields map[string]json.RawMessage
	if data, err := json.Marshal(entity); err == nil && json.Unmarshal(data, &fields) == nil {
		parseScope := func(key string) *uuid.UUID {
			raw := fields[key]
			var value string
			if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
				return nil
			}
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil {
				return nil
			}
			return &id
		}
		event.UniversityID = parseScope("university_id")
		event.DepartmentID = parseScope("department_id")
		event.BatchID = parseScope("batch_id")
	}
	return audit.FromRequest(c, event)
}

// deleteErrorStatus answers 409 when a repository refused the delete because
// other records still depend on the row (domain.ErrInUse).
func deleteErrorStatus(err error) int {
	if errors.Is(err, domain.ErrInUse) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (h *GenericHandler[T]) HardDelete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	// No super-admin check: this backs user-owned rows with no restore value
	// (bookmarks), not dashboard deletes — those go through Delete.
	before, _ := h.Usecase.GetByID(c.Request.Context(), id)
	if err := h.Usecase.HardDelete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.recordDelete(c, before, true)

	c.JSON(http.StatusOK, gin.H{"message": "Deleted permanently"})
}
