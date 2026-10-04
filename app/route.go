package app

import (
	"errors"
	"io"
	"net"
	"net/http"

	"goteway/pkg/auth"
	"goteway/pkg/cache"
	"goteway/pkg/log"
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

		call, err := client.Request(ctx, r)

		if err != nil {
			writeRequestError(w, r, err)
			return
		}

		body, err := io.ReadAll(call.Response.Body)
		defer call.Response.Body.Close()
		if err != nil {
			// The upstream headers are already chosen at this point, so the
			// status cannot be changed any more; the truncated body is the
			// honest answer and the reason goes to the log.
			log.LogError(ctx, "reading the upstream response: "+err.Error())
		}

		for hn, hvs := range call.Response.Header {
			w.Header().Del(hn)
			for _, hv := range hvs {
				w.Header().Add(hn, hv)
			}
		}

		w.Write(body)
	})
}

// writeRequestError maps a proxy error to the status that describes it, the
// same way writeAuthError does for the auth domain: the request package names
// what went wrong and the gateway decides how it is shown.
//
// Nothing here echoes err.Error(). The proxy errors carry upstream hosts and
// dial failures, so the message a client gets is a fixed one per class of
// failure, and the real cause stays in the log.
func writeRequestError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, request.ErrAccessDenied):
		// The path is real and reachable, only not by this caller.
		log.LogInfo(r.Context(), "proxy denied: "+err.Error())
		writeErrorResponse(w, map[string]any{"error": "unauthorized"}, http.StatusUnauthorized)

	case errors.Is(err, request.ErrServiceNotFound):
		// Nothing is configured to serve this path, which the client is the one
		// best placed to fix.
		log.LogInfo(r.Context(), "no service for the path: "+err.Error())
		writeErrorResponse(w, map[string]any{"error": "not found"}, http.StatusNotFound)

	case errors.Is(err, request.ErrServiceUnavailable):
		// Our upstream failed, not the request: say so with a gateway status
		// rather than blaming the caller.
		log.LogError(r.Context(), "upstream failed: "+err.Error())
		writeErrorResponse(w, map[string]any{"error": "service not available"}, http.StatusBadGateway)

	default:
		// A misconfigured host, an unencodable request or a response that
		// cannot be cached is ours, not the caller's.
		log.LogError(r.Context(), "proxy failed: "+err.Error())
		writeErrorResponse(w, map[string]any{"error": "internal error"}, http.StatusInternalServerError)
	}
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
