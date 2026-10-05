package repo

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// widget identifies itself with a textual key, the way every entity the auth
// domain stores does. The repository is generic, so it cannot assume the primary
// key is a number.
type widget struct {
	ID    string `gorm:"type:varchar(26);primaryKey"`
	Name  string `gorm:"uniqueIndex;not null"`
	Stock int
}

// newWidget returns a widget with a distinct, caller assigned key, which is
// what a ULID primary key obliges the caller to do. The counter stands in for
// the ULID the real entities get, and only has to be unique within a run.
var widgetSeq atomic.Uint64

func newWidget(t *testing.T, name string) *widget {
	t.Helper()

	n := widgetSeq.Add(1)

	return &widget{ID: fmt.Sprintf("01HQZX00000000000000%010d", n), Name: name}
}

func newTestRepo(t *testing.T) *GormRepository[widget] {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=foreign_keys(1)"), &gorm.Config{
		Logger: logger.Discard,
	})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&widget{}))
	require.NoError(t, db.AutoMigrate(&widget{}))

	return NewGormRepository[widget](db)
}

func TestGormRepositoryStoresTheKeyItIsGiven(t *testing.T) {
	r := newTestRepo(t)

	w := newWidget(t, "bolt")
	w.Stock = 3
	require.NoError(t, r.Create(context.Background(), w))

	assert.NotEmpty(t, w.ID)

	stored, err := r.GetByID(context.Background(), w.ID)
	require.NoError(t, err)
	assert.Equal(t, "bolt", stored.Name)
	assert.Equal(t, 3, stored.Stock)
}

func TestGormRepositoryGetByIDNotFound(t *testing.T) {
	r := newTestRepo(t)

	_, err := r.GetByID(context.Background(), "01HQZX0000000000000000000A")

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGormRepositoryUpdate(t *testing.T) {
	r := newTestRepo(t)

	w := newWidget(t, "nut")
	w.Stock = 1
	require.NoError(t, r.Create(context.Background(), w))

	w.Stock = 9
	require.NoError(t, r.Update(context.Background(), w))

	stored, err := r.GetByID(context.Background(), w.ID)
	require.NoError(t, err)
	assert.Equal(t, 9, stored.Stock)
}

func TestGormRepositoryDelete(t *testing.T) {
	r := newTestRepo(t)

	w := newWidget(t, "screw")
	require.NoError(t, r.Create(context.Background(), w))
	require.NoError(t, r.Delete(context.Background(), w.ID))

	_, err := r.GetByID(context.Background(), w.ID)

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGormRepositoryFindAll(t *testing.T) {
	r := newTestRepo(t)

	require.NoError(t, r.Create(context.Background(), newWidget(t, "a")))
	require.NoError(t, r.Create(context.Background(), newWidget(t, "b")))

	all, err := r.FindAll(context.Background())

	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestGormRepositoryFindAllEmptyIsNotNil(t *testing.T) {
	r := newTestRepo(t)

	all, err := r.FindAll(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, all)
	assert.Empty(t, all)
}

func TestGormRepositoryWithTxRollsBack(t *testing.T) {
	r := newTestRepo(t)
	require.NoError(t, r.Create(context.Background(), newWidget(t, "outside")))

	tx := r.DB().Begin()
	txRepo := r.WithTx(tx)
	require.NoError(t, txRepo.Create(context.Background(), newWidget(t, "inside")))
	require.NoError(t, tx.Rollback().Error)

	all, err := r.FindAll(context.Background())

	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "outside", all[0].Name)
}

func TestNormalizeError(t *testing.T) {
	assert.Equal(t, ErrNotFound, NormalizeError(gorm.ErrRecordNotFound))

	wrapped := fmt.Errorf("while loading user: %w", gorm.ErrRecordNotFound)
	assert.ErrorIs(t, NormalizeError(wrapped), ErrNotFound)

	other := errors.New("connection refused")
	assert.Equal(t, other, NormalizeError(other))

	assert.NoError(t, NormalizeError(nil))
}

func TestRepositoryInterfaceIsImplemented(t *testing.T) {
	var _ Repository[widget] = (*GormRepository[widget])(nil)
}
