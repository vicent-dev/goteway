package request

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindServiceConfigForUri(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api/external", Host: "external.example.com"},
			{Path: "public", Host: "public.example.com"},
		},
		Internal: []ServiceConfig{
			{Path: "api/internal", Host: "internal.example.com"},
			{Path: "api/internal/admin", Host: "admin.example.com"},
		},
	}

	tests := []struct {
		name           string
		uri            string
		wantService    *ServiceConfig
		wantIsInternal bool
		description    string
	}{
		{
			name:           "external exact match",
			uri:            "api/external/test",
			wantService:    &ServiceConfig{Path: "api/external", Host: "external.example.com"},
			wantIsInternal: false,
		},
		{
			name:           "external root level",
			uri:            "public/resource",
			wantService:    &ServiceConfig{Path: "public", Host: "public.example.com"},
			wantIsInternal: false,
		},
		{
			name:           "internal match",
			uri:            "api/internal/users",
			wantService:    &ServiceConfig{Path: "api/internal", Host: "internal.example.com"},
			wantIsInternal: true,
		},
		{
			name:           "internal deeper path wins over shorter",
			uri:            "api/internal/admin/users",
			wantService:    &ServiceConfig{Path: "api/internal/admin", Host: "admin.example.com"},
			wantIsInternal: true,
		},
		{
			name:           "no match",
			uri:            "unknown/path",
			wantService:    nil,
			wantIsInternal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, isInternal := findServiceConfigForUri(tt.uri, services)
			if tt.wantService == nil {
				assert.Nil(t, sc)
			} else {
				assert.NotNil(t, sc)
				assert.Equal(t, tt.wantService.Path, sc.Path)
				assert.Equal(t, tt.wantService.Host, sc.Host)
			}
			assert.Equal(t, tt.wantIsInternal, isInternal)
		})
	}
}

func TestNewCall_CacheKeyDeterministic(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: "backend.example.com"},
		},
	}

	body := []byte(`{"test": "data"}`)
	req, err := http.NewRequest("POST", "http://gateway/api/users", bytes.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom", "value")
	req.Header.Set("Date", "Wed, 01 Jan 2020 00:00:00 GMT") // should be deleted

	call1, err := NewCall(req, services)
	assert.NoError(t, err)
	assert.NotNil(t, call1)

	// Create identical request
	body2 := []byte(`{"test": "data"}`)
	req2, err := http.NewRequest("POST", "http://gateway/api/users", bytes.NewReader(body2))
	assert.NoError(t, err)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Custom", "value")
	req2.Header.Set("Date", "Wed, 01 Jan 2020 00:00:00 GMT")

	call2, err := NewCall(req2, services)
	assert.NoError(t, err)

	assert.Equal(t, call1.Key(), call2.Key())
}

func TestNewCall_PathRewrite(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: "backend.example.com/v1"},
		},
	}

	req, err := http.NewRequest("GET", "http://gateway/api/users/123", nil)
	assert.NoError(t, err)

	call, err := NewCall(req, services)
	assert.NoError(t, err)
	assert.Equal(t, "backend.example.com/v1/users/123", call.requestUrl)
}

// TestNewCall_PathRewriteKeepsTheQuery covers the query string, which
// r.URL.Path never carries: without it appended, the upstream is asked a
// different question than the client asked.
func TestNewCall_PathRewriteKeepsTheQuery(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{
			{Path: "api", Host: "backend.example.com/v1"},
		},
	}

	req, err := http.NewRequest("GET", "http://gateway/api/users?page=2&sort=name", nil)
	assert.NoError(t, err)

	call, err := NewCall(req, services)
	assert.NoError(t, err)
	assert.Equal(t, "backend.example.com/v1/users?page=2&sort=name", call.requestUrl)
}

// TestNewCall_PathRewriteWithoutAQuery pins the empty case: appending
// unconditionally would leave a bare "?" on every request that has no query.
func TestNewCall_PathRewriteWithoutAQuery(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: "backend.example.com"}},
	}

	req, err := http.NewRequest("GET", "http://gateway/api/users", nil)
	assert.NoError(t, err)

	call, err := NewCall(req, services)
	assert.NoError(t, err)
	assert.Equal(t, "backend.example.com/users", call.requestUrl)
	assert.NotContains(t, call.requestUrl, "?")
}

// TestNewCall_QueryDoesNotChangeTheMatch pins the other half: the query travels
// with the rewritten URL, never with the prefix that decides which service
// answers.
func TestNewCall_QueryDoesNotChangeTheMatch(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: "backend.example.com"}},
	}

	withQuery, err := http.NewRequest("GET", "http://gateway/api/users?page=2", nil)
	require.NoError(t, err)
	withoutQuery, err := http.NewRequest("GET", "http://gateway/api/users", nil)
	require.NoError(t, err)

	first, err := NewCall(withQuery, services)
	require.NoError(t, err)
	second, err := NewCall(withoutQuery, services)
	require.NoError(t, err)

	assert.Equal(t, "backend.example.com", first.requestUrl[:len("backend.example.com")])
	assert.Equal(t, second.requestUrl, first.requestUrl[:len(second.requestUrl)],
		"both requests hit the same upstream path")
	assert.NotEqual(t, first.Key(), second.Key(),
		"different queries are different requests, and cache separately")
}

func TestNewCall_ServiceNotFound(t *testing.T) {
	services := ServicesConfig{}
	req, err := http.NewRequest("GET", "http://gateway/unknown", nil)
	assert.NoError(t, err)

	call, err := NewCall(req, services)

	// The path travels with the error, so a log line says what matched nothing.
	assert.ErrorIs(t, err, ErrServiceNotFound)
	assert.ErrorContains(t, err, "/unknown")
	assert.Nil(t, call)
}

func TestNewCall_BodyIsHandedBackToTheCaller(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: "backend.example.com"}},
	}

	body := []byte(`{"test": "data"}`)
	req, err := http.NewRequest("POST", "http://gateway/api/users", bytes.NewReader(body))
	require.NoError(t, err)

	_, err = NewCall(req, services)
	require.NoError(t, err)

	// Fingerprinting reads the body, so the upstream request is only possible
	// because NewCall puts it back.
	read, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, body, read)
}

func TestNewCall_UnreadableBodyIsReported(t *testing.T) {
	services := ServicesConfig{
		External: []ServiceConfig{{Path: "api", Host: "backend.example.com"}},
	}

	req, err := http.NewRequest("POST", "http://gateway/api/users", strings.NewReader("data"))
	require.NoError(t, err)
	req.Body = io.NopCloser(errReader{})

	// Fingerprinting a truncated body would key the cache on a request that
	// never arrived, so the read failure has to be visible.
	_, err = NewCall(req, services)
	assert.ErrorIs(t, err, ErrRequestBodyRead)
}

// errReader is a body that fails halfway through, which a plain
// io.NopCloser cannot express.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCall_AttachResponseSnapshot(t *testing.T) {
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"result": "ok"}`)),
	}

	call := &Call{}
	require.NoError(t, call.attachResponse(resp))

	value, err := call.Value()
	require.NoError(t, err)

	var serialized serializedResponse
	require.NoError(t, json.Unmarshal([]byte(value), &serialized))
	assert.Equal(t, "200 OK", serialized.Status)
	assert.Equal(t, 200, serialized.StatusCode)
	assert.Equal(t, `{"result": "ok"}`, serialized.Body)
}

func TestCall_LeavesTheHandlerItsOwnBody(t *testing.T) {
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"result": "ok"}`)),
	}

	call := &Call{}
	require.NoError(t, call.attachResponse(resp))

	// The cache is serialized while the handler is still streaming the response
	// to the client, so serializing must read the snapshot and leave the
	// handler's reader alone.
	_, err := call.Value()
	require.NoError(t, err)

	body, err := io.ReadAll(call.Response.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"result": "ok"}`, string(body))
}

func TestCall_AttachResponseReportsAnUnreadableBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(errReader{}),
	}

	call := &Call{}
	assert.ErrorIs(t, call.attachResponse(resp), ErrResponseRead)
}

func TestCall_ValueWithoutAResponse(t *testing.T) {
	value, err := (&Call{}).Value()

	assert.ErrorIs(t, err, ErrNoResponse)
	assert.Empty(t, value)
}

func TestCall_SetValue(t *testing.T) {
	call := &Call{}
	require.NoError(t, call.SetValue(
		`{"status":"200 OK","status_code":200,"body":"{\"result\": \"ok\"}","header":{"Content-Type":["application/json"]}}`))

	require.NotNil(t, call.Response)
	assert.Equal(t, "200 OK", call.Response.Status)
	assert.Equal(t, 200, call.Response.StatusCode)
	assert.Equal(t, "application/json", call.Response.Header.Get("Content-Type"))

	body, err := io.ReadAll(call.Response.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"result": "ok"}`, string(body))
}

func TestCall_SetValueRejectsACorruptEntry(t *testing.T) {
	call := &Call{}

	// A payload that cannot be decoded is reported rather than ignored, so a
	// corrupted entry cannot be mistaken for an empty response.
	assert.ErrorIs(t, call.SetValue("not json"), ErrResponseDeserialization)
	assert.Nil(t, call.Response)
}

// TestCall_SetValueRejectsAnImpossibleStatus is the codec half of trusting a
// cached status: the handler hands it to ResponseWriter.WriteHeader, which
// panics outside 100-999, so a payload that decodes but carries an impossible
// code is corrupt in the same way an undecodable one is.
func TestCall_SetValueRejectsAnImpossibleStatus(t *testing.T) {
	for _, payload := range []string{
		`{"status":"","status_code":0}`,
		`{"status":"","status_code":99}`,
		`{"status":"","status_code":1000}`,
		`{"status":"","status_code":-1}`,
	} {
		call := &Call{}
		assert.ErrorIs(t, call.SetValue(payload), ErrResponseDeserialization, payload)
		assert.Nil(t, call.Response, payload)
	}
}

func TestCall_ValueSetValueRoundTrip(t *testing.T) {
	resp := &http.Response{
		Status:     "201 Created",
		StatusCode: 201,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"result": "ok"}`)),
	}

	call := &Call{}
	require.NoError(t, call.attachResponse(resp))

	value, err := call.Value()
	require.NoError(t, err)
	assert.NotEmpty(t, value)

	restored := &Call{}
	require.NoError(t, restored.SetValue(value))

	again, err := restored.Value()
	require.NoError(t, err)
	assert.Equal(t, value, again)
}

func TestNewCall_InternalFlag(t *testing.T) {
	services := ServicesConfig{
		Internal: []ServiceConfig{
			{Path: "admin", Host: "admin.internal:8080"},
		},
	}

	req, err := http.NewRequest("GET", "http://gateway/admin/panel", nil)
	assert.NoError(t, err)

	call, err := NewCall(req, services)
	assert.NoError(t, err)
	assert.True(t, call.isInternal)
}

// TestCall_ValueAndHandlerReadConcurrently is the regression test for the
// snapshot: run with -race, serializing for the cache and copying the body to
// the client happen at once and must both see the whole payload.
func TestCall_ValueAndHandlerReadConcurrently(t *testing.T) {
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"result": "ok"}`)),
	}

	call := &Call{}
	require.NoError(t, call.attachResponse(resp))

	cached := make(chan string, 1)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		value, err := call.Value()
		assert.NoError(t, err)
		cached <- value
	}()
	go func() {
		defer wg.Done()
		body, err := io.ReadAll(call.Response.Body)
		assert.NoError(t, err)
		assert.Equal(t, `{"result": "ok"}`, string(body))
	}()

	wg.Wait()
	close(cached)

	var serialized serializedResponse
	require.NoError(t, json.Unmarshal([]byte(<-cached), &serialized))
	assert.Equal(t, `{"result": "ok"}`, serialized.Body)
}
