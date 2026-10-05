package app

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"goteway/pkg/auth"
	"goteway/pkg/cache"
	"goteway/pkg/log"
	"goteway/pkg/request"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(rateLimiterMiddleware)

	health := healthHandler()
	s.r.HandleFunc("/health", health).Methods("GET", "HEAD")
	s.r.HandleFunc("/health/", health).Methods("GET", "HEAD")

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

		copyUpstreamHeaders(w.Header(), call.Response.Header)

		// Written rather than left implicit: an upstream 404 or 500 has to reach
		// the caller as one instead of being flattened into a 200. It is the same
		// value on both paths, because a cache hit restores the status it stored.
		w.WriteHeader(call.Response.StatusCode)
		w.Write(body)
	})
}

// copyUpstreamHeaders copies the upstream response headers onto the response the
// client will receive, minus the ones that describe the connection the upstream
// used. That connection does not exist on this side, and forwarding its framing
// next to a body that has been buffered produces a response the client cannot
// parse. Content-Length goes for the same reason: the length of what is written
// is this handler's to declare, not the upstream's to be copied.
func copyUpstreamHeaders(dst, src http.Header) {
	for name, values := range src {
		if request.IsHopByHop(name) || strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

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
