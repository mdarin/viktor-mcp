package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"viktor/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, os.Stderr); err != nil {
		os.Exit(1)
	}
}
