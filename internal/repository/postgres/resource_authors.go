package postgres

import (
	"context"
	"fmt"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func orderAuthors(db *gorm.DB) *gorm.DB {
	return db.Order("position ASC")
}

// replaceResourceAuthors swaps a resource's authors for the given list, in
// order. A linked author without a name takes the profile's current name, so
// the credit still reads right if the profile is later removed.
func replaceResourceAuthors(ctx context.Context, db *gorm.DB, resourceID uuid.UUID, authors []domain.ResourceAuthor) ([]domain.ResourceAuthor, error) {
	saved := make([]domain.ResourceAuthor, 0, len(authors))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("resource_id = ?", resourceID).Delete(&domain.ResourceAuthor{}).Error; err != nil {
			return err
		}
		for i, a := range authors {
			a.ID = uuid.Nil
			a.ResourceID = resourceID
			a.Position = i
			if a.Name == "" {
				name, err := profileName(tx, a)
				if err != nil {
					return err
				}
				a.Name = name
			}
			if err := tx.Create(&a).Error; err != nil {
				return err
			}
			saved = append(saved, a)
		}
		return nil
	})
	return saved, err
}

func profileName(tx *gorm.DB, a domain.ResourceAuthor) (string, error) {
	table, id := "teachers", a.TeacherID
	if a.StudentID != nil {
		table, id = "students", a.StudentID
	}
	var name string
	err := tx.Table(table).Select("name").Where("id = ? AND deleted_at IS NULL", id).Scan(&name).Error
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("author %d: %s profile not found", a.Position+1, table[:len(table)-1])
	}
	return name, nil
}
