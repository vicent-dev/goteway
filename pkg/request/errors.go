package request

import "errors"

// Sentinel errors returned by Client and Call. Callers map them to their own
// transport semantics with errors.Is; nothing here mentions HTTP.
var (
	// ErrServiceNotFound is returned when no configured service matches the
	// requested path, and wraps the path that matched nothing.
	ErrServiceNotFound = errors.New("request: no service configured for the path")

	// ErrAccessDenied is returned when an internal service is reached without a
	// caller in the context to authorise it.
	ErrAccessDenied = errors.New("request: access denied")

	// ErrServiceUnavailable covers every reason the upstream could not be
	// reached, dial failure included, and wraps the underlying cause. The
	// caller decides whether that cause is safe to show.
	ErrServiceUnavailable = errors.New("request: service not available")

	// ErrInvalidUpstreamURL comes from building the upstream request out of a
	// configured host, so it means the configuration is wrong rather than the
	// caller.
	ErrInvalidUpstreamURL = errors.New("request: configured host is not a usable url")

	// ErrRequestBodyRead comes from reading the caller's body and ErrResponseRead
	// from reading the upstream one. Both are internal: the bytes never arrived,
	// so nothing derived from them can be trusted.
	ErrRequestBodyRead = errors.New("request: cannot read the request body")
	ErrResponseRead    = errors.New("request: cannot read the upstream response")

	// ErrCacheKeyEncoding comes from fingerprinting the incoming request, whose
	// result is the cache key, so an unencodable one means a cache entry the
	// gateway cannot address.
	ErrCacheKeyEncoding = errors.New("request: cannot encode the cache key")

	// ErrResponseSerialization and ErrResponseDeserialization are the two
	// directions of the cache codec, so a cache that cannot be written and one
	// that cannot be read are told apart.
	ErrResponseSerialization   = errors.New("request: cannot serialize the response")
	ErrResponseDeserialization = errors.New("request: cannot deserialize the response")

	// ErrNoResponse is returned when a call is asked to serialize a response it
	// never received, which is a programming error rather than a runtime one.
	ErrNoResponse = errors.New("request: call has no response")
)
