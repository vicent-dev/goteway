package request

import (
	"context"
	"errors"
	"goteway/pkg/auth"
	"goteway/pkg/cache"
	"goteway/pkg/log"
	"net/http"
	"strings"
	"time"
)

type ServicesConfig struct {
	Internal []ServiceConfig
	External []ServiceConfig
}

type ServiceConfig struct {
	Path string
	Host string
}

type Client struct {
	cache          *cache.Cache[*Call]
	servicesConfig ServicesConfig
}

func NewClient(c *cache.Cache[*Call], services ServicesConfig) *Client {
	return &Client{c, services}
}

func (c *Client) Request(ctx context.Context, httpW http.ResponseWriter, httpR *http.Request) (*Call, int, error) {

	call, err := NewCall(httpR, c.servicesConfig)

	if err != nil {
		log.LogError(ctx, err.Error())
		return nil, http.StatusBadRequest, err
	}

	if call.isInternal && !auth.IsValidToken(ctx) {
		return nil, http.StatusUnauthorized, errors.New("access denied")
	}

	// get response from cache
	(*c.cache).Get(call)
	if call.Response != nil {
		log.LogInfo(ctx, "Serve response from cache")
		return call, call.Response.StatusCode, nil
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
		return nil, http.StatusBadRequest, errors.New("service not available")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	call.Response, err = client.Do(internalRequest)

	if err != nil {
		log.LogError(ctx, err.Error())
		return call, http.StatusBadRequest, errors.New("service not available")
	}

	go func(call *Call) {
		(*c.cache).Set(call)
	}(call)

	return call, call.Response.StatusCode, nil
}
