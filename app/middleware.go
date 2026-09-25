package app

import (
	"context"
	"goteway/pkg/log"
	"net/http"
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		ctx := context.WithValue(r.Context(), log.METHOD_LOG_KEY, r.Method)
		ctx = context.WithValue(ctx, log.PATH_LOG_KEY, r.URL.Path)

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
