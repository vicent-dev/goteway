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

// hopByHopHeaders are scoped to one connection rather than to the message, so
// forwarding them across a proxy would describe a connection the far side does
// not have. RFC 7230 §6.1 requires both directions of an exchange to drop them.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// IsHopByHop reports whether a header belongs to the connection rather than to
// the message. It is exported because the response side of the exchange is
// copied by the adapter, which needs the same rule and must not keep a second
// copy of the list.
func IsHopByHop(name string) bool {
	for _, h := range hopByHopHeaders {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// upstreamHeaders builds the header set the upstream request is sent with.
func upstreamHeaders(caller http.Header) http.Header {
	header := caller.Clone()
	for name := range header {
		if IsHopByHop(name) {
			header.Del(name)
		}
	}

	// The bearer authenticates the caller against the gateway. Handing the same
	// token to every upstream would spread a credential that is valid here, and
	// a compromised upstream would be able to replay it. Backends are expected
	// to sit behind the gateway, not to re-verify its tokens.
	header.Del(auth.AuthorizationHeader)

	return header
}

type Client struct {
	cache          *cache.Cache[*Call]
	servicesConfig ServicesConfig

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
	internalRequest, err := http.NewRequestWithContext(
		ctx,
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

	internalRequest.Header = upstreamHeaders(call.request.Header)

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

	go func(ctx context.Context, call *Call) {
		if err := (*c.cache).Set(ctx, call); err != nil {
			log.LogWarn(ctx, "cache unwritable: "+err.Error())
		}
	}(context.WithoutCancel(ctx), call)

	return call, nil
}
