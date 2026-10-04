package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goteway/pkg/auth"
	"goteway/pkg/log"
)

func TestLoggingMiddleware(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ctx := r.Context()
		assert.NotNil(t, ctx.Value(log.METHOD_CTX_LOG_KEY))
		assert.NotNil(t, ctx.Value(log.PATH_CTX_LOG_KEY))
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test/path", nil)
	req.Header.Set(auth.AuthorizationHeader, "Bearer testtoken")
	rec := httptest.NewRecorder()

	mw := loggingMiddleware(next)
	mw.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestJsonMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mw := jsonMiddleware(next)
	mw.ServeHTTP(rec, req)

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestRateLimiterMiddleware_AllowsRequests(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mw := rateLimiterMiddleware(next)
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimiterMiddleware_Throttle(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := rateLimiterMiddleware(next)
	allowed := 0
	throttled := 0

	// Make many requests quickly - global limiter is 5 rps burst 10
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			allowed++
		} else if rec.Code == http.StatusTooManyRequests {
			throttled++
		}
	}

	// With burst 10, first 10 should be allowed, rest throttled
	assert.GreaterOrEqual(t, allowed, 1)
	assert.GreaterOrEqual(t, throttled, 1)
}

// The middleware delegates to a service the server builds on the spot, so these
// tests go through a real session rather than a stubbed authenticator.

func TestAuthMiddlewareRejectsAnonymousRequests(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "no header"},
		{name: "not a bearer scheme", header: "Basic dXNlcjpwYXNz"},
		{name: "invalid token", header: "Bearer nope"},
		{name: "a token signed with another secret", header: "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, nil)
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			})

			req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
			if tt.header != "" {
				req.Header.Set(auth.AuthorizationHeader, tt.header)
			}
			rec := httptest.NewRecorder()

			s.authMiddleware(next).ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())
			assert.False(t, called, "the guarded handler must not run")
		})
	}
}

func TestAuthMiddlewareInjectsThePrincipal(t *testing.T) {
	s := newTestServer(t, nil)
	session := register(t, s, "ada@example.com")

	var seen *auth.Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromRequest(r)
		require.True(t, ok, "the principal must reach the guarded handler")
		seen = principal
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set(auth.AuthorizationHeader, "Bearer "+session.AccessToken)
	rec := httptest.NewRecorder()

	s.authMiddleware(next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, seen)
	assert.Equal(t, session.User.ID, seen.UserID)

	claims, err := auth.NewIssuer(s.c.AuthConfig()).Parse(session.AccessToken, auth.KindAccess)
	require.NoError(t, err)
	assert.Equal(t, claims.ID, seen.TokenID, "the principal names the token that was presented")
}

func TestAuthMiddlewareSurvivesADatabaseOutage(t *testing.T) {
	// Verifying an access token hits no storage: the signed token is its own
	// proof, so a database outage must not take the proxy down with it. The
	// paths that do read storage report their failures as a 500, which is what
	// TestAuthHandlerHidesStorageFailures covers.
	s := newTestServer(t, nil)
	session := register(t, s, "ada@example.com")

	sqlDB, err := s.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	var seen *auth.Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromRequest(r)
		require.True(t, ok)
		seen = principal
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set(auth.AuthorizationHeader, "Bearer "+session.AccessToken)
	rec := httptest.NewRecorder()

	s.authMiddleware(next).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, seen)
	assert.Equal(t, session.User.ID, seen.UserID)
}
