package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuditEntityNameMatchesModelNames(t *testing.T) {
	cases := map[string]string{
		"resources":              "resource",
		"batches":                "batch",
		"class_notices":          "classnotice",
		"exam_notices":           "examnotice",
		"course_prefixes":        "courseprefix",
		"marketplace_categories": "marketplacecategory",
		"faculties":              "faculty",
		"sessions":               "session",
		"courses":                "course",
		"alumnis":                "alumni",
		"staffs":                 "staff",
		"crs":                    "cr",
	}
	for table, want := range cases {
		if got := auditEntityName(table); got != want {
			t.Errorf("auditEntityName(%q) = %q, want %q", table, got, want)
		}
	}
}

func TestRequireSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for role, allowed := range map[string]bool{"super_admin": true, "admin": false, "student": false, "": false} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if role != "" {
			c.Set("user_role", role)
		}
		if got := requireSuperAdmin(c); got != allowed {
			t.Errorf("role %q: allowed = %v, want %v", role, got, allowed)
		}
		if !allowed && w.Code != http.StatusForbidden {
			t.Errorf("role %q: status = %d, want 403", role, w.Code)
		}
	}
}

func TestTrashTypesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tt := range trashTypes {
		if seen[tt.Slug] {
			t.Errorf("duplicate trash type %q", tt.Slug)
		}
		seen[tt.Slug] = true
	}
}
