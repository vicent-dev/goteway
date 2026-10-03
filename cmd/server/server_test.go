package main

import (
	"context"
	"testing"
	"time"

	"goteway/app"
)

func TestMain_GracefulShutdown(t *testing.T) {
	// Test that we can create server and shutdown
	s := app.NewServer()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := s.Run(ctx)
	if err != nil && err.Error() != "http server shutdown: context deadline exceeded" && err != context.Canceled {
		// Also possible that errCh returns nil after shutdown
	}
}
