package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"goteway/pkg/auth"
	"goteway/pkg/cache"
	"goteway/pkg/log"
)

type ServicesConfig struct {
	Internal []ServiceConfig
	External []ServiceConfig
}

type ServiceConfig struct {
	Path string
	Host string
}

// upstreamTimeout bounds a single upstream exchange. A cached response skips it
// entirely, which is half of what the cache is for.
const upstreamTimeout = 10 * time.Second

type Client struct {
	cache          *cache.Cache[*Call]
	servicesConfig ServicesConfig
	// httpClient reaches the upstreams. It is a field so that the timeout, and
	// the way it fails, are the caller's to choose, and so tests can drive a
	// failing upstream without a real network.
	httpClient *http.Client
}

func NewClient(c *cache.Cache[*Call], services ServicesConfig) *Client {
	return &Client{
		cache:          c,
		servicesConfig: services,
		httpClient:     &http.Client{Timeout: upstreamTimeout},
	}
}

// Request forwards the call to the upstream its path resolves to, serving the
// cached response instead when there is one.
//
// It reports what went wrong as one of the package sentinels and never as a
// status code: deciding how a failure looks over the wire belongs to the
// caller, which is the split the auth domain uses. The call is nil whenever an
// error is returned.
func (c *Client) Request(ctx context.Context, r *http.Request) (*Call, error) {

	call, err := NewCall(r, c.servicesConfig)
	if err != nil {
		log.LogError(ctx, err.Error())
		return nil, err
	}

	if call.isInternal {
		if _, ok := auth.PrincipalFromContext(ctx); !ok {
			return nil, ErrAccessDenied
		}
	}

	// get response from cache. A cache that cannot answer is not a failed
	// request: the upstream is asked instead, and the reason is logged so an
	// outage stays visible instead of showing up as a permanent cache miss.
	err = (*c.cache).Get(ctx, call)
	switch {
	case err == nil:
		log.LogInfo(ctx, "Serve response from cache")
		return call, nil
	case errors.Is(err, cache.ErrNotFound):
		log.LogInfo(ctx, "cache miss")
	default:
		log.LogWarn(ctx, "cache unreadable, going upstream: "+err.Error())
	}

	// http request if not found and async cache
	requestUrl := call.requestUrl
	if !strings.HasPrefix(requestUrl, "http://") && !strings.HasPrefix(requestUrl, "https://") {
		requestUrl = "http://" + requestUrl
	}
	internalRequest, err := http.NewRequest(
		call.request.Method,
		requestUrl,
		call.request.Body,
	)

	if err != nil {
		log.LogError(ctx, err.Error())
		// The host comes from the configuration, so this is a fault of ours and
		// is reported as one rather than as a bad request.
		return nil, fmt.Errorf("%w: %v", ErrInvalidUpstreamURL, err)
	}

	response, err := c.httpClient.Do(internalRequest)
	if err != nil {
		log.LogError(ctx, err.Error())
		// The cause is kept for the log and deliberately not for the client: it
		// carries the upstream host and the dial error.
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	if err := call.attachResponse(response); err != nil {
		log.LogError(ctx, err.Error())
		return nil, err
	}

	// The response is already on its way to the client, so caching it cannot
	// fail the request. The goroutine keeps the context only to log with the
	// method and path the call came in on.
	go func(ctx context.Context, call *Call) {
		if err := (*c.cache).Set(ctx, call); err != nil {
			log.LogWarn(ctx, "cache unwritable: "+err.Error())
		}
	}(ctx, call)

	return call, nil
}
