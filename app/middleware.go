package app

import (
	"context"
	"encoding/json"
	"goteway/pkg/auth"
	"goteway/pkg/log"
	"net/http"

	"golang.org/x/time/rate"
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		ctx := context.WithValue(r.Context(), log.METHOD_CTX_LOG_KEY, r.Method)
		ctx = context.WithValue(ctx, log.PATH_CTX_LOG_KEY, r.URL.Path)
		ctx = context.WithValue(ctx, auth.AUTH_CTX_KEY, r.Header.Get(auth.BEARER_TOKEN_HEADER_KEY))

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)

		log.LogRequest(ctx)
	})
}

func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func rateLimiterMiddleware(next http.Handler) http.Handler {
	limiter := rate.NewLimiter(5, 10)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			byteResponse, _ := json.Marshal(map[string]string{"error": "rate limit reached"})
			_, _ = w.Write(byteResponse)
			return
		} else {
			next.ServeHTTP(w, r)
		}
	})
}
