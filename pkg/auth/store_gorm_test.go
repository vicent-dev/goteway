package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"goteway/pkg/repo"
)

func newTestGormStore(t *testing.T) *GormStore {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)

	for _, model := range Models() {
		require.NoError(t, db.Migrator().DropTable(model))
	}
	require.NoError(t, Migrate(db))

	return NewGormStore(db)
}

// newRowID returns a fresh valid identifier, so each row a test inserts has a
// primary key of its own: the service is what assigns them, and the store
// refuses anything that arrives without one.
func newRowID(t *testing.T) ID {
	t.Helper()

	id, err := newID(testNow)
	require.NoError(t, err)

	return id
}

func TestMigrateCreatesTheAuthTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasTable(&User{}))
	assert.True(t, db.Migrator().HasTable(&RefreshToken{}))
	assert.True(t, db.Migrator().HasTable(&RegistrationToken{}))
}

func TestMigrateStoresIdentifiersAsText(t *testing.T) {
	// Guards the column types themselves. A driver is free to pick something
	// else for a Go string, and an identifier stored as an integer would bring
	// back the enumeration this scheme exists to remove — silently, since
	// everything would still round-trip.
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))

	models := map[string]any{
		"User":              &User{},
		"RefreshToken":      &RefreshToken{},
		"RegistrationToken": &RegistrationToken{},
	}
	textual := map[string][]string{
		"User":              {"id"},
		"RefreshToken":      {"id", "user_id"},
		"RegistrationToken": {"id", "used_by_user_id"},
	}

	for name, model := range models {
		columns, err := db.Migrator().ColumnTypes(model)
		require.NoError(t, err)

		byName := make(map[string]string, len(columns))
		for _, column := range columns {
			byName[column.Name()] = strings.ToLower(column.DatabaseTypeName())
		}

		for _, column := range textual[name] {
			declared, ok := byName[column]
			require.True(t, ok, "%s should have a %s column", name, column)
			assert.NotContains(t, declared, "int",
				"%s.%s must not be an integer column, got %q", name, column, declared)
		}
	}
}

func TestGormStoreUserLookups(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	u := &User{ID: newRowID(t), Email: "ada@example.com", Username: "ada", PasswordHash: "hash", Role: RoleUser, IsActive: true}
	require.NoError(t, store.CreateUser(ctx, u))
	assert.NotEmpty(t, u.ID)

	byEmail, err := store.ByEmail(ctx, "ada@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, byEmail.ID)

	byID, err := store.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "ada@example.com", byID.Email)
}

func TestGormStoreRefusesARecordWithoutAnID(t *testing.T) {
	// There is no sequence to fall back on, so an unidentified row would be
	// stored under the empty string and collide with the next one.
	store := newTestGormStore(t)
	ctx := context.Background()

	assert.ErrorIs(t, store.CreateUser(ctx, &User{Email: "ada@example.com"}), errMissingID)
	assert.ErrorIs(t, store.CreateRefreshToken(ctx, &RefreshToken{JTI: "jti-1"}), errMissingID)
	assert.ErrorIs(t, store.CreateRegistrationToken(ctx, &RegistrationToken{TokenHash: "hash"}), errMissingID)

	_, err := store.ByEmail(ctx, "ada@example.com")
	assert.ErrorIs(t, err, repo.ErrNotFound, "nothing is written")
}

func TestGormStoreMissingRecordsReportRepoErrNotFound(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	_, err := store.ByEmail(ctx, "ghost@example.com")
	assert.ErrorIs(t, err, repo.ErrNotFound)

	_, err = store.ByID(ctx, testID())
	assert.ErrorIs(t, err, repo.ErrNotFound)

	_, err = store.ByJTI(ctx, "unknown-jti")
	assert.ErrorIs(t, err, repo.ErrNotFound)

	_, err = store.ByTokenHash(ctx, "unknown-hash")
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

func TestGormStoreRefreshTokenLifecycle(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	rt := &RefreshToken{
		ID:        newRowID(t),
		JTI:       "jti-1",
		UserID:    testID(),
		TokenHash: "hash-1",
		ExpiresAt: now.Add(time.Hour),
		UserAgent: "curl",
		IP:        "127.0.0.1",
	}
	require.NoError(t, store.CreateRefreshToken(ctx, rt))

	stored, err := store.ByJTI(ctx, "jti-1")
	require.NoError(t, err)
	assert.Equal(t, testID(), stored.UserID)
	assert.Equal(t, rt.ID, stored.ID)
	assert.False(t, stored.IsRevoked())

	stored.Revoke(now, "jti-2")
	require.NoError(t, store.UpdateRefreshToken(ctx, stored))

	revoked, err := store.ByJTI(ctx, "jti-1")
	require.NoError(t, err)
	assert.True(t, revoked.IsRevoked())
	assert.Equal(t, "jti-2", revoked.ReplacedByJTI)
}

func TestGormStoreRevokeAllByUser(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	first, second := testID(), otherID()

	for _, rt := range []*RefreshToken{
		{ID: newRowID(t), JTI: "u1-a", UserID: first, ExpiresAt: now.Add(time.Hour)},
		{ID: newRowID(t), JTI: "u1-b", UserID: first, ExpiresAt: now.Add(time.Hour)},
		{ID: newRowID(t), JTI: "u2-a", UserID: second, ExpiresAt: now.Add(time.Hour)},
	} {
		require.NoError(t, store.CreateRefreshToken(ctx, rt))
	}

	require.NoError(t, store.RevokeAllByUser(ctx, first, now))

	first_, err := store.ByJTI(ctx, "u1-a")
	require.NoError(t, err)
	second_, err := store.ByJTI(ctx, "u1-b")
	require.NoError(t, err)
	other, err := store.ByJTI(ctx, "u2-a")
	require.NoError(t, err)

	assert.True(t, first_.IsRevoked())
	assert.True(t, second_.IsRevoked())
	assert.False(t, other.IsRevoked(), "another user's session is untouched")
}

func TestGormStoreConsumeRegistrationTokenIsAtomic(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	token := &RegistrationToken{ID: newRowID(t), TokenHash: "hash", IssuedBy: "admin", ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, store.CreateRegistrationToken(ctx, token))

	consumed, err := store.ConsumeRegistrationToken(ctx, token.ID, testID(), now)
	require.NoError(t, err)
	assert.True(t, consumed)

	// A second registration racing for the same token loses, and says so.
	consumed, err = store.ConsumeRegistrationToken(ctx, token.ID, otherID(), now)
	require.NoError(t, err)
	assert.False(t, consumed)

	stored, err := store.ByTokenHash(ctx, "hash")
	require.NoError(t, err)
	require.NotNil(t, stored.UsedAt)
	assert.True(t, now.Equal(*stored.UsedAt), "used_at is %v, want %v", stored.UsedAt, now)
	require.NotNil(t, stored.UsedByUserID)
	assert.Equal(t, testID(), *stored.UsedByUserID, "the first caller keeps the attribution")
}

func TestGormStoreConsumeUnknownRegistrationToken(t *testing.T) {
	store := newTestGormStore(t)

	// A conditional update that matches nothing is not an error: the token is
	// simply not consumable, whether it is gone or already taken.
	consumed, err := store.ConsumeRegistrationToken(context.Background(), newRowID(t), testID(), time.Now())

	assert.NoError(t, err)
	assert.False(t, consumed)
}

func TestGormStoreWithinTxCommits(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	err := store.WithinTx(ctx, func(tx Store) error {
		if err := tx.CreateUser(ctx, &User{ID: newRowID(t), Email: "ada@example.com", IsActive: true}); err != nil {
			return err
		}
		return tx.CreateRefreshToken(ctx, &RefreshToken{ID: newRowID(t), JTI: "jti-1", UserID: testID(), ExpiresAt: time.Now().Add(time.Hour)})
	})

	require.NoError(t, err)

	_, err = store.ByEmail(ctx, "ada@example.com")
	assert.NoError(t, err)
	_, err = store.ByJTI(ctx, "jti-1")
	assert.NoError(t, err)
}

func TestGormStoreWithinTxRollsBack(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	boom := errors.New("something went wrong")

	err := store.WithinTx(ctx, func(tx Store) error {
		if err := tx.CreateUser(ctx, &User{ID: newRowID(t), Email: "ada@example.com", IsActive: true}); err != nil {
			return err
		}
		if err := tx.CreateRefreshToken(ctx, &RefreshToken{ID: newRowID(t), JTI: "jti-1", UserID: testID(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			return err
		}
		return boom
	})

	assert.ErrorIs(t, err, boom)

	_, err = store.ByEmail(ctx, "ada@example.com")
	assert.ErrorIs(t, err, repo.ErrNotFound)
	_, err = store.ByJTI(ctx, "jti-1")
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

func TestServiceRunsAgainstTheGormStore(t *testing.T) {
	// The service is written against Store; this runs the happy path on the
	// real adapter rather than on the fake.
	store := newTestGormStore(t)
	svc := NewService(Config{AccessSecret: "a", RefreshSecret: "b", BcryptCost: bcryptMinCost}, store, nil)
	ctx := context.Background()

	raw, err := svc.IssueRegistrationToken(ctx, IssueRegistrationTokenInput{IssuedBy: "admin"})
	require.NoError(t, err)

	session, err := svc.Register(ctx, RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: raw,
	})
	require.NoError(t, err)
	require.NotNil(t, session.User)

	principal, err := svc.Authenticate(session.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, session.User.ID, principal.UserID)

	loggedIn, err := svc.Login(ctx, LoginInput{Email: "ada@example.com", Password: "supersecret"})
	require.NoError(t, err)

	refreshed, err := svc.Refresh(ctx, RefreshInput{RefreshToken: loggedIn.RefreshToken})
	require.NoError(t, err)

	// The rotated token is refused, and the reuse drops the whole family.
	_, err = svc.Refresh(ctx, RefreshInput{RefreshToken: loggedIn.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)
	_, err = svc.Refresh(ctx, RefreshInput{RefreshToken: refreshed.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)

	require.NoError(t, svc.Logout(ctx, LogoutInput{}))
}
