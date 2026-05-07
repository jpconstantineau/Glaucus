package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/jpconstantineau/Glaucus/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runtime, err := app.NewRuntime(app.RuntimeOptions{
		Name: "Glaucus",
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := runtime.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
