package request

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goteway/pkg/auth"
	"goteway/pkg/cache"
)

// mockCache keeps the Cache contract, including reporting a miss as
// cache.ErrNotFound rather than as silence, and can be told to fail so the
// client's handling of an unusable cache is testable.
type mockCache[T cache.Cacheable] struct {
	mu     sync.Mutex
	items  map[string]string
	getErr error
	setErr error
}

func newMockCache[T cache.Cacheable]() *mockCache[T] {
	return &mockCache[T]{items: make(map[string]string)}
}

func (m *mockCache[T]) Set(ctx context.Context, t T) error {
	if m.setErr != nil {
		return m.setErr
	}

	value, err := t.Value()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[t.Key()] = value

	return nil
}

func (m *mockCache[T]) Get(ctx context.Context, t T) error {
	if m.getErr != nil {
		return m.getErr
	}

	m.mu.Lock()
	stored, ok := m.items[t.Key()]
	m.mu.Unlock()
	if !ok {
		return cache.ErrNotFound
	}

	return t.SetValue(stored)
}

func (m *mockCache[T]) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.items[key]

	return ok
}

// newTestClient wires a client over a cache. NewClient takes the interface by
// pointer, so the mock is handed over through a variable of its own.
func newTestClient(mc cache.Cache[*Call], services ServicesConfig) *Client {
	c := cache.Cache[*Call](mc)
	return NewClient(&c, services)
}

// upstreamAt starts a server answering with body and returns the client config
// that routes the api prefix to it.
func upstreamAt(t *testing.T, body string, hits *atomic.Int64) (ServicesConfig, func()) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	return ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: upstream.URL[len("http://"):]}},
	}, upstream.Close
}

func TestClient_RequestCacheMiss(t *testing.T) {
	services, _ := upstreamAt(t, `{"message": "hello"}`, nil)
	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	call, err := client.Request(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, call)
	require.NotNil(t, call.Response)
	assert.Equal(t, http.StatusOK, call.Response.StatusCode)

	body, err := io.ReadAll(call.Response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"message": "hello"}`, string(body))
}

// TestClient_ServesTheSecondRequestFromCache covers the whole caching round
// trip: the upstream answers once, the response is cached asynchronously, and
// an identical request is then answered without touching it again.
func TestClient_ServesTheSecondRequestFromCache(t *testing.T) {
	var hits atomic.Int64
	services, _ := upstreamAt(t, `{"message": "hello"}`, &hits)

	mc := newMockCache[*Call]()
	client := newTestClient(mc, services)

	// Two identical requests rather than one reused twice: forwarding the first
	// one consumes its body, and the body is part of the cache key.
	newRequest := func() *http.Request {
		r, err := http.NewRequest("POST", "http://gateway/api/data", bytes.NewReader([]byte("test")))
		require.NoError(t, err)
		r.Header.Set("X-Test", "1")
		return r
	}

	first, err := client.Request(context.Background(), newRequest())
	require.NoError(t, err)

	// The write happens on its own goroutine, so the test waits for it instead
	// of assuming it has already landed.
	require.Eventually(t, func() bool { return mc.has(first.Key()) },
		time.Second, 5*time.Millisecond, "the response was never cached")

	// Caching read a snapshot of the body, not the reader the client is being
	// served from: the first response is still readable in full.
	body, err := io.ReadAll(first.Response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"message": "hello"}`, string(body))

	second, err := client.Request(context.Background(), newRequest())
	require.NoError(t, err)
	require.NotNil(t, second.Response)
	assert.Equal(t, int64(1), hits.Load(), "the upstream was only asked once")

	cached, err := io.ReadAll(second.Response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"message": "hello"}`, string(cached))
}

func TestClient_InternalRequiresAPrincipal(t *testing.T) {
	services := ServicesConfig{
		Internal: []ServiceConfig{
			{Path: "admin", Host: "admin:8080"},
		},
	}

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/admin/users", nil)
	require.NoError(t, err)
	ctx := context.Background() // no principal

	call, err := client.Request(ctx, req)

	assert.ErrorIs(t, err, ErrAccessDenied)
	assert.Nil(t, call)
}

func TestClient_InternalWithAPrincipal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	services := ServicesConfig{
		Internal: []ServiceConfig{
			{Path: "admin", Host: upstream.URL[len("http://"):]},
		},
	}

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/admin/users", nil)
	require.NoError(t, err)
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{UserID: 7})

	call, err := client.Request(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, call)
	assert.Equal(t, http.StatusOK, call.Response.StatusCode)
}

func TestClient_ServiceNotFound(t *testing.T) {
	client := newTestClient(newMockCache[*Call](), ServicesConfig{})

	req, err := http.NewRequest("GET", "http://gateway/unknown", nil)
	require.NoError(t, err)

	call, err := client.Request(context.Background(), req)

	assert.ErrorIs(t, err, ErrServiceNotFound)
	assert.Nil(t, call)
}

func TestClient_UpstreamUnreachable(t *testing.T) {
	// A server that is closed right away, so nothing answers on its port.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))
	host := upstream.URL[len("http://"):]
	upstream.Close()

	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: host}},
	}

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	call, err := client.Request(context.Background(), req)

	// The cause is kept, so an operator can read which upstream failed, while
	// the sentinel is what a caller classifies on.
	assert.ErrorIs(t, err, ErrServiceUnavailable)
	assert.ErrorContains(t, err, host)
	assert.Nil(t, call)
}

func TestClient_UnusableUpstreamHost(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: "not a host:8080"},
		},
	}

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	call, err := client.Request(context.Background(), req)

	// A host that cannot be parsed comes from the configuration, so it is
	// reported apart from an upstream that was merely down.
	assert.ErrorIs(t, err, ErrInvalidUpstreamURL)
	assert.Nil(t, call)
}

func TestClient_UnreadableCacheStillProxies(t *testing.T) {
	services, _ := upstreamAt(t, `{"message": "hello"}`, nil)

	mc := newMockCache[*Call]()
	mc.getErr = errors.New("cache: get: connection refused")
	client := newTestClient(mc, services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	// A cache that cannot be read is an outage, not a failed request.
	call, err := client.Request(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, call)
	assert.Equal(t, http.StatusOK, call.Response.StatusCode)
}

func TestClient_UnwritableCacheDoesNotFailTheRequest(t *testing.T) {
	services, _ := upstreamAt(t, `ok`, nil)

	mc := newMockCache[*Call]()
	mc.setErr = errors.New("cache: set: connection refused")
	client := newTestClient(mc, services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	// The response has already been fetched and is about to be returned, so a
	// caching failure must not turn into an error for the caller.
	call, err := client.Request(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, call)
	assert.Equal(t, http.StatusOK, call.Response.StatusCode)
}
