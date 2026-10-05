package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"goteway/pkg/auth"
	"goteway/pkg/log"
)

type server struct {
	r   *mux.Router
	c   *Config
	rdb *redis.Client
	db  *gorm.DB

	httpServer http.Server
}

func NewServer() (*server, error) {
	c, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return newServer(c)
}

func newServer(c *Config) (*server, error) {
	s := &server{
		r: mux.NewRouter(),
		c: c,
	}

	if err := s.authConfig(); err != nil {
		return nil, err
	}

	s.redis()
	s.routes()

	s.httpServer = http.Server{
		Addr:              ":" + s.c.Server.Port,
		Handler:           handlers.RecoveryHandler()(s.r),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

func (s *server) authConfig() error {
	if err := s.c.AuthConfig().Validate(); err != nil {
		return err
	}

	db, err := OpenDB(s.c.DB)
	if err != nil {
		return err
	}
	s.db = db

	return nil
}

func (s *server) authService() *auth.Service {
	return auth.NewService(s.c.AuthConfig(), auth.NewGormStore(s.db), nil)
}

func (s *server) Run(ctx context.Context) error {

	errCh := make(chan error, 1)

	go func() {
		var err error
		if s.c.TLSEnabled() {
			if s.c.Server.CertFile == "" || s.c.Server.KeyFile == "" {
				errCh <- fmt.Errorf("tls enabled but cert_file or key_file missing")
				close(errCh)
				return
			}
			err = s.httpServer.ListenAndServeTLS(s.c.Server.CertFile, s.c.Server.KeyFile)
		} else {
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	<-ctx.Done()
	log.LogInfo(ctx, "shutting down HTTP server")

	// give in-flight requests a deadline to finish
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdownErr := s.httpServer.Shutdown(shutdownCtx)
	if s.db != nil {
		if err := CloseDB(shutdownCtx, s.db); err != nil {
			log.LogWarn(shutdownCtx, "closing database: "+err.Error())
		}
	}

	if shutdownErr != nil {
		return fmt.Errorf("http server shutdown: %w", shutdownErr)
	}

	return <-errCh
}

func writeErrorResponse(w http.ResponseWriter, response map[string]any, errorCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errorCode)
	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}
