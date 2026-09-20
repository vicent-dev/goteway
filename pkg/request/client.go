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
	_ = NewRequest(httpR)

	// get response from cache

	// return if found

	// http request if not found and async cache

}
