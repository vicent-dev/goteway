package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"goteway/pkg/auth"
	"goteway/pkg/request"
)

// The handlers build their own auth service out of the server's infrastructure,
// so these tests wire that infrastructure for real: sqlite for postgres and
// miniredis for redis. What comes back is the auth domain's own behaviour.

var testDBSequence atomic.Int64

const testPassword = "supersecret"

// sessionPayload mirrors what the handlers render, so the tests read the
// response the way a client would.
type sessionPayload struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	TokenType    string     `json:"token_type"`
	ExpiresAt    time.Time  `json:"expires_at"`
	User         *auth.User `json:"user"`
}

func newTestServer(t *testing.T, cfg *Config) *server {
	t.Helper()

	if cfg == nil {
		cfg = testConfig()
	}

	cache, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(cache.Close)

	db := newTestDB(t)

	s := &server{
		r:   mux.NewRouter(),
		c:   cfg,
		rdb: redis.NewClient(&redis.Options{Addr: cache.Addr()}),
		db:  db,
	}
	s.routes()

	return s
}

// newTestDB is a throwaway postgres stand in: one in memory database per test,
// so nothing leaks between them.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:app_test_%d?mode=memory&cache=shared", testDBSequence.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, auth.Migrate(db))

	return db
}

func testConfig() *Config {
	return &Config{
		Auth: AuthConfig{
			AccessSecret:         "access-secret",
			RefreshSecret:        "refresh-secret",
			AccessTTL:            15 * time.Minute,
			RefreshTTL:           24 * time.Hour,
			RegistrationTokenTTL: time.Hour,
			Issuer:               "goteway",
			Audience:             "goteway-clients",
			// The cheapest hash bcrypt offers: these tests are about the
			// handlers, not about password hashing.
			BcryptCost: bcrypt.MinCost,
		},
	}
}

func doRequest(s *server, method, target, body string) *httptest.ResponseRecorder {
	return doRequestAs(s, method, target, body, "")
}

// doRequestAs sends a request as a given bearer token, which is how an
// authenticated caller reaches the proxy.
func doRequestAs(s *server, method, target, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "10.0.0.7:5555"
	req.Header.Set("User-Agent", "goteway-test")
	if bearer != "" {
		req.Header.Set(auth.AuthorizationHeader, "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	return rec
}

func decodeSession(t *testing.T, rec *httptest.ResponseRecorder) sessionPayload {
	t.Helper()

	var payload sessionPayload
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	return payload
}

// mintRegistrationToken goes through the same service the handlers use, which
// is also how operators mint them.
func mintRegistrationToken(t *testing.T, s *server) string {
	t.Helper()

	raw, err := s.authService().IssueRegistrationToken(t.Context(), auth.IssueRegistrationTokenInput{
		IssuedBy: "test",
		TTL:      time.Hour,
	})
	require.NoError(t, err)

	return raw
}

// register creates an account through the endpoint and returns its session.
func register(t *testing.T, s *server, email string) sessionPayload {
	t.Helper()

	body := fmt.Sprintf(
		`{"email":%q,"username":"ada","password":%q,"registration_token":%q}`,
		email, testPassword, mintRegistrationToken(t, s),
	)
	rec := doRequest(s, http.MethodPost, "/auth/register", body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	return decodeSession(t, rec)
}

// deactivate disables an account behind the domain's back, the way an admin
// tool or a moderation flow would.
func deactivate(t *testing.T, s *server, email string) {
	t.Helper()

	require.NoError(t, s.db.Model(&auth.User{}).Where("email = ?", email).Update("is_active", false).Error)
}

func TestRegisterHandler(t *testing.T) {
	s := newTestServer(t, nil)

	rec := doRequest(s, http.MethodPost, "/auth/register", fmt.Sprintf(
		`{"email":"ada@example.com","username":"ada","password":%q,"registration_token":%q}`,
		testPassword, mintRegistrationToken(t, s),
	))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	session := decodeSession(t, rec)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)
	assert.Equal(t, auth.BearerScheme, session.TokenType)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), session.ExpiresAt, time.Minute)

	require.NotNil(t, session.User)
	assert.Equal(t, "ada@example.com", session.User.Email)
	assert.Equal(t, "ada", session.User.Username)
	assert.Equal(t, auth.RoleUser, session.User.Role)
	assert.True(t, session.User.IsActive)

	// The account is really persisted, and the password is not what was sent.
	u, err := auth.NewGormStore(s.db).ByEmail(t.Context(), "ada@example.com")
	require.NoError(t, err)
	assert.Equal(t, session.User.ID, u.ID)
	assert.NotEqual(t, testPassword, u.PasswordHash)
}

func TestRegisterHandlerRecordsTheCaller(t *testing.T) {
	s := newTestServer(t, nil)
	session := register(t, s, "ada@example.com")

	claims, err := auth.NewIssuer(s.c.AuthConfig()).Parse(session.RefreshToken, auth.KindRefresh)
	require.NoError(t, err)

	stored, err := auth.NewGormStore(s.db).ByJTI(t.Context(), claims.ID)
	require.NoError(t, err)
	assert.Equal(t, session.User.ID, stored.UserID)
	assert.Equal(t, "goteway-test", stored.UserAgent)
	// The port is not part of the ip the column can hold.
	assert.Equal(t, "10.0.0.7", stored.IP)
}

func TestRegisterHandlerRejectsMalformedBody(t *testing.T) {
	s := newTestServer(t, nil)

	rec := doRequest(s, http.MethodPost, "/auth/register", `{"email":`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"invalid request body"}`, rec.Body.String())
}

func TestAuthHandlerErrorMapping(t *testing.T) {
	const (
		registerPath = "/auth/register"
		loginPath    = "/auth/login"
	)

	tests := []struct {
		name         string
		path         string
		body         string
		setup        func(t *testing.T, s *server)
		wantStatus   int
		wantContains string
	}{
		{
			name:         "the domain refuses the payload",
			path:         registerPath,
			body:         `{"email":"not-an-email","username":"ada","password":"supersecret","registration_token":"whatever"}`,
			wantStatus:   http.StatusBadRequest,
			wantContains: "invalid input",
		},
		{
			name:         "a missing field is named",
			path:         registerPath,
			body:         `{"username":"ada","password":"supersecret","registration_token":"whatever"}`,
			wantStatus:   http.StatusBadRequest,
			wantContains: "email is required",
		},
		{
			name: "the email is already registered",
			path: registerPath,
			body: `{"email":"ada@example.com","username":"grace","password":"supersecret","registration_token":"%s"}`,
			setup: func(t *testing.T, s *server) {
				_ = register(t, s, "ada@example.com")
			},
			wantStatus:   http.StatusConflict,
			wantContains: "email already registered",
		},
		{
			name: "the account is disabled",
			path: loginPath,
			body: `{"email":"ada@example.com","password":"supersecret"}`,
			setup: func(t *testing.T, s *server) {
				register(t, s, "ada@example.com")
				deactivate(t, s, "ada@example.com")
			},
			wantStatus:   http.StatusForbidden,
			wantContains: "user is not active",
		},
		{
			name:         "wrong credentials",
			path:         loginPath,
			body:         `{"email":"ada@example.com","password":"wrongpassword"}`,
			wantStatus:   http.StatusUnauthorized,
			wantContains: `"error":"unauthorized"`,
		},
		{
			name:         "an unknown registration token",
			path:         registerPath,
			body:         `{"email":"ada@example.com","username":"ada","password":"supersecret","registration_token":"never-minted"}`,
			wantStatus:   http.StatusUnauthorized,
			wantContains: `"error":"unauthorized"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, nil)
			if tt.setup != nil {
				tt.setup(t, s)
			}

			// Registration needs a fresh token, which only the setup knows about.
			body := tt.body
			if strings.Contains(body, "%s") {
				body = fmt.Sprintf(body, mintRegistrationToken(t, s))
			}

			rec := doRequest(s, http.MethodPost, tt.path, body)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantContains)
		})
	}
}

func TestAuthHandlerHidesStorageFailures(t *testing.T) {
	s := newTestServer(t, nil)
	register(t, s, "ada@example.com")

	// Take the database away: what the client sees must stay the same.
	sqlDB, err := s.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	rec := doRequest(s, http.MethodPost, "/auth/login", `{"email":"ada@example.com","password":"supersecret"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":"internal error"}`, rec.Body.String())
}

func TestLoginHandler(t *testing.T) {
	s := newTestServer(t, nil)
	registered := register(t, s, "ada@example.com")

	rec := doRequest(s, http.MethodPost, "/auth/login",
		fmt.Sprintf(`{"email":"ada@example.com","password":%q}`, testPassword))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	session := decodeSession(t, rec)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)
	assert.Equal(t, auth.BearerScheme, session.TokenType)
	require.NotNil(t, session.User)
	assert.Equal(t, registered.User.ID, session.User.ID)
	assert.Equal(t, "ada@example.com", session.User.Email)
	assert.NotEqual(t, session.RefreshToken, registered.RefreshToken, "each session is its own")
}

func TestLoginHandlerRejectsMalformedBody(t *testing.T) {
	s := newTestServer(t, nil)

	rec := doRequest(s, http.MethodPost, "/auth/login", `not json`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"invalid request body"}`, rec.Body.String())
}

func TestRefreshHandlerRotatesTheToken(t *testing.T) {
	s := newTestServer(t, nil)
	registered := register(t, s, "ada@example.com")

	rec := doRequest(s, http.MethodPost, "/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, registered.RefreshToken))

	assert.Equal(t, http.StatusOK, rec.Code)
	session := decodeSession(t, rec)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEqual(t, registered.RefreshToken, session.RefreshToken, "the refresh token is rotated")
	assert.NotEqual(t, registered.AccessToken, session.AccessToken)
	require.NotNil(t, session.User)
	assert.Equal(t, registered.User.ID, session.User.ID)

	// The presented token is kept, revoked, pointing at its replacement.
	issuer := auth.NewIssuer(s.c.AuthConfig())
	oldClaims, err := issuer.Parse(registered.RefreshToken, auth.KindRefresh)
	require.NoError(t, err)
	newClaims, err := issuer.Parse(session.RefreshToken, auth.KindRefresh)
	require.NoError(t, err)

	stored, err := auth.NewGormStore(s.db).ByJTI(t.Context(), oldClaims.ID)
	require.NoError(t, err)
	assert.True(t, stored.IsRevoked())
	assert.Equal(t, newClaims.ID, stored.ReplacedByJTI)
}

func TestRefreshHandlerTreatsAReusedTokenAsAReplay(t *testing.T) {
	s := newTestServer(t, nil)
	registered := register(t, s, "ada@example.com")

	refresh := func(token string) *httptest.ResponseRecorder {
		return doRequest(s, http.MethodPost, "/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, token))
	}

	first := refresh(registered.RefreshToken)
	require.Equal(t, http.StatusOK, first.Code)
	rotated := decodeSession(t, first)

	// The old token comes back: a replay and a stolen token look the same, so
	// the whole session set goes.
	replay := refresh(registered.RefreshToken)
	assert.Equal(t, http.StatusUnauthorized, replay.Code)
	assert.JSONEq(t, `{"error":"unauthorized"}`, replay.Body.String())

	// Even the token the attacker never had is dead now.
	assert.Equal(t, http.StatusUnauthorized, refresh(rotated.RefreshToken).Code)
}

func TestRefreshHandlerAfterLogout(t *testing.T) {
	s := newTestServer(t, nil)
	registered := register(t, s, "ada@example.com")

	require.Equal(t, http.StatusNoContent, doRequest(s, http.MethodPost, "/auth/logout",
		fmt.Sprintf(`{"refresh_token":%q}`, registered.RefreshToken)).Code)

	rec := doRequest(s, http.MethodPost, "/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, registered.RefreshToken))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRefreshHandlerRejectsMalformedBody(t *testing.T) {
	s := newTestServer(t, nil)

	rec := doRequest(s, http.MethodPost, "/auth/refresh", `[]`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLogoutHandlerIsAlwaysNoContent(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "with a token", body: `%q`},
		{name: "without a token", body: `{}`},
		{name: "with a token nobody issued", body: `{"refresh_token":"stale"}`},
		{name: "with a malformed body", body: `nonsense`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, nil)
			body := tt.body
			if strings.Contains(body, "%q") {
				body = fmt.Sprintf(body, register(t, s, "ada@example.com").RefreshToken)
			}

			rec := doRequest(s, http.MethodPost, "/auth/logout", body)

			// The client always sees the same answer: the session is gone.
			assert.Equal(t, http.StatusNoContent, rec.Code)
			assert.Empty(t, rec.Body.String())
		})
	}
}

func TestLogoutHandlerRevokesTheStoredSession(t *testing.T) {
	s := newTestServer(t, nil)
	session := register(t, s, "ada@example.com")

	require.Equal(t, http.StatusNoContent, doRequest(s, http.MethodPost, "/auth/logout",
		fmt.Sprintf(`{"refresh_token":%q}`, session.RefreshToken)).Code)

	claims, err := auth.NewIssuer(s.c.AuthConfig()).Parse(session.RefreshToken, auth.KindRefresh)
	require.NoError(t, err)

	stored, err := auth.NewGormStore(s.db).ByJTI(t.Context(), claims.ID)
	require.NoError(t, err)
	assert.True(t, stored.IsRevoked())
	assert.Empty(t, stored.ReplacedByJTI, "a logout does not rotate into a successor")
}

func TestAuthRoutesFallThroughOnOtherMethods(t *testing.T) {
	s := newTestServer(t, nil)

	rec := doRequest(s, http.MethodGet, "/auth/login", "")

	// The auth endpoints are POST only, and a GET matches neither of them nor
	// the /auth prefix, so it lands on the proxy, which demands a token.
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestProxyRequiresAuthentication(t *testing.T) {
	s := newTestServer(t, nil)

	rec := doRequest(s, http.MethodGet, "/admin/users", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())
}

func TestProxyRejectsAnExpiredToken(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.AccessTTL = time.Second
	// The default leeway is 5s, which is longer than this test's token lives.
	cfg.Auth.ClockSkew = time.Nanosecond
	s := newTestServer(t, cfg)
	session := register(t, s, "ada@example.com")

	// The token is valid now, and not in a moment.
	claims, err := auth.NewIssuer(cfg.AuthConfig()).Parse(session.AccessToken, auth.KindAccess)
	require.NoError(t, err)
	require.True(t, claims.ExpiresAt.Time.After(time.Now()))

	time.Sleep(1100 * time.Millisecond)

	rec := doRequestAs(s, http.MethodGet, "/admin/users", "", session.AccessToken)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestProxyForwardsToTheUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer upstream.Close()

	cfg := testConfig()
	cfg.Services = ServicesConfig{
		External: []Upstream{{Path: "public", Host: upstream.URL[len("http://"):]}},
	}
	s := newTestServer(t, cfg)
	session := register(t, s, "ada@example.com")

	rec := doRequestAs(s, http.MethodGet, "/public/thing", "", session.AccessToken)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"hello":"world"}`, rec.Body.String())
	assert.Equal(t, "yes", rec.Header().Get("X-Upstream"))
}

func TestProxyReportsUnknownService(t *testing.T) {
	s := newTestServer(t, nil)
	session := register(t, s, "ada@example.com")

	rec := doRequestAs(s, http.MethodGet, "/unknown/path", "", session.AccessToken)

	// Authenticated, but nothing is configured to serve it.
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":"not found"}`, rec.Body.String())
}

func TestProxyReportsAnUnreachableUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("never"))
	}))
	host := upstream.URL[len("http://"):]
	upstream.Close()

	cfg := testConfig()
	cfg.Services = ServicesConfig{
		External: []Upstream{{Path: "public", Host: host}},
	}
	s := newTestServer(t, cfg)
	session := register(t, s, "ada@example.com")

	rec := doRequestAs(s, http.MethodGet, "/public/thing", "", session.AccessToken)

	// Our upstream failed, not the request: that is a gateway status.
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.JSONEq(t, `{"error":"service not available"}`, rec.Body.String())
	// The host and the dial error stay in the log.
	assert.NotContains(t, rec.Body.String(), host)
}

// TestWriteRequestError maps the whole error surface of the proxy, including
// the branches no route can reach on its own, the same way
// TestAuthHandlerErrorMapping covers the auth domain.
func TestWriteRequestError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "the path is real, the caller is not allowed on it",
			err:        request.ErrAccessDenied,
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"error":"unauthorized"}`,
		},
		{
			name:       "nothing is configured to serve the path",
			err:        fmt.Errorf("%w: /unknown", request.ErrServiceNotFound),
			wantStatus: http.StatusNotFound,
			wantBody:   `{"error":"not found"}`,
		},
		{
			name:       "the upstream did not answer",
			err:        fmt.Errorf("%w: dial tcp 10.0.0.1:8080: connection refused", request.ErrServiceUnavailable),
			wantStatus: http.StatusBadGateway,
			wantBody:   `{"error":"service not available"}`,
		},
		{
			name:       "the configured host is unusable",
			err:        fmt.Errorf("%w: invalid character in host name", request.ErrInvalidUpstreamURL),
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal error"}`,
		},
		{
			name:       "an error nobody has classified yet",
			err:        errors.New("something else went wrong"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeRequestError(rec, httptest.NewRequest(http.MethodGet, "/api/test", nil), tt.err)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.JSONEq(t, tt.wantBody, rec.Body.String())
		})
	}
}

// TestWriteRequestErrorHidesTheCause checks the property the whole mapping
// exists for: the proxy errors name upstream hosts and dial failures, and none
// of that may be echoed to the client.
func TestWriteRequestErrorHidesTheCause(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRequestError(rec, httptest.NewRequest(http.MethodGet, "/api/test", nil),
		fmt.Errorf("%w: %v", request.ErrServiceUnavailable,
			errors.New(`Get "http://internal.example.com:8080/test": dial tcp: connection refused`)))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.JSONEq(t, `{"error":"service not available"}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "internal.example.com")
	assert.NotContains(t, rec.Body.String(), "connection refused")
}
