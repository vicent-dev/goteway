package auth

import (
	"context"
	"time"
)

// UserStore persists accounts.
type UserStore interface {
	CreateUser(ctx context.Context, u *User) error
	ByEmail(ctx context.Context, email string) (*User, error)
	ByID(ctx context.Context, id ID) (*User, error)
}

// RefreshTokenStore persists issued sessions.
type RefreshTokenStore interface {
	CreateRefreshToken(ctx context.Context, rt *RefreshToken) error
	ByJTI(ctx context.Context, jti string) (*RefreshToken, error)
	UpdateRefreshToken(ctx context.Context, rt *RefreshToken) error
	// RevokeAllByUser revokes every session of a user at once, which is how
	// token reuse is answered and how an account can be logged out everywhere.
	RevokeAllByUser(ctx context.Context, userID ID, at time.Time) error
}

// RegistrationTokenStore persists one time registration tokens.
type RegistrationTokenStore interface {
	CreateRegistrationToken(ctx context.Context, t *RegistrationToken) error
	ByTokenHash(ctx context.Context, hash string) (*RegistrationToken, error)
	// ConsumeRegistrationToken atomically marks a token as used by usedBy. It
	// reports whether this call is the one that consumed it, so two
	// registrations racing for the same token cannot both succeed.
	ConsumeRegistrationToken(ctx context.Context, id ID, usedBy ID, at time.Time) (bool, error)
}

// Store is every port the auth service needs, plus the transaction boundary
// that lets a registration be all or nothing.
type Store interface {
	UserStore
	RefreshTokenStore
	RegistrationTokenStore
	// WithinTx runs fn in a single transaction, passing it a Store bound to
	// that transaction. Returning an error from fn rolls everything back.
	WithinTx(ctx context.Context, fn func(Store) error) error
}
