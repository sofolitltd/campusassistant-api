package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Top contributor badge: a student in their department's top N by notes
// written, with at least a minimum count so a quiet department doesn't hand
// it out for one note.
const (
	topContributorRank     = 3
	topContributorMinNotes = 5
)

type contributorCourse struct {
	CourseCode string `json:"course_code"`
	Count      int64  `json:"count"`
}

type contributorStats struct {
	Written   int64               `json:"written"`  // credited as writer
	Lectures  int64               `json:"lectures"` // credited as the lecture's teacher
	Uploaded  int64               `json:"uploaded"` // uploaded by the profile's account
	Views     int64               `json:"views"`
	Downloads int64               `json:"downloads"`
	Courses   []contributorCourse `json:"courses"`
	Rank      int64               `json:"rank,omitempty"` // students: place in the department by notes written
	Badge     string              `json:"badge,omitempty"`
}

// ContributorStats returns what a teacher or student profile is credited
// with across published resources.
// GET /resources/contributor?teacher_id=... | ?student_id=...
func (h *ResourceHandler) ContributorStats(c *gin.Context) {
	column, table, raw := "teacher_id", "teachers", c.Query("teacher_id")
	if raw == "" {
		column, table, raw = "student_id", "students", c.Query("student_id")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "teacher_id or student_id is required"})
		return
	}
	db := h.db.WithContext(c.Request.Context())

	var profile struct {
		UserID       *uuid.UUID
		DepartmentID uuid.UUID
	}
	if err := db.Table(table).Select("user_id, department_id").Where("id = ? AND deleted_at IS NULL", id).Scan(&profile).Error; err != nil || profile.DepartmentID == uuid.Nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Profile not found"})
		return
	}

	credited := db.Table("resources").
		Where("resources.deleted_at IS NULL AND resources.status = ?", domain.ResourceStatusPublished).
		Where("EXISTS (SELECT 1 FROM resource_authors ra WHERE ra.resource_id = resources.id AND ra."+column+" = ?)", id)

	var stats contributorStats
	var totals struct{ Views, Downloads int64 }
	if err := credited.Session(&gorm.Session{}).Select("COALESCE(SUM(view_count),0) AS views, COALESCE(SUM(download_count),0) AS downloads").Scan(&totals).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stats.Views, stats.Downloads = totals.Views, totals.Downloads

	var roles []struct {
		Role  string
		Count int64
	}
	if err := db.Table("resource_authors ra").
		Joins("JOIN resources ON resources.id = ra.resource_id AND resources.deleted_at IS NULL AND resources.status = ?", domain.ResourceStatusPublished).
		Where("ra."+column+" = ?", id).
		Select("ra.role, COUNT(DISTINCT ra.resource_id) AS count").Group("ra.role").Scan(&roles).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, r := range roles {
		switch r.Role {
		case domain.AuthorRoleWriter:
			stats.Written = r.Count
		case domain.AuthorRoleLecture:
			stats.Lectures = r.Count
		}
	}

	stats.Courses = []contributorCourse{}
	if err := credited.Session(&gorm.Session{}).Select("course_code, COUNT(*) AS count").Group("course_code").Order("count DESC, course_code").Scan(&stats.Courses).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if profile.UserID != nil {
		db.Table("resources").Where("deleted_at IS NULL AND status = ? AND created_by_id = ?", domain.ResourceStatusPublished, *profile.UserID).Count(&stats.Uploaded)
	}

	if column == "student_id" && stats.Written > 0 {
		// 1 + the number of department students credited with more notes.
		var ahead int64
		db.Raw(`SELECT COUNT(*) FROM (
				SELECT ra.student_id FROM resource_authors ra
				JOIN resources r ON r.id = ra.resource_id AND r.deleted_at IS NULL AND r.status = ?
				JOIN students s ON s.id = ra.student_id AND s.department_id = ?
				WHERE ra.role = ?
				GROUP BY ra.student_id HAVING COUNT(DISTINCT ra.resource_id) > ?
			) t`, domain.ResourceStatusPublished, profile.DepartmentID, domain.AuthorRoleWriter, stats.Written).Scan(&ahead)
		stats.Rank = ahead + 1
		if stats.Rank <= topContributorRank && stats.Written >= topContributorMinNotes {
			stats.Badge = "top_contributor"
		}
	}

	c.JSON(http.StatusOK, stats)
}
