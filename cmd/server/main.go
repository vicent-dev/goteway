package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"goteway/app"
)

func main() {
	s, err := app.NewServer()
	if err != nil {
		log.Fatalf("could not start goteway: %v", err)
	}

	// create a context that cancels on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := s.Run(ctx); err != nil {
		log.Fatalf("goteway stopped: %v", err)
	}
}
