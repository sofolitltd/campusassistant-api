package handler

import (
	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/domain"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EnrollmentHandler moves students between programs (Honors → Masters).
//
// A student's current program lives on Student itself; earlier programs are
// StudentEnrollment rows. Promote snapshots the current columns into a row
// and repoints Student at the new batch; Revert undoes the latest promote.
type EnrollmentHandler struct {
	db *gorm.DB
}

func NewEnrollmentHandler(db *gorm.DB) *EnrollmentHandler {
	return &EnrollmentHandler{db: db}
}

type promoteStudent struct {
	ID   uuid.UUID `json:"id" binding:"required"`
	Roll string    `json:"roll"` // empty keeps the current roll
}

type promoteRequest struct {
	SessionID uuid.UUID        `json:"session_id" binding:"required"`
	Students  []promoteStudent `json:"students" binding:"required,min=1"`
}

// Promote moves the listed students into batch :id.
// POST /batches/:id/promote (AdminJWT). All-or-nothing.
func (h *EnrollmentHandler) Promote(c *gin.Context) {
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid batch id"})
		return
	}
	var req promoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	db := h.db.WithContext(c.Request.Context())
	var target domain.Batch
	if err := db.First(&target, "id = ?", targetID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Batch not found"})
		return
	}
	if ok, err := domain.BatchHasSession(db, target.ID, req.SessionID); err != nil || !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Selected session does not belong to the target batch"})
		return
	}

	actorID, _ := c.Get("user_id")
	actor, _ := actorID.(uuid.UUID)
	now := time.Now()
	promoted := make([]uuid.UUID, 0, len(req.Students))

	err = db.Transaction(func(tx *gorm.DB) error {
		for _, item := range req.Students {
			var student domain.Student
			if err := tx.First(&student, "id = ?", item.ID).Error; err != nil {
				return fmt.Errorf("student %s not found", item.ID)
			}
			if student.BatchID == target.ID {
				return fmt.Errorf("%s is already in %s", student.Name, target.Name)
			}
			var from domain.Batch
			if err := tx.First(&from, "id = ?", student.BatchID).Error; err != nil {
				return fmt.Errorf("current batch of %s not found", student.Name)
			}

			enrollment := domain.StudentEnrollment{
				StudentID:    student.ID,
				BatchID:      student.BatchID,
				SessionID:    student.SessionID,
				DepartmentID: student.DepartmentID,
				UniversityID: student.UniversityID,
				Program:      from.Program,
				Roll:         student.StudentID,
				IsCR:         student.IsCR,
				IsRegular:    student.IsRegular,
				Status:       domain.EnrollmentStatusCompleted,
			}
			enrollment.ID = uuid.New()
			enrollment.CreatedByID = actor
			enrollment.UpdatedByID = actor
			if err := tx.Create(&enrollment).Error; err != nil {
				return err
			}

			roll := strings.TrimSpace(item.Roll)
			if roll == "" {
				roll = student.StudentID
			}
			if err := tx.Model(&domain.Student{}).Where("id = ?", student.ID).Updates(map[string]any{
				"batch_id":      target.ID,
				"session_id":    req.SessionID,
				"department_id": target.DepartmentID,
				"university_id": target.UniversityID,
				"student_id":    roll,
				"is_cr":         false,
				"is_regular":    true,
				"updated_by_id": actor,
			}).Error; err != nil {
				return err
			}

			if student.UserID != nil {
				// A Masters batch in another department moves the account too.
				if target.DepartmentID != student.DepartmentID || target.UniversityID != student.UniversityID {
					if err := tx.Model(&domain.User{}).Where("id = ?", *student.UserID).Updates(map[string]any{
						"department_id": target.DepartmentID,
						"university_id": target.UniversityID,
					}).Error; err != nil {
						return err
					}
				}
				// A moderator of the old batch no longer belongs to it.
				if err := tx.Model(&domain.Moderator{}).
					Where("user_id = ? AND scope_type = ? AND scope_id = ? AND is_active = true", *student.UserID, domain.ModeratorScopeBatch, student.BatchID).
					Updates(map[string]any{
						"is_active":  false,
						"revoked_at": now,
						"revoked_by": actor,
						"note":       gorm.Expr("TRIM(COALESCE(note, '') || ' ' || ?)", "Auto-revoked: promoted to "+target.Name),
					}).Error; err != nil {
					return err
				}
			}
			promoted = append(promoted, student.ID)
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	audit.RecordRequest(c, h.db, audit.Event{
		Action:       "batch.promote",
		EntityType:   "batch",
		EntityID:     target.ID,
		UniversityID: &target.UniversityID,
		DepartmentID: &target.DepartmentID,
		BatchID:      &target.ID,
		Changes:      gin.H{"student_ids": promoted, "session_id": req.SessionID},
	})
	c.JSON(http.StatusOK, gin.H{"promoted": len(promoted)})
}

func (h *EnrollmentHandler) list(c *gin.Context, studentID uuid.UUID) {
	rows, err := h.earlier(c, studentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

func (h *EnrollmentHandler) earlier(c *gin.Context, studentID uuid.UUID) ([]domain.StudentEnrollment, error) {
	var rows []domain.StudentEnrollment
	if err := h.db.WithContext(c.Request.Context()).
		Preload("Batch").Preload("Session").
		Where("student_id = ?", studentID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListForStudent returns a student's earlier programs, newest first.
// GET /students/:id/enrollments (AdminJWT)
func (h *EnrollmentHandler) ListForStudent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student id"})
		return
	}
	h.list(c, id)
}

// ListMine returns the caller's earlier programs plus the current one's
// program and batch, so the app can list e.g. Honors levels under a
// "Masters" header without another lookup.
// GET /my/student/enrollments (JWT)
func (h *EnrollmentHandler) ListMine(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	db := h.db.WithContext(c.Request.Context())
	var student domain.Student
	if err := db.Select("id", "batch_id").First(&student, "user_id = ?", userID).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []domain.StudentEnrollment{}})
		return
	}
	rows, err := h.earlier(c, student.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var current domain.Batch
	db.Select("id", "program").First(&current, "id = ?", student.BatchID)
	c.JSON(http.StatusOK, gin.H{
		"data":             rows,
		"current_batch_id": student.BatchID,
		"current_program":  current.Program,
	})
}

// Revert undoes a student's latest promote: Student goes back to the
// enrollment's batch/session/roll and the row is removed. Moderator roles
// revoked by the promote are not restored.
// POST /students/:id/enrollments/:enrollmentId/revert (AdminJWT)
func (h *EnrollmentHandler) Revert(c *gin.Context) {
	studentID, err1 := uuid.Parse(c.Param("id"))
	enrollmentID, err2 := uuid.Parse(c.Param("enrollmentId"))
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	db := h.db.WithContext(c.Request.Context())

	var latest domain.StudentEnrollment
	if err := db.Where("student_id = ?", studentID).Order("created_at DESC").First(&latest).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No earlier program to revert to"})
		return
	}
	if latest.ID != enrollmentID {
		c.JSON(http.StatusConflict, gin.H{"error": "Only the most recent promotion can be reverted"})
		return
	}

	var student domain.Student
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&student, "id = ?", studentID).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.Student{}).Where("id = ?", studentID).Updates(map[string]any{
			"batch_id":      latest.BatchID,
			"session_id":    latest.SessionID,
			"department_id": latest.DepartmentID,
			"university_id": latest.UniversityID,
			"student_id":    latest.Roll,
			"is_cr":         latest.IsCR,
			"is_regular":    latest.IsRegular,
		}).Error; err != nil {
			return err
		}
		if student.UserID != nil && (student.DepartmentID != latest.DepartmentID || student.UniversityID != latest.UniversityID) {
			if err := tx.Model(&domain.User{}).Where("id = ?", *student.UserID).Updates(map[string]any{
				"department_id": latest.DepartmentID,
				"university_id": latest.UniversityID,
			}).Error; err != nil {
				return err
			}
		}
		return tx.Unscoped().Delete(&domain.StudentEnrollment{}, "id = ?", latest.ID).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Student not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	audit.RecordRequest(c, h.db, audit.Event{
		Action:       "student.promote_revert",
		EntityType:   "student",
		EntityID:     studentID,
		UniversityID: &latest.UniversityID,
		DepartmentID: &latest.DepartmentID,
		BatchID:      &latest.BatchID,
		Changes:      gin.H{"from_batch_id": student.BatchID, "to_batch_id": latest.BatchID},
	})
	c.JSON(http.StatusOK, gin.H{"reverted": true})
}
