package postgres

import (
	"context"
	"strings"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormRepository[T any] struct {
	DB           *gorm.DB
	defaultOrder string
}

func NewGormRepository[T any](db *gorm.DB) domain.Repository[T] {
	return &GormRepository[T]{DB: db}
}

func NewGormRepositoryWithOrder[T any](db *gorm.DB, order string) domain.Repository[T] {
	return &GormRepository[T]{DB: db, defaultOrder: order}
}

func (r *GormRepository[T]) Create(ctx context.Context, entity *T) error {
	return r.DB.WithContext(ctx).Create(entity).Error
}

func (r *GormRepository[T]) GetByID(ctx context.Context, id uuid.UUID) (*T, error) {
	var entity T
	// Assumes the entity struct has a field named "ID" or similar mapping.
	// GORM handles this well if the ID is the primary key.
	if err := r.DB.WithContext(ctx).Preload(clause.Associations).First(&entity, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

func (r *GormRepository[T]) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]T, int64, error) {
	var entities []T
	var count int64

	// Use a session to avoid polluting the main DB instance
	db := r.DB.WithContext(ctx).Model(new(T))

	// Track if target tables have been joined
	joinedTables := make(map[string]bool)

	// Apply filters
	shouldPreload := false
	// DEBUG LOG
	// fmt.Printf("DEBUG: GetAll Filters for %T: %v\n", *new(T), filter)
	for key, value := range filter {
		if key == "search" {
			searchVal := "%" + value.(string) + "%"
			// Dynamically find which columns exist in the model
			searchableColumns := []string{"name", "Name", "full_name", "FullName", "organization", "Organization", "title", "Title", "designation", "Designation", "email", "Email", "phone", "Phone", "student_id", "StudentID", "first_name", "FirstName", "last_name", "LastName", "acronym", "Acronym", "address", "Address", "slug", "Slug"}
			var conditions []string
			var values []interface{}

			// Get schema to check columns
			var t T
			stmt := &gorm.Statement{DB: r.DB}
			_ = stmt.Parse(&t)
			schema := stmt.Schema

			if schema != nil {
				tableName := schema.Table
				seenColumns := make(map[string]bool)
				for _, col := range searchableColumns {
					field := schema.LookUpField(col)
					if field != nil && field.DBName != "" {
						if seenColumns[field.DBName] {
							continue
						}
						seenColumns[field.DBName] = true

						// Double check with migrator to be absolutely sure the column exists in the DB
						if r.DB.Migrator().HasColumn(tableName, field.DBName) {
							conditions = append(conditions, field.DBName+" ILIKE ?")
							values = append(values, searchVal)
						}
					}
				}
			}

			if len(conditions) > 0 {
				query := ""
				for i, cond := range conditions {
					if i > 0 {
						query += " OR "
					}
					query += cond
				}
				db = db.Where("("+query+")", values...)
			}
		} else if key == "preload" {
			if b, ok := value.(bool); ok && b {
				shouldPreload = true
			}
		} else {
			// Specialized filtering for Banners/Contacts targeting specific entities
			var t T
			stmt := &gorm.Statement{DB: r.DB}
			_ = stmt.Parse(&t)
			isBanner := stmt.Schema != nil && (stmt.Schema.Table == "banners" || stmt.Schema.Name == "Banner")
			isContact := stmt.Schema != nil && (stmt.Schema.Table == "contacts" || stmt.Schema.Name == "EmergencyContact")

			if (isBanner || isContact) && (key == "university_id" || key == "department_id") {
				// Join with targets for specialized filtering
				targetTable := "banner_targets"
				foreignKey := "banner_id"
				if isContact {
					targetTable = "contact_targets"
					foreignKey = "contact_id"
				}

				// Only join once
				if !joinedTables[targetTable] {
					db = db.Joins("JOIN " + targetTable + " ON " + targetTable + "." + foreignKey + " = " + stmt.Schema.Table + ".id")
					joinedTables[targetTable] = true
				}
				db = db.Where(targetTable+"."+key+" = ?", value).Distinct()
			} else if isContact && key == "scope" {
				// Flutter client sends 'scope' with values like 'department', 'university', 'national'.
				// The DB column is 'target_scope' with values 'Department', 'University', 'National'.
				// Translate and qualify to avoid ambiguity when contact_targets is joined.
				scopeMap := map[string]string{
					"department": "Department",
					"university": "University",
					"national":   "National",
				}
				dbScope := value.(string)
				if mapped, ok := scopeMap[strings.ToLower(dbScope)]; ok {
					dbScope = mapped
				}
				db = db.Where("contacts.target_scope = ?", dbScope)
			} else if isContact && key == "target_scope" {
				// Admin sends 'target_scope' directly with the correct DB value (e.g. 'Department').
				// Qualify to avoid ambiguity when contact_targets is joined.
				db = db.Where("contacts.target_scope = ?", value)
			} else {
				// Check if value is a slice for IN query
				switch v := value.(type) {
				case []interface{}, []string, []uuid.UUID:
					db = db.Where(key+" IN (?)", v)
				default:
					db = db.Where(key+" = ?", v)
				}
			}
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	// Get schema information
	var t T
	stmt := &gorm.Statement{DB: r.DB}
	_ = stmt.Parse(&t)

	if shouldPreload {
		db = db.Preload(clause.Associations)
		if stmt.Schema != nil {
			if stmt.Schema.Table == "users" || stmt.Schema.Name == "User" {
				// Explicitly preload student/teacher profiles for User model
				db = db.Preload("Student").Preload("Teacher")
				// Also preload one level deeper for student/teacher details if they exist
				db = db.Preload("Student.Batch").Preload("Student.Session")
				db = db.Preload("Teacher.Department").Preload("Teacher.University")
			} else if stmt.Schema.Table == "universities" || stmt.Schema.Name == "University" {
				// Explicitly preload Departments for Universities
				db = db.Preload("Departments")
			} else if stmt.Schema.Name == "Alumni" {
				// Explicitly preload StudentProfile and its nested User/Batch for Alumni
				db = db.Preload("StudentProfile").Preload("StudentProfile.User").Preload("StudentProfile.Batch").Preload("OrganizationRef")
			}
		}
	}

	// Always preload targets for targeted entities
	if stmt.Schema != nil && (stmt.Schema.Table == "banners" || stmt.Schema.Table == "contacts") {
		db = db.Preload("Targets")
	}

	q := db.Limit(limit).Offset(offset)
	if r.defaultOrder != "" {
		q = q.Order(r.defaultOrder)
	}
	err := q.Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	// Smart Linkage for User model (Backend-side)
	// This ensures Flutter and Web both get a complete object without extra logic
	// Detect if we are working with the User model
	if stmt.Schema != nil && (stmt.Schema.Table == "users" || stmt.Schema.Name == "User") {
		// Extract emails of users missing profiles
		var emails []string
		userMap := make(map[string]int) // email -> index in entities

		for i := range entities {
			// Convert to any to access fields safely
			u, ok := any(&entities[i]).(*domain.User)
			if !ok {
				continue
			}
			
			if (u.Role == domain.RoleStudent && u.Student == nil) ||
				(u.Role == domain.RoleTeacher && u.Teacher == nil) {
				if u.Email != "" {
					emailLower := u.Email
					emails = append(emails, emailLower)
					userMap[emailLower] = i
				}
			}
		}

		if len(emails) > 0 {
			// Convert all search emails to lowercase
			lowerEmails := make([]string, len(emails))
			for i, e := range emails {
				lowerEmails[i] = strings.ToLower(e)
			}

			// Fetch Students with case-insensitive email matching
			var students []domain.Student
			r.DB.WithContext(ctx).Where("LOWER(email) IN ?", lowerEmails).
				Preload("Batch").Preload("Session").
				Find(&students)
			
			for _, s := range students {
				emailLower := strings.ToLower(s.Email)
				if idx, ok := userMap[emailLower]; ok {
					u := any(&entities[idx]).(*domain.User)
					if u.Student == nil {
						studentCopy := s
						u.Student = &studentCopy
					}
				}
			}

			// Fetch Teachers with case-insensitive email matching
			var teachers []domain.Teacher
			r.DB.WithContext(ctx).Where("LOWER(email) IN ?", lowerEmails).
				Preload("Department").Preload("University").
				Find(&teachers)
				
			for _, t := range teachers {
				emailLower := strings.ToLower(t.Email)
				if idx, ok := userMap[emailLower]; ok {
					u := any(&entities[idx]).(*domain.User)
					if u.Teacher == nil {
						teacherCopy := t
						u.Teacher = &teacherCopy
					}
				}
			}
		}
	}

	return entities, count, nil
}

func (r *GormRepository[T]) Update(ctx context.Context, entity *T) error {
	return r.DB.WithContext(ctx).Save(entity).Error
}

func (r *GormRepository[T]) Delete(ctx context.Context, id uuid.UUID) error {
	// Soft delete: GORM sets deleted_at if DeletedAt field is present in the model.
	return r.DB.WithContext(ctx).Delete(new(T), "id = ?", id).Error
}

func (r *GormRepository[T]) HardDelete(ctx context.Context, id uuid.UUID) error {
	// Permanently remove the row from the DB, bypassing soft-delete.
	return r.DB.WithContext(ctx).Unscoped().Delete(new(T), "id = ?", id).Error
}
