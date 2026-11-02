package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestProfileCodeRedactionForAuthenticatedReads(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Student{}, &domain.Teacher{}, &domain.Staff{}); err != nil {
		t.Fatal(err)
	}
	universityID, departmentID, batchID := uuid.New(), uuid.New(), uuid.New()
	student := domain.Student{
		StudentID: "123", UniversityID: universityID, DepartmentID: departmentID,
		BatchID: batchID, SessionID: uuid.New(), Name: "Student", VerificationCode: "111111",
	}
	teacher := domain.Teacher{
		UniversityID: universityID, DepartmentID: departmentID,
		Name: "Teacher", VerificationCode: "222222",
	}
	staff := domain.Staff{
		UniversityID: universityID, DepartmentID: departmentID,
		Name: "Staff", VerificationCode: "333333",
	}
	for _, row := range []any{&student, &teacher, &staff} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	type listResponse struct {
		Data []map[string]any `json:"data"`
	}
	call := func(path, role string, fn gin.HandlerFunc) listResponse {
		t.Helper()
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		c.Set("user_role", role)
		fn(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", path, recorder.Code, recorder.Body.String())
		}
		var response listResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	studentHandler := NewStudentHandler(usecase.NewGenericUsecase(postgres.NewGormRepository[domain.Student](db)), db, nil)
	teacherHandler := NewTeacherHandler(usecase.NewGenericUsecase(postgres.NewGormRepository[domain.Teacher](db)), db)
	staffHandler := NewStaffHandler(usecase.NewGenericUsecase(postgres.NewGormRepository[domain.Staff](db)), db)

	for _, tc := range []struct {
		path string
		code string
		fn   gin.HandlerFunc
	}{
		{"/students", "111111", studentHandler.GetAll},
		{"/teachers", "222222", teacherHandler.GetAll},
		{"/staffs", "333333", staffHandler.GetAll},
	} {
		t.Run(tc.path, func(t *testing.T) {
			userRows := call(tc.path, "student", tc.fn).Data
			if len(userRows) != 1 || userRows[0]["verification_code"] != "" {
				t.Fatalf("non-admin response leaked a code: %#v", userRows)
			}
			adminRows := call(tc.path, "admin", tc.fn).Data
			if len(adminRows) != 1 || adminRows[0]["verification_code"] != tc.code {
				t.Fatalf("admin response should include the code: %#v", adminRows)
			}
		})
	}
}
