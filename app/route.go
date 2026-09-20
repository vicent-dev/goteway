package app

import (
	"goteway/pkg/cache"
	"goteway/pkg/request"
	"net/http"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(jsonMiddleware)

	cache := cache.NewRedis(s.rdb)
	client := request.NewClient(&cache)

	//ping example
	s.r.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)
		response["ping"] = "pong pong"

		client.Request(w, r)
		s.writeResponse(w, response)

	}).Methods("GET")
}
