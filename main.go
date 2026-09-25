// Command sidecar runs next to a game server container and serves a small HTTP API for
// managing the server's files and streaming its console log. It knows nothing about the
// orchestrator: readiness of the game is checked by Kubernetes probes on the game container.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pegnia/sidecar/internal/api"
	"github.com/pegnia/sidecar/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	cfg := config.LoadFromEnv()
	if err := cfg.Validate(); err != nil {
		slog.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("Starting sidecar", "address", cfg.API.ListenAddress, "data_root", cfg.Data.Root,
		"auth", cfg.API.APIKey != "")

	apiServer, err := api.NewServer(cfg)
	if err != nil {
		slog.Error("Cannot start file API", "error", err)
		os.Exit(1)
	}
	if err := apiServer.Run(ctx); err != nil {
		slog.Error("File API stopped with an error", "error", err)
		os.Exit(1)
	}
	slog.Info("Shutdown complete")
}
