package app

import (
	"context"
	"goteway/pkg/auth"
	"goteway/pkg/log"
	"net/http"
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
