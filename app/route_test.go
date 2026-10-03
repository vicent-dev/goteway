package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestRoutes_AuthLoginStub(t *testing.T) {
	s := &server{
		r: mux.NewRouter(),
		c: &config{},
	}
	s.redis()
	s.routes()

	req := httptest.NewRequest("POST", "/auth/login", nil)
	rec := httptest.NewRecorder()

	s.r.ServeHTTP(rec, req)
	// Stub handler returns empty body with 200 OK
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRoutes_AuthLogoutStub(t *testing.T) {
	s := &server{
		r: mux.NewRouter(),
		c: &config{},
	}
	s.redis()
	s.routes()

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	rec := httptest.NewRecorder()

	s.r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRoutes_DefaultHandlerRoutes(t *testing.T) {
	s := &server{
		r: mux.NewRouter(),
		c: &config{
			Services: struct {
				Internal []service `yaml:"internal"`
				External []service `yaml:"external"`
			}{},
		},
	}
	s.redis()
	s.routes()

	req := httptest.NewRequest("GET", "/some/random/path", nil)
	rec := httptest.NewRecorder()

	s.r.ServeHTTP(rec, req)
	// No service configured - should return error
	assert.NotEqual(t, http.StatusNotFound, rec.Code) // Goes to default handler
}
