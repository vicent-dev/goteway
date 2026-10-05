package request

import "errors"

var (
	ErrServiceNotFound = errors.New("request: no service configured for the path")

	ErrAccessDenied = errors.New("request: access denied")

	ErrServiceUnavailable = errors.New("request: service not available")

	ErrInvalidUpstreamURL = errors.New("request: configured host is not a usable url")

	ErrRequestBodyRead = errors.New("request: cannot read the request body")
	ErrResponseRead    = errors.New("request: cannot read the upstream response")

	ErrCacheKeyEncoding = errors.New("request: cannot encode the cache key")

	ErrResponseSerialization   = errors.New("request: cannot serialize the response")
	ErrResponseDeserialization = errors.New("request: cannot deserialize the response")

	ErrNoResponse = errors.New("request: call has no response")
)
