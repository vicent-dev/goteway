package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// GormRepository implements Repository[T] on top of gorm.
type GormRepository[T any] struct {
	db *gorm.DB
}

// NewGormRepository returns a repository bound to db.
func NewGormRepository[T any](db *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: db}
}

// WithTx returns a copy bound to the given transaction.
func (r *GormRepository[T]) WithTx(tx *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: tx}
}

// DB exposes the underlying handle so that a repository living next to its
// domain can run its own queries, instead of forcing every query through the
// generic string based API below.
func (r *GormRepository[T]) DB() *gorm.DB {
	return r.db
}

// Create inserts entity, filling in generated fields such as the primary key.
func (r *GormRepository[T]) Create(ctx context.Context, entity *T) error {
	return r.db.WithContext(ctx).Create(entity).Error
}

// GetByID returns the entity with the given primary key, or ErrNotFound.
//
// The key is matched with an explicit Where rather than passed as an inline
// condition: GORM reads a numeric string as "this is the primary key" and
// anything else as raw SQL, so a textual key handed to First(dest, id) would
// end up in the wrong branch.
func (r *GormRepository[T]) GetByID(ctx context.Context, id string) (*T, error) {
	var entity T
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&entity).Error; err != nil {
		return nil, NormalizeError(err)
	}
	return &entity, nil
}

// Update persists every field of entity.
func (r *GormRepository[T]) Update(ctx context.Context, entity *T) error {
	return r.db.WithContext(ctx).Save(entity).Error
}

// Delete removes the entity with the given primary key, honouring soft deletes
// for models that declare gorm.DeletedAt.
func (r *GormRepository[T]) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(new(T)).Error
}

// FindAll returns every entity.
func (r *GormRepository[T]) FindAll(ctx context.Context) ([]T, error) {
	entities := make([]T, 0)
	if err := r.db.WithContext(ctx).Find(&entities).Error; err != nil {
		return nil, NormalizeError(err)
	}
	return entities, nil
}

// NormalizeError maps driver specific errors to the sentinels of this package
// and leaves every other error untouched.
func NormalizeError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
