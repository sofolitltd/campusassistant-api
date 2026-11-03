package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"campusassistant-api/internal/audit"
	"campusassistant-api/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// trashType is one kind of soft-deletable row the dashboard can list,
// restore or purge. Table and Label are fixed here, never taken from the
// request, so they are safe to put into SQL.
type trashType struct {
	Slug  string // URL key, matches the dashboard's REST path
	Name  string // shown in the dashboard
	Table string
	Label string // SQL expression for a human-readable title
}

var trashTypes = []trashType{
	{"resources", "Resources", "resources", "title || ' · ' || coalesce(course_code, '')"},
	{"chapters", "Chapters", "chapters", "course_code || ' #' || chapter_no || ' · ' || chapter_title"},
	{"courses", "Courses", "courses", "course_code || ' · ' || course_title"},
	{"levels", "Levels", "levels", "name"},
	{"batches", "Batches", "batches", "name"},
	{"students", "Students", "students", "name"},
	{"teachers", "Teachers", "teachers", "name"},
	{"staffs", "Staff", "staffs", "name"},
	{"crs", "CRs", "crs", "name"},
	{"users", "Users", "users", "email"},
	{"admins", "Admins", "admins", "coalesce(name, '') || ' · ' || email"},
	{"class-notices", "Class notices", "class_notices", "title"},
	{"class-slots", "Class slots", "class_slots", "course_name || ' · ' || day_of_week::text || ' ' || start_time::text"},
	{"exam-notices", "Exam notices", "exam_notices", "course_code"},
	{"notices", "Notices", "notices", "left(message, 80)"},
	{"emergency-contacts", "Emergency contacts", "contacts", "title"},
	{"banners", "Banners", "banners", "title"},
	{"clubs", "Clubs", "clubs", "name"},
	{"club-events", "Club events", "club_events", "title"},
	{"associations", "Associations", "associations", "name"},
	{"association-events", "Association events", "association_events", "title"},
	{"products", "Products", "products", "title"},
	{"merchants", "Merchants", "merchants", "email"},
	{"marketplace-categories", "Marketplace categories", "marketplace_categories", "name"},
	{"lost-found-items", "Lost & found items", "lost_found_items", "title"},
	{"lost-found-categories", "Lost & found categories", "lost_found_categories", "name"},
	{"alumni", "Alumni", "alumnis", "full_name"},
	{"career-circulars", "Career circulars", "career_circulars", "title"},
	{"career-circular-categories", "Career categories", "career_circular_categories", "name"},
	{"skills", "Skills", "skills", "title"},
	{"skill-videos", "Skill videos", "skill_videos", "title"},
	{"halls", "Halls", "halls", "name"},
	{"transports", "Transports", "transports", "title"},
	{"contributors", "Contributors", "contributors", "name"},
	{"routines", "Routines", "routines", "title"},
	{"organizations", "Organizations", "organizations", "name"},
	{"faculties", "Faculties", "faculties", "name"},
	{"course-categories", "Course categories", "course_categories", "name"},
	{"course-prefixes", "Course prefixes", "course_prefixes", "prefix"},
	{"subscription-plans", "Subscription plans", "subscription_plans", "title"},
	{"sessions", "Sessions", "sessions", "name"},
	{"access-grants", "Access grants", "access_grants", "label"},
	{"departments", "Departments", "departments", "name"},
	{"universities", "Universities", "universities", "name"},
}

func findTrashType(slug string) (trashType, bool) {
	for _, t := range trashTypes {
		if t.Slug == slug {
			return t, true
		}
	}
	return trashType{}, false
}

// TrashHandler backs the dashboard's Trash page: everything soft-deleted,
// with restore for any admin and purge (permanent delete) for super admins.
type TrashHandler struct {
	db      *gorm.DB
	storage *storage.R2Storage
}

func NewTrashHandler(db *gorm.DB, storage *storage.R2Storage) *TrashHandler {
	return &TrashHandler{db: db, storage: storage}
}

type trashSummaryRow struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// GET /admin/trash/summary — soft-deleted row count per type (non-zero only).
func (h *TrashHandler) Summary(c *gin.Context) {
	out := []trashSummaryRow{}
	for _, t := range trashTypes {
		var n int64
		if err := h.db.WithContext(c.Request.Context()).Table(t.Table).Where("deleted_at IS NOT NULL").Count(&n).Error; err != nil {
			continue // a table missing in this environment shouldn't break the page
		}
		if n > 0 {
			out = append(out, trashSummaryRow{Type: t.Slug, Name: t.Name, Count: n})
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

type trashItem struct {
	ID        uuid.UUID `json:"id"`
	Label     string    `json:"label"`
	DeletedAt time.Time `json:"deleted_at"`
	DeletedBy string    `json:"deleted_by"`
}

// GET /admin/trash?type=resources&limit=50&offset=0
func (h *TrashHandler) List(c *gin.Context) {
	t, ok := findTrashType(c.Query("type"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown type"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	db := h.db.WithContext(c.Request.Context())
	var total int64
	if err := db.Table(t.Table).Where("deleted_at IS NOT NULL").Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Who deleted it comes from the newest soft-delete audit entry, if any —
	// deletes before auditing existed have none.
	items := []trashItem{}
	err := db.Raw(`
		SELECT t.id, coalesce(`+t.Label+`, '') AS label, t.deleted_at,
		  coalesce((SELECT a.actor_label FROM audit_logs a
		            WHERE a.entity_id = t.id AND a.action LIKE '%.delete'
		            ORDER BY a.created_at DESC LIMIT 1), '') AS deleted_by
		FROM `+t.Table+` t
		WHERE t.deleted_at IS NOT NULL
		ORDER BY t.deleted_at DESC
		LIMIT ? OFFSET ?`, limit, offset).Scan(&items).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "total": total})
}

func (h *TrashHandler) target(c *gin.Context) (trashType, uuid.UUID, bool) {
	t, ok := findTrashType(c.Param("type"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown type"})
		return t, uuid.Nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return t, uuid.Nil, false
	}
	return t, id, true
}

// auditEntityName turns a table name into the name audit.EntityName gives
// its model (lowercased struct name), so a restore or purge shows up in the
// same history as the delete: class_notices → classnotice, batches → batch.
func auditEntityName(table string) string {
	switch {
	case strings.HasSuffix(table, "ies"):
		table = strings.TrimSuffix(table, "ies") + "y"
	case strings.HasSuffix(table, "ches"), strings.HasSuffix(table, "xes"):
		table = strings.TrimSuffix(table, "es")
	default:
		table = strings.TrimSuffix(table, "s")
	}
	return strings.ReplaceAll(table, "_", "")
}

func (h *TrashHandler) record(c *gin.Context, t trashType, id uuid.UUID, action string) {
	name := auditEntityName(t.Table)
	audit.RecordRequest(c, h.db, audit.Event{
		Action:     name + "." + action,
		EntityType: name,
		EntityID:   id,
	})
}

// POST /admin/trash/:type/:id/restore — clears deleted_at.
func (h *TrashHandler) Restore(c *gin.Context) {
	t, id, ok := h.target(c)
	if !ok {
		return
	}
	res := h.db.WithContext(c.Request.Context()).
		Exec("UPDATE "+t.Table+" SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL", id)
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) || strings.Contains(res.Error.Error(), "duplicate key") {
			c.JSON(http.StatusConflict, gin.H{"error": "A live record with the same unique value exists; rename or delete it first."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not in the Trash"})
		return
	}
	h.record(c, t, id, "restore")
	c.JSON(http.StatusOK, gin.H{"restored": true})
}

// DELETE /admin/trash/:type/:id — permanent delete, super admins only, and
// only for rows already in the Trash (soft delete is always the first step).
func (h *TrashHandler) Purge(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	t, id, ok := h.target(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var files []string
	err := h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var exists int64
		if err := tx.Table(t.Table).Where("id = ? AND deleted_at IS NOT NULL", id).Count(&exists).Error; err != nil {
			return err
		}
		if exists == 0 {
			return gorm.ErrRecordNotFound
		}
		if t.Table == "resources" {
			// Same cleanup as the resource repository's HardDelete, plus the
			// stored files, removed only after the row is gone.
			var r struct{ FileURL, ThumbnailURL string }
			if err := tx.Raw("SELECT file_url, thumbnail_url FROM resources WHERE id = ?", id).Scan(&r).Error; err != nil {
				return err
			}
			files = []string{r.FileURL, r.ThumbnailURL}
			if err := tx.Exec("DELETE FROM resource_batches WHERE resource_id = ?", id).Error; err != nil {
				return err
			}
			if err := tx.Exec("DELETE FROM resource_ratings WHERE resource_id = ?", id).Error; err != nil {
				return err
			}
		}
		return tx.Exec("DELETE FROM "+t.Table+" WHERE id = ?", id).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not in the Trash"})
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			c.JSON(http.StatusConflict, gin.H{"error": "Other records still depend on this one, so it can't be permanently deleted. It stays in the Trash."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.storage != nil {
		for _, f := range files {
			if f != "" && !fileStillUsed(ctx, h.db, f) {
				if err := h.storage.DeleteFile(ctx, f); err != nil {
					c.Error(err)
				}
			}
		}
	}
	h.record(c, t, id, "purge")
	c.JSON(http.StatusOK, gin.H{"purged": true})
}

// GET /admin/trash/types — every type the Trash knows, for the filter list.
func (h *TrashHandler) Types(c *gin.Context) {
	out := make([]gin.H, 0, len(trashTypes))
	for _, t := range trashTypes {
		out = append(out, gin.H{"type": t.Slug, "name": t.Name})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}
