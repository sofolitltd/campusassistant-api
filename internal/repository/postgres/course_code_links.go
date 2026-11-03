package postgres

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// linkedCourseCodes returns code plus the codes linked to it through a
// course's previous_code in the same department, in both directions: the
// new course sees what was filed under its old code, and the old course
// sees what is filed under the new one. One step only, so a code renamed
// twice links each neighbour, not the whole chain. Codes are compared and
// returned lowercased: older rows store psy-101, the admin saves PSY-101.
func linkedCourseCodes(ctx context.Context, db *gorm.DB, departmentID interface{}, code string) []string {
	var rows []struct {
		CourseCode   string
		PreviousCode string
	}
	db.WithContext(ctx).Table("courses").
		Select("course_code, previous_code").
		Where("department_id = ? AND deleted_at IS NULL AND (LOWER(course_code) = LOWER(?) OR LOWER(previous_code) = LOWER(?))", departmentID, code, code).
		Scan(&rows)

	var codes []string
	seen := map[string]bool{}
	add := func(c string) {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" && !seen[c] {
			seen[c] = true
			codes = append(codes, c)
		}
	}
	add(code)
	for _, r := range rows {
		add(r.CourseCode)
		add(r.PreviousCode)
	}
	return codes
}
