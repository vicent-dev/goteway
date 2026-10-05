// Package repo holds the generic persistence port and its generic GORM
// implementation. Domain specific queries live in the package that owns the
// domain, on top of GormRepository, so that neither the domain nor this
// package has to know about the other's entities.
package repo

import (
	"context"
	"errors"
)

// ErrNotFound is returned by every lookup that matches no record. Adapters
// translate driver specific errors into this sentinel so callers can check it
// with errors.Is without importing the driver.
var ErrNotFound = errors.New("repo: record not found")

// Repository is the generic CRUD contract implemented by GormRepository.
// Lookups report ErrNotFound when nothing matches.
//
// The primary key is passed as the string the database stores it as. The
// entities this repository serves identify themselves with ULIDs, and a
// narrower type here would either duplicate that domain vocabulary or tie a
// generic port to the first domain that used it.
type Repository[T any] interface {
	Create(ctx context.Context, entity *T) error
	GetByID(ctx context.Context, id string) (*T, error)
	Update(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id string) error
	FindAll(ctx context.Context) ([]T, error)
}
