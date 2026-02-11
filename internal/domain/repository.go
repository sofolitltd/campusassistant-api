package domain

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines the standard CRUD operations.
type Repository[T any] interface {
	Create(ctx context.Context, entity *T) error
	GetByID(ctx context.Context, id uuid.UUID) (*T, error)
	GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]T, int64, error)
	Update(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id uuid.UUID) error
	// Additional flexible query methods could be added here
}

// Specific repositories can extend this if needed
type UserRepository interface {
	Repository[User]
	GetByEmail(ctx context.Context, email string) (*User, error)
}
