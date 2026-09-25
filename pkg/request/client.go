package request

import (
	"bytes"
	"context"
	"goteway/pkg/cache"
	"goteway/pkg/log"
	"io"
	"net/http"
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

func (c *Client) Request(ctx context.Context, httpW http.ResponseWriter, httpR *http.Request) {

	call, err := NewCall(httpR, c.servicesConfig)

	if err != nil {
		log.LogError(ctx, err.Error())
		return
	}

	// get response from cache
	(*c.cache).Get(call)
	if call.response != nil {
		log.LogInfo(ctx, "Serve response from cache")
		mapResponseIntoResponseWriter(call.response, httpW)
		return
	}

	// http request if not found and async cache
	internalRequest, err := http.NewRequest(
		call.request.Method,
		call.requestUrl,
		call.request.Body,
	)

	if err != nil {
		log.LogError(ctx, err.Error())
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}

	call.response, err = client.Do(internalRequest)

	if err != nil {
		log.LogError(ctx, err.Error())
		return
	}

	mapResponseIntoResponseWriter(call.response, httpW)

	go func(call *Call) {
		(*c.cache).Set(call)
	}(call)
}

func mapResponseIntoResponseWriter(r *http.Response, rw http.ResponseWriter) {
	body, _ := io.ReadAll(r.Body)

	defer r.Body.Close()

	for hn, hvs := range r.Header {
		rw.Header().Del(hn)
		for _, hv := range hvs {
			rw.Header().Add(hn, hv)
		}
	}

	rw.Write(body)

	r.Body = io.NopCloser(bytes.NewBuffer(body))
}
