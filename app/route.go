package app

import (
	"goteway/pkg/cache"
	"goteway/pkg/request"
	"net/http"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(jsonMiddleware)

	cache := cache.NewRedis[*request.Request](s.rdb)
	client := request.NewClient(&cache)

	// auth handler
	authR := s.r.PathPrefix("/auth").Subrouter()

	authR.PathPrefix("/login").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)
		response[""] = "login"

		s.writeResponse(w, response)
	}).Methods("POST")

	authR.PathPrefix("/logout").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)
		response[""] = "logout"

		s.writeResponse(w, response)
	}).Methods("POST")

	// default router handler
	s.r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)

		client.Request(w, r)

		s.writeResponse(w, response)
	})
}
