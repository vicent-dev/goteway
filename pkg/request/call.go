package request

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type serializedResponse struct {
	Status     string         `json:"status"`
	StatusCode int            `json:"status_code"`
	Body       string         `json:"body"`
	Header     http.Header    `json:"header"`
	Cookies    []*http.Cookie `json:"cookies"`
}

// Call is one cacheable upstream exchange: the fingerprint that identifies it,
// the upstream response it produced, and the bytes that response carried.
type Call struct {
	id         string // key generated from gateway request
	isInternal bool
	requestUrl string
	request    *http.Request
	// body is the upstream body, captured once when the response is attached.
	// It is the source of truth for the cache value and the source the handler
	// reads, which is why it exists: the cache is written from a goroutine while
	// the handler is still copying the response to the client, and sharing a
	// single reader between them is a race.
	body []byte
	// Response is nil until the upstream answers, which is also how a cache hit
	// is detected.
	Response *http.Response
}

// NewCall matches the request against the configured services and derives its
// cache key. It reads the caller's body and hands it back, so the upstream
// request can still be built from the same request afterwards.
func NewCall(r *http.Request, servicesConfig ServicesConfig) (*Call, error) {
	sc, isInternal := findServiceConfigForUri(r.URL.Path, servicesConfig)

	if sc == nil {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, r.URL.Path)
	}

	requestUrl := strings.Replace(r.URL.Path[1:], sc.Path, sc.Host, -1)

	// r.URL.Path never carries the query, so it has to be appended by hand or
	// the upstream is asked a different question than the client asked. The
	// fingerprint below hashes the whole *url.URL, query included, so before
	// this the gateway kept one cache entry per query string for requests the
	// upstream could not tell apart.
	if r.URL.RawQuery != "" {
		requestUrl += "?" + r.URL.RawQuery
	}

	// generate key
	var buf bytes.Buffer
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	defer encoder.Close()
	if r.Body != nil {
		defer r.Body.Close()
	}

	header := r.Header
	// @todo check other time based headers
	header.Del("Date")

	var body []byte
	if r.Body != nil {
		var err error
		// A truncated body would fingerprint a request that never arrived, and
		// every later request sharing that fingerprint would be served the
		// answer to a request that was never made.
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRequestBodyRead, err)
		}
	}

	encodeKey := struct {
		Url     any
		Header  any
		Body    any
		Method  any
		Cookies any
	}{
		r.URL,
		header,
		body,
		r.Method,
		r.Cookies(),
	}

	err := json.NewEncoder(encoder).Encode(fmt.Sprintf("%v", encodeKey))

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCacheKeyEncoding, err)
	}

	if r.Body != nil {
		r.Body = io.NopCloser(bytes.NewBuffer(body))
	}

	return &Call{
		id:         buf.String(),
		isInternal: isInternal,
		requestUrl: requestUrl,
		request:    r,
		Response:   nil,
	}, nil
}

func findServiceConfigForUri(uri string, servicesConfig ServicesConfig) (*ServiceConfig, bool) {

	var sc *ServiceConfig = nil
	isInternal := false

	for _, s := range servicesConfig.External {

		if matched, _ := regexp.MatchString(s.Path+"*", uri); matched {
			sc = &ServiceConfig{Path: s.Path, Host: s.Host}
			break
		}
	}

	for _, s := range servicesConfig.Internal {

		if matched, _ := regexp.MatchString(s.Path+"*", uri); matched {

			if sc == nil || strings.Count(s.Path, "/") > strings.Count(sc.Path, "/") {
				isInternal = true
				sc = &ServiceConfig{Path: s.Path, Host: s.Host}
				// Continue scanning to find deeper matches
				continue
			}

		}
	}

	return sc, isInternal
}

func (c *Call) Key() string {
	return c.id
}

// validStatusCode is the range ResponseWriter.WriteHeader accepts. It is checked
// against a decoded payload rather than trusted, so that the handler can write
// the cached status without having to defend itself.
func validStatusCode(code int) bool {
	return code >= 100 && code <= 999
}

// Value serializes the upstream response for the cache. It reads the snapshot
// taken when the response was attached, never the response body itself: that
// body belongs to whoever is still streaming it to the client.
func (c *Call) Value() (string, error) {
	if c.Response == nil {
		return "", ErrNoResponse
	}

	serialized := serializedResponse{
		c.Response.Status,
		c.Response.StatusCode,
		string(c.body),
		c.Response.Header,
		c.Response.Cookies(),
	}

	v, err := json.Marshal(serialized)

	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrResponseSerialization, err)
	}

	return string(v), nil
}

// SetValue restores a cached response. A payload it cannot decode is reported
// instead of being ignored, so a corrupted entry is an observable failure
// rather than a silently empty response.
func (c *Call) SetValue(s string) error {
	serialized := &serializedResponse{}
	err := json.Unmarshal([]byte(s), serialized)

	if err != nil {
		return fmt.Errorf("%w: %v", ErrResponseDeserialization, err)
	}

	// The status is written straight to the ResponseWriter by the handler, which
	// panics on a code outside this range. A payload that decodes but carries an
	// impossible status is corrupt in exactly the same way an undecodable one
	// is, so it is rejected here instead of becoming a panic per request until
	// the entry expires.
	if !validStatusCode(serialized.StatusCode) {
		return fmt.Errorf("%w: status_code %d is not a status code",
			ErrResponseDeserialization, serialized.StatusCode)
	}

	c.body = []byte(serialized.Body)
	c.Response = &http.Response{
		Status:     serialized.Status,
		StatusCode: serialized.StatusCode,
		Body:       io.NopCloser(bytes.NewReader(c.body)),
		Header:     serialized.Header,
	}

	return nil
}

// attachResponse takes ownership of an upstream response: the body is read once
// and kept as the snapshot both the cache value and the handler are built from.
func (c *Call) attachResponse(resp *http.Response) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrResponseRead, err)
	}

	c.Response = resp
	c.body = body
	// The handler gets its own reader over the same bytes: reading resp.Body
	// directly would be a second reader over a body that has already been
	// drained and closed.
	c.Response.Body = io.NopCloser(bytes.NewReader(body))

	return nil
}
