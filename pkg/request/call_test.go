package request

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestNewCall_ServiceNotFound(t *testing.T) {
	services := ServicesConfig{}
	req, err := http.NewRequest("GET", "http://gateway/unknown", nil)
	assert.NoError(t, err)

	call, err := NewCall(req, services)
	assert.Error(t, err)
	assert.Nil(t, call)
}

func TestCall_ValueSetValue(t *testing.T) {
	// Create a response
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"result": "ok"}`)),
	}

	call := &Call{Response: resp}
	value := call.Value()
	assert.NotEmpty(t, value)

	// Decode to verify structure
	var serialized serializedResponse
	err := json.Unmarshal([]byte(value), &serialized)
	assert.NoError(t, err)
	assert.Equal(t, "200 OK", serialized.Status)
	assert.Equal(t, 200, serialized.StatusCode)
	assert.Equal(t, `{"result": "ok"}`, serialized.Body)

	// SetValue back
	call2 := &Call{}
	call2.SetValue(value)
	assert.NotNil(t, call2.Response)
	assert.Equal(t, 200, call2.Response.StatusCode)
	assert.Equal(t, "200 OK", call2.Response.Status)
	body, err := io.ReadAll(call2.Response.Body)
	assert.NoError(t, err)
	assert.Equal(t, `{"result": "ok"}`, string(body))
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
