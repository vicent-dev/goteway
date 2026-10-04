package app

import (
	"context"
	"net/http"

	"golang.org/x/time/rate"

	"goteway/pkg/auth"
	"goteway/pkg/log"
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		ctx := context.WithValue(r.Context(), log.METHOD_CTX_LOG_KEY, r.Method)
		ctx = context.WithValue(ctx, log.PATH_CTX_LOG_KEY, r.URL.Path)

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)

		log.LogRequest(ctx)
	})
}

func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func rateLimiterMiddleware(next http.Handler) http.Handler {
	limiter := rate.NewLimiter(5, 10)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			writeErrorResponse(w, map[string]any{"error": "rate limit reached"}, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddleware guards the proxy with a verified access token.
//
// The authentication itself lives in pkg/auth, which decides who the caller is;
// the gateway only decides how a rejection is rendered.
func (s *server) authMiddleware(next http.Handler) http.Handler {
	return auth.RequireAuth(s.authService(), unauthorizedResponse)(next)
}

func unauthorizedResponse(w http.ResponseWriter, r *http.Request, err error) {
	log.LogInfo(r.Context(), "unauthorized: "+err.Error())
	writeErrorResponse(w, map[string]any{"error": "unauthorized"}, http.StatusUnauthorized)
}
