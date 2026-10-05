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

func (r *GormRepository[T]) DB() *gorm.DB {
	return r.db
}

// Create inserts entity, filling in generated fields such as the primary key.
func (r *GormRepository[T]) Create(ctx context.Context, entity *T) error {
	return r.db.WithContext(ctx).Create(entity).Error
}

func (r *GormRepository[T]) GetByID(ctx context.Context, id string) (*T, error) {
	var entity T
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&entity).Error; err != nil {
		return nil, NormalizeError(err)
	}
	return &entity, nil
}

func (r *GormRepository[T]) Update(ctx context.Context, entity *T) error {
	return r.db.WithContext(ctx).Save(entity).Error
}

func (r *GormRepository[T]) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(new(T)).Error
}

func (r *GormRepository[T]) FindAll(ctx context.Context) ([]T, error) {
	entities := make([]T, 0)
	if err := r.db.WithContext(ctx).Find(&entities).Error; err != nil {
		return nil, NormalizeError(err)
	}
	return entities, nil
}

func NormalizeError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
