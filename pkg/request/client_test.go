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
//
// It refuses a cancelled context the way the redis client does, which is what
// makes it able to tell a cache write that was abandoned from one that was
// carried out.
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
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
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

// upstreamCapturing starts a server that records the last request it received,
// so a test can assert on what actually crossed the gateway instead of on what
// the gateway was handed.
func upstreamCapturing(t *testing.T, status int, body string) (ServicesConfig, func() *http.Request) {
	t.Helper()

	var mu sync.Mutex
	var received *http.Request

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		received = r.Clone(context.Background())
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: upstream.URL[len("http://"):]}},
	}

	return services, func() *http.Request {
		mu.Lock()
		defer mu.Unlock()
		return received
	}
}

func TestIsHopByHop(t *testing.T) {
	// The lookup is case-insensitive because header names arrive from the wire
	// in whatever case the client chose, and the strip is only correct if it
	// catches every spelling.
	for _, name := range []string{"Connection", "connection", "TRANSFER-ENCODING", "Upgrade", "keep-alive"} {
		assert.True(t, IsHopByHop(name), name)
	}
	for _, name := range []string{"Content-Type", "Authorization", "X-Request-Id", "Cookie", ""} {
		assert.False(t, IsHopByHop(name), name)
	}
}

// TestClient_ForwardsTheCallersHeaders is the contract that a proxied request
// keeps its identity: what the caller sent arrives, so a service behind the
// gateway sees the same content type, cookies and metadata the client did.
func TestClient_ForwardsTheCallersHeaders(t *testing.T) {
	services, received := upstreamCapturing(t, http.StatusOK, `ok`)

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("POST", "http://gateway/api/data", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "abc-123")
	req.AddCookie(&http.Cookie{Name: "session", Value: "xyz"})

	_, err = client.Request(context.Background(), req)
	require.NoError(t, err)

	got := received()
	require.NotNil(t, got, "the upstream was never reached")
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
	assert.Equal(t, "abc-123", got.Header.Get("X-Request-Id"))
	assert.Equal(t, "session=xyz", got.Header.Get("Cookie"))
}

// TestClient_StripsTheGatewayBearerAndHopByHopHeaders covers the two things that
// must not cross the gateway: the bearer is a credential valid against this
// gateway and no other, and the framing headers describe one connection, which
// the upstream and the client do not share.
func TestClient_StripsTheGatewayBearerAndHopByHopHeaders(t *testing.T) {
	services, received := upstreamCapturing(t, http.StatusOK, `ok`)

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)
	req.Header.Set(auth.AuthorizationHeader, "Bearer a-gateway-token")
	req.Header.Set("X-Kept", "yes")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Authorization", "Basic something")
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("Upgrade", "websocket")

	_, err = client.Request(context.Background(), req)
	require.NoError(t, err)

	got := received()
	require.NotNil(t, got)
	assert.Empty(t, got.Header.Get(auth.AuthorizationHeader),
		"a token valid against the gateway must not be handed to an upstream")
	assert.Equal(t, "yes", got.Header.Get("X-Kept"),
		"stripping the bearer must not cost the headers around it")
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authorization", "Transfer-Encoding", "Upgrade"} {
		assert.Empty(t, got.Header.Get(name), "%s is scoped to one connection", name)
	}
}

// TestClient_DoesNotAliasTheCallersHeaders is why the header map is cloned:
// NewCall rewrites the inbound request in place, so a shared map would let the
// outbound request keep mutating the caller's headers.
func TestClient_DoesNotAliasTheCallersHeaders(t *testing.T) {
	services, received := upstreamCapturing(t, http.StatusOK, `ok`)

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/api/test", nil)
	require.NoError(t, err)
	req.Header.Set("X-Test", "one")

	_, err = client.Request(context.Background(), req)
	require.NoError(t, err)

	// What the upstream ended up sending is not the caller's map, so writing to
	// the request afterwards leaves both alone.
	req.Header.Set("X-Late", "two")

	got := received()
	require.NotNil(t, got)
	assert.Empty(t, got.Header.Get("X-Late"))
	assert.Equal(t, "one", req.Header.Get("X-Test"))
}

// TestClient_ForwardsTheQueryString is the end-to-end half of the rewrite fix:
// r.URL.Path never carries the query, so the upstream would otherwise be asked
// a different question than the client asked.
func TestClient_ForwardsTheQueryString(t *testing.T) {
	services, received := upstreamCapturing(t, http.StatusOK, `ok`)

	client := newTestClient(newMockCache[*Call](), services)

	req, err := http.NewRequest("GET", "http://gateway/api/search?q=goteway&page=2", nil)
	require.NoError(t, err)

	_, err = client.Request(context.Background(), req)
	require.NoError(t, err)

	got := received()
	require.NotNil(t, got)
	assert.Equal(t, "/search", got.URL.Path)
	assert.Equal(t, "q=goteway&page=2", got.URL.RawQuery)
}

// TestClient_CancelledCallerStopsTheUpstream covers the request context reaching
// the upstream call: a caller that hangs up must not leave the gateway waiting
// on a service that nobody is waiting for.
func TestClient_CancelledCallerStopsTheUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()

	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: upstream.URL[len("http://"):]}},
	}

	client := newTestClient(newMockCache[*Call](), services)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	call, err := client.Request(ctx, req)

	// ErrServiceUnavailable is the single sentinel a caller classifies on, so the
	// cause travels in the message rather than as a second wrapped error.
	assert.ErrorIs(t, err, ErrServiceUnavailable)
	assert.ErrorContains(t, err, "context canceled")
	assert.Nil(t, call)
}

// TestClient_CachesAResponseTheCallerAlreadyHungUpOn is the pairing with the test
// above: the upstream call follows the client, but the cache write must not, or
// a disconnect halfway through a response throws away a response that was
// fetched whole.
func TestClient_CachesAResponseTheCallerAlreadyHungUpOn(t *testing.T) {
	services, _ := upstreamCapturing(t, http.StatusOK, `{"cached": true}`)

	mc := newMockCache[*Call]()
	client := newTestClient(mc, services)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://gateway/api/test", nil)
	require.NoError(t, err)

	call, err := client.Request(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, call)

	// The caller goes away after the upstream answered and the snapshot was
	// taken, which is exactly when the cache write is still pending.
	cancel()

	require.Eventually(t, func() bool { return mc.has(call.Key()) },
		time.Second, 5*time.Millisecond, "the response was dropped instead of cached")
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
