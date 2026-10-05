package auth

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"goteway/pkg/repo"
)

// GormStore is the GORM implementation of Store. It composes the generic
// repository for the CRUD operations and runs the domain specific queries
// itself, so no SQL leaks into the ports declared in store.go.
type GormStore struct {
	db           *gorm.DB
	users        *repo.GormRepository[User]
	refreshToken *repo.GormRepository[RefreshToken]
	regToken     *repo.GormRepository[RegistrationToken]
}

func NewGormStore(db *gorm.DB) *GormStore {
	return &GormStore{
		db:           db,
		users:        repo.NewGormRepository[User](db),
		refreshToken: repo.NewGormRepository[RefreshToken](db),
		regToken:     repo.NewGormRepository[RegistrationToken](db),
	}
}

func Models() []any {
	return []any{&User{}, &RefreshToken{}, &RegistrationToken{}}
}

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(Models()...)
}

func (s *GormStore) WithinTx(ctx context.Context, fn func(Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewGormStore(tx))
	})
}

func (s *GormStore) CreateUser(ctx context.Context, u *User) error {
	if err := requireID(u.ID); err != nil {
		return err
	}
	return s.users.Create(ctx, u)
}

func (s *GormStore) ByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.users.DB().WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		return nil, repo.NormalizeError(err)
	}
	return &u, nil
}

func (s *GormStore) ByID(ctx context.Context, id ID) (*User, error) {
	return s.users.GetByID(ctx, id.String())
}

func (s *GormStore) CreateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	if err := requireID(rt.ID); err != nil {
		return err
	}
	return s.refreshToken.Create(ctx, rt)
}

func (s *GormStore) ByJTI(ctx context.Context, jti string) (*RefreshToken, error) {
	var rt RefreshToken
	if err := s.refreshToken.DB().WithContext(ctx).Where("jti = ?", jti).First(&rt).Error; err != nil {
		return nil, repo.NormalizeError(err)
	}
	return &rt, nil
}

func (s *GormStore) UpdateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	return s.refreshToken.Update(ctx, rt)
}

func (s *GormStore) RevokeAllByUser(ctx context.Context, userID ID, at time.Time) error {
	return s.refreshToken.DB().WithContext(ctx).
		Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error
}

func (s *GormStore) CreateRegistrationToken(ctx context.Context, t *RegistrationToken) error {
	if err := requireID(t.ID); err != nil {
		return err
	}
	return s.regToken.Create(ctx, t)
}

func (s *GormStore) ByTokenHash(ctx context.Context, hash string) (*RegistrationToken, error) {
	var t RegistrationToken
	if err := s.regToken.DB().WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error; err != nil {
		return nil, repo.NormalizeError(err)
	}
	return &t, nil
}

func (s *GormStore) ConsumeRegistrationToken(ctx context.Context, id ID, usedBy ID, at time.Time) (bool, error) {
	result := s.regToken.DB().WithContext(ctx).
		Model(&RegistrationToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Updates(map[string]any{"used_at": at, "used_by_user_id": usedBy})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

var errMissingID = errors.New("auth: stored a record without an id")

func requireID(id ID) error {
	if id == "" {
		return errMissingID
	}
	return nil
}
