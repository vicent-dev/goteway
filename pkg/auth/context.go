package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const AuthorizationHeader = "Authorization"

const bearerPrefix = "bearer"

type Principal struct {
	UserID    ID
	TokenID   string
	ExpiresAt time.Time
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(*Principal)
	return p, ok && p != nil
}

func PrincipalFromRequest(r *http.Request) (*Principal, bool) {
	return PrincipalFromContext(r.Context())
}

func BearerToken(header http.Header) (string, error) {
	raw := strings.TrimSpace(header.Get(AuthorizationHeader))
	if raw == "" {
		return "", ErrMissingToken
	}

	scheme, token, found := strings.Cut(raw, " ")
	if !found || !strings.EqualFold(scheme, bearerPrefix) {
		return "", ErrMissingToken
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrMissingToken
	}
	return token, nil
}
