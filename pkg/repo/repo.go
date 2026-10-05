// Package repo holds the generic persistence port and its generic GORM
// implementation. Domain specific queries live in the package that owns the
// domain, on top of GormRepository, so that neither the domain nor this
// package has to know about the other's entities.
package repo

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("repo: record not found")

type Repository[T any] interface {
	Create(ctx context.Context, entity *T) error
	GetByID(ctx context.Context, id string) (*T, error)
	Update(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id string) error
	FindAll(ctx context.Context) ([]T, error)
}
