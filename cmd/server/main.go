package main

import (
	"context"
	"goteway/app"
	"os/signal"
	"syscall"
)

func main() {
	s := app.NewServer()

	// create a context that cancels on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := s.Run(ctx); err != nil {
		panic(err)
	}
}
