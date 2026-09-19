package app

import (
	"context"
	"net/http"
	"time"

	"github.com/en-vee/alog"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(jsonMiddleware)

	//ping example
	s.r.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]any)
		response["ping"] = "pong pong"

		alog.Info("log before response")
		s.writeResponse(w, response)

		go func() {
			s.rdb.Set(context.TODO(), "request_"+time.Now().String(), response, time.Minute)
			alog.Info("redis cache after response")
		}()

	}).Methods("GET")
}
