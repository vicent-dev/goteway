package app

import (
	"goteway/pkg/cache"
	"goteway/pkg/request"
	"io"
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

		call, statusCode, err := client.Request(ctx, w, r)

		if err != nil {
			writeErrorResponse(w, map[string]any{"error": err.Error()}, statusCode)
			return
		}

		body, _ := io.ReadAll(call.Response.Body)
		defer call.Response.Body.Close()

		for hn, hvs := range call.Response.Header {
			w.Header().Del(hn)
			for _, hv := range hvs {
				w.Header().Add(hn, hv)
			}
		}

		w.Write(body)

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
