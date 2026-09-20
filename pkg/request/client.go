package request

import (
	"goteway/pkg/cache"
	"net/http"
)

type Client struct {
	cache *cache.Cache[*Request]
}

func NewClient(c *cache.Cache[*Request]) *Client {
	return &Client{c}
}

func (c *Client) Request(httpW http.ResponseWriter, httpR *http.Request) {
	request := NewRequest(httpR)

	// get response from cache
	if (*c.cache).Get(request) != nil {
		// set response
		return
	}

	// http request if not found and async cache

	go func(req *Request) {
		(*c.cache).Set(req)
	}(request)
}
