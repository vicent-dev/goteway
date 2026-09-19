package app

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

type server struct {
	r *mux.Router
	c *config
}

func NewServer() *server {

	s := server{
		c: loadConfig(),
		r: mux.NewRouter(),
	}

	s.routes()

	return &s
}

func (s *server) Run() error {
	return http.ListenAndServe(":"+s.c.Server.Port, handlers.RecoveryHandler()(s.r))
}

func (s *server) writeResponse(w http.ResponseWriter, response map[string]any) {
	w.WriteHeader(http.StatusOK)

	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}

func (s *server) writeErrorResponse(w http.ResponseWriter, response map[string]any, errorCode int) {
	w.WriteHeader(errorCode)
	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}
