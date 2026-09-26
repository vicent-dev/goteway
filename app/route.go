package app

import (
	"goteway/pkg/cache"
	"goteway/pkg/request"
	"net/http"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(rateLimiterMiddleware)

	// auth handler
	authR := s.r.PathPrefix("/auth").Subrouter()
	authR.Use(jsonMiddleware)
	authR.PathPrefix("/login").HandlerFunc(s.loginHandler()).Methods("POST")
	authR.PathPrefix("/logout").HandlerFunc(s.logoutHandler()).Methods("POST")

	// default router handler
	s.r.PathPrefix("/").HandlerFunc(s.defaultRouteHandler())
}

func (s *server) defaultRouteHandler() func(http.ResponseWriter, *http.Request) {

	cache := cache.NewRedis[*request.Call](s.rdb)
	client := request.NewClient(&cache, s.c.convertServicesToRequest())

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		client.Request(ctx, w, r)

		ctx.Done()
	}
}

func (s *server) loginHandler() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
	}
}

func (s *server) logoutHandler() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
	}
}
