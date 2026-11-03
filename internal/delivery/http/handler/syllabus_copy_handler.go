package handler

import (
	"net/http"

	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SyllabusCopyHandler gives a new batch an earlier batch's syllabus: the
// same levels, courses and chapters, by adding the batch to their tags.
// Resources aren't touched; every batch sees them already.
type SyllabusCopyHandler struct {
	db *gorm.DB
}

func NewSyllabusCopyHandler(db *gorm.DB) *SyllabusCopyHandler {
	return &SyllabusCopyHandler{db: db}
}

// syllabusTagTables are the batch-tag join tables copied, with the table
// and column of the tagged entity so trashed ones are skipped.
var syllabusTagTables = []struct{ join, entity, column string }{
	{"level_batches", "levels", "level_id"},
	{"course_batches", "courses", "course_id"},
	{"chapter_batches", "chapters", "chapter_id"},
}

// POST /batches/:id/copy-syllabus {from_batch_id}
// Adds batch :id to every live level, course and chapter tagged with the
// source batch. Existing tags are kept, so running it twice is harmless.
func (h *SyllabusCopyHandler) CopySyllabus(c *gin.Context) {
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}
	var req struct {
		FromBatchID uuid.UUID `json:"from_batch_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.FromBatchID == targetID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pick a different batch to copy from"})
		return
	}

	ctx := c.Request.Context()
	var target, source domain.Batch
	if err := h.db.WithContext(ctx).First(&target, "id = ?", targetID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Batch not found"})
		return
	}
	if err := h.db.WithContext(ctx).First(&source, "id = ?", req.FromBatchID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Source batch not found"})
		return
	}
	if source.DepartmentID != target.DepartmentID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Both batches must be in the same department"})
		return
	}

	added := gin.H{}
	err = h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, t := range syllabusTagTables {
			res := tx.Exec(`INSERT INTO `+t.join+` (`+t.column+`, batch_id)
				SELECT j.`+t.column+`, ? FROM `+t.join+` j
				JOIN `+t.entity+` e ON e.id = j.`+t.column+` AND e.deleted_at IS NULL
				WHERE j.batch_id = ?
				  AND NOT EXISTS (SELECT 1 FROM `+t.join+` x WHERE x.`+t.column+` = j.`+t.column+` AND x.batch_id = ?)`,
				targetID, req.FromBatchID, targetID)
			if res.Error != nil {
				return res.Error
			}
			added[t.entity] = res.RowsAffected
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	audit.RecordRequest(c, h.db, audit.Event{
		Action:       "batch.copy_syllabus",
		EntityType:   "batch",
		EntityID:     target.ID,
		UniversityID: &target.UniversityID,
		DepartmentID: &target.DepartmentID,
		BatchID:      &target.ID,
		Changes:      gin.H{"from_batch_id": source.ID, "added": added},
	})
	c.JSON(http.StatusOK, gin.H{"added": added})
}
