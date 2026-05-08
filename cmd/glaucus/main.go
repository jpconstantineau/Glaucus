package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jpconstantineau/Glaucus/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.Options{Name: "Glaucus"}); err != nil {
		log.Fatal(err)
	}
}
