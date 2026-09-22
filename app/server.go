package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
)

type server struct {
	r          *mux.Router
	c          *config
	rdb        *redis.Client
	httpServer http.Server
}

func NewServer() *server {

	s := server{
		c: loadConfig(),
		r: mux.NewRouter(),
	}

	s.redis()
	s.routes()

	s.httpServer = http.Server{
		Addr:    ":" + s.c.Server.Port,
		Handler: handlers.RecoveryHandler()(s.r),
	}

	return &s
}

func (s *server) Run(ctx context.Context) error {

	errCh := make(chan error, 1)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	<-ctx.Done()
	log.Println("shutting down HTTP server...")

	// give in-flight requests a deadline to finish
	shutdownCtx, _ := context.WithTimeout(context.Background(), 10*time.Second)

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	return <-errCh
}

// @todo remove
func (s *server) writeResponse(w http.ResponseWriter, response map[string]any) {
	w.WriteHeader(http.StatusOK)

	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}

// @todo remove
func (s *server) writeErrorResponse(w http.ResponseWriter, response map[string]any, errorCode int) {
	w.WriteHeader(errorCode)
	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}
