package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"goteway/pkg/auth"
	"goteway/pkg/log"

	"github.com/stretchr/testify/assert"
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
	req.Header.Set(auth.BEARER_TOKEN_HEADER_KEY, "Bearer testtoken")
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
