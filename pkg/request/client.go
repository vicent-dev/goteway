package request

import (
	"context"
	"fmt"
	"goteway/pkg/cache"
	"io"
	"net/http"
	"time"

	"github.com/en-vee/alog"
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
	cache          *cache.Cache[*Request]
	servicesConfig ServicesConfig
}

func NewClient(c *cache.Cache[*Request], services ServicesConfig) *Client {
	return &Client{c, services}
}

func (c *Client) Request(ctx context.Context, httpW http.ResponseWriter, httpR *http.Request) {

	request, err := NewRequest(httpR, c.servicesConfig)

	if err != nil {
		alog.Error(err.Error())
		return
	}

	// get response from cache
	cacheResponse := (*c.cache).Get(request)
	if cacheResponse != nil {
		// @todo build internal response
		return
	}

	// http request if not found and async cache
	internalRequest, err := http.NewRequest(
		request.Method,
		request.ConvertedRequest,
		request.Body,
	)

	if err != nil {
		alog.Error(err.Error())
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}

	alog.Info(fmt.Sprintf("Internal request: %v", internalRequest))
	internalResponse, err := client.Do(internalRequest)

	if err != nil {
		alog.Error(err.Error())
		return
	}

	defer internalResponse.Body.Close()

	internalBody, _ := io.ReadAll(internalResponse.Body)

	// @todo refactor to have same logic from cache and from http client
	// set same body, headers as internal request
	httpW.WriteHeader(internalResponse.StatusCode)
	httpW.Write(internalBody)

	request.HttpResponse = internalBody

	alog.Info("Internal body response: " + string(internalBody))
	for ih, v := range internalResponse.Header {
		httpW.Header().Set(ih, v[0])
	}

	go func(req *Request) {
		(*c.cache).Set(req)
	}(request)
}
