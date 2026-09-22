package app

import (
	"context"
	"goteway/pkg/cache"
	"goteway/pkg/request"
	"net/http"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)

	// auth handler
	authR := s.r.PathPrefix("/auth").Subrouter()
	authR.PathPrefix("/login").HandlerFunc(s.loginHandler()).Methods("POST")
	authR.PathPrefix("/logout").HandlerFunc(s.logoutHandler()).Methods("POST")

	// default router handler
	s.r.PathPrefix("/").HandlerFunc(s.defaultRouteHandler())
}

func (s *server) defaultRouteHandler() func(http.ResponseWriter, *http.Request) {

	cache := cache.NewRedis[*request.Request](s.rdb)
	client := request.NewClient(&cache, s.c.convertServicesToRequest())

	return func(w http.ResponseWriter, r *http.Request) {
		client.Request(context.Background(), w, r)
	}
}

func (s *server) loginHandler() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)
		response[""] = "login"

		s.writeResponse(w, response)
	}
}

func (s *server) logoutHandler() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)
		response[""] = "logout"

		s.writeResponse(w, response)
	}
}
