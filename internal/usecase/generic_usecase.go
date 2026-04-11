package usecase

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

type Usecase[T any] interface {
	Create(ctx context.Context, entity *T) error
	GetByID(ctx context.Context, id uuid.UUID) (*T, error)
	GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]T, int64, error)
	Update(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type genericUsecase[T any] struct {
	repo domain.Repository[T]
}

func NewGenericUsecase[T any](repo domain.Repository[T]) Usecase[T] {
	return &genericUsecase[T]{repo: repo}
}

func (u *genericUsecase[T]) Create(ctx context.Context, entity *T) error {
	// Add business logic/validation here if needed
	return u.repo.Create(ctx, entity)
}

func (u *genericUsecase[T]) GetByID(ctx context.Context, id uuid.UUID) (*T, error) {
	return u.repo.GetByID(ctx, id)
}

func (u *genericUsecase[T]) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]T, int64, error) {
	return u.repo.GetAll(ctx, filter, limit, offset)
}

func (u *genericUsecase[T]) Update(ctx context.Context, entity *T) error {
	return u.repo.Update(ctx, entity)
}

func (u *genericUsecase[T]) Delete(ctx context.Context, id uuid.UUID) error {
	return u.repo.Delete(ctx, id)
}
