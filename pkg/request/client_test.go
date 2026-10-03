package request

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"goteway/pkg/auth"
	"goteway/pkg/cache"

	"github.com/stretchr/testify/assert"
)

type mockCache[T cache.Cacheable] struct {
	items map[string]string
	getFn func(T)
	setFn func(T)
}

func newMockCache[T cache.Cacheable]() *mockCache[T] {
	return &mockCache[T]{
		items: make(map[string]string),
	}
}

func (m *mockCache[T]) Set(t T) {
	m.items[t.Key()] = t.Value()
	if m.setFn != nil {
		m.setFn(t)
	}
}

func (m *mockCache[T]) Get(t T) {
	if v, ok := m.items[t.Key()]; ok {
		t.SetValue(v)
		return
	}
	if m.getFn != nil {
		m.getFn(t)
	}
}

func TestClient_RequestCacheMiss(t *testing.T) {
	// Set up upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "hello"}`))
	}))
	defer upstream.Close()

	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: upstream.URL[7:]}, // remove http://
		},
	}

	mc := newMockCache[*Call]()
	c := cache.Cache[*Call](mc)
	client := NewClient(&c, services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	assert.NoError(t, err)
	ctx := context.Background()

	call, status, err := client.Request(ctx, httptest.NewRecorder(), req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.NotNil(t, call)
	assert.NotNil(t, call.Response)
}

func TestClient_RequestCacheHit(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: "backend:8080"},
		},
	}

	mc := newMockCache[*Call]()
	// Pre-populate cache
	call := &Call{id: "testkey", Response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"X-Cached": {"true"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`cached`))),
	}}
	mc.items["testkey"] = call.Value()

	c := cache.Cache[*Call](mc)
	client := NewClient(&c, services)

	// Need to make request that generates same key - but easier to test by mocking
	// Or just construct properly
	body := []byte(`test`)
	req, err := http.NewRequest("POST", "http://gateway/api/data", bytes.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("X-Test", "1")

	// Make first call structure
	call1, err := NewCall(req, services)
	assert.NoError(t, err)
	key := call1.Key()

	// Put in cache
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"X-Cached": {"true"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`fromcache`))),
	}
	callCached := &Call{id: key, Response: resp}
	mc.items[key] = callCached.Value()

	// Reset body
	req.Body = io.NopCloser(bytes.NewReader(body))
	call2, status, err := client.Request(context.Background(), httptest.NewRecorder(), req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.NotNil(t, call2.Response)
}

func TestClient_InternalRequiresAPrincipal(t *testing.T) {
	services := ServicesConfig{
		Internal: []ServiceConfig{
			{Path: "admin", Host: "admin:8080"},
		},
	}

	mc := newMockCache[*Call]()
	c := cache.Cache[*Call](mc)
	client := NewClient(&c, services)

	req, err := http.NewRequest("GET", "http://gateway/admin/users", nil)
	assert.NoError(t, err)
	ctx := context.Background() // no principal

	call, status, err := client.Request(ctx, httptest.NewRecorder(), req)
	assert.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Nil(t, call)
}

func TestClient_InternalWithAPrincipal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	services := ServicesConfig{
		Internal: []ServiceConfig{
			{Path: "admin", Host: upstream.URL[7:]},
		},
	}

	mc := newMockCache[*Call]()
	c := cache.Cache[*Call](mc)
	client := NewClient(&c, services)

	req, err := http.NewRequest("GET", "http://gateway/admin/users", nil)
	assert.NoError(t, err)
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{UserID: 7})

	call, status, err := client.Request(ctx, httptest.NewRecorder(), req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.NotNil(t, call)
}

func TestClient_ServiceNotFound(t *testing.T) {
	services := ServicesConfig{}
	mc := newMockCache[*Call]()
	c := cache.Cache[*Call](mc)
	client := NewClient(&c, services)

	req, err := http.NewRequest("GET", "http://gateway/unknown", nil)
	assert.NoError(t, err)

	call, status, err := client.Request(context.Background(), httptest.NewRecorder(), req)
	assert.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Nil(t, call)
}

func TestClient_UpstreamErrorMasksInternals(t *testing.T) {
	// Server that will cause error (close immediately)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))
	upstream.Close()

	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: upstream.URL[7:]},
		},
	}

	mc := newMockCache[*Call]()
	c := cache.Cache[*Call](mc)
	client := NewClient(&c, services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	assert.NoError(t, err)

	_, status, err := client.Request(context.Background(), httptest.NewRecorder(), req)
	assert.Error(t, err)
	assert.Equal(t, "service not available", err.Error())
	assert.Equal(t, http.StatusBadRequest, status)
}
