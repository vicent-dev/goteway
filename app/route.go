package app

import (
	"io"
	"net"
	"net/http"

	"goteway/pkg/auth"
	"goteway/pkg/cache"
	"goteway/pkg/request"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(rateLimiterMiddleware)

	// auth handler
	authR := s.r.PathPrefix("/auth").Subrouter()
	authR.Use(jsonMiddleware)
	authR.PathPrefix("/register").HandlerFunc(s.registerHandler()).Methods("POST")
	authR.PathPrefix("/login").HandlerFunc(s.loginHandler()).Methods("POST")
	authR.PathPrefix("/logout").HandlerFunc(s.logoutHandler()).Methods("POST")
	authR.PathPrefix("/refresh").HandlerFunc(s.refreshHandler()).Methods("POST")

	// Everything else is proxied, and requires a verified access token:
	// whether a route additionally demands one is decided per service by the
	// request package, from the principal in the context.
	s.r.PathPrefix("/").Handler(s.authMiddleware(s.defaultRouteHandler()))
}

func (s *server) defaultRouteHandler() http.Handler {

	responseCache := cache.NewRedis[*request.Call](s.rdb)
	client := request.NewClient(&responseCache, s.c.convertServicesToRequest())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
}

// requestMeta describes the caller for the session records the auth domain
// stores, so a session can be traced back to the client that created it.
func requestMeta(r *http.Request) auth.RequestMeta {
	return auth.RequestMeta{
		UserAgent: r.UserAgent(),
		IP:        remoteIP(r),
	}
}

// remoteIP is the client address without the port, which is what the persisted
// varchar(45) column can hold.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
