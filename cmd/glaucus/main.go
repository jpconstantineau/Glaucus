package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jpconstantineau/Glaucus/internal/cli"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.Options{Name: "Glaucus"}); err != nil {
		slog.Error("glaucus command failed", "error", err)
		os.Exit(1)
	}
}
