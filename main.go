package main

import (
	"context"
	"github.com/pegnia/sidecar/internal/agones"
	"github.com/pegnia/sidecar/internal/api"
	"github.com/pegnia/sidecar/internal/config"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"agones.dev/agones/sdks/go"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	cfg := config.LoadFromEnv()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("Starting Unified Agones Sidecar")

	agonesSDK, err := sdk.NewSDK()
	if err != nil {
		slog.Error("Could not connect to Agones SDK", "error", err)
		os.Exit(1)
	}
	slog.Info("Successfully connected to Agones SDK")

	apiServer := api.NewServer(cfg.API.ListenAddress, cfg.Data.Root)

	go agones.RunManager(ctx, cfg.Agones, agonesSDK)
	go apiServer.Run(ctx)

	<-ctx.Done()

	slog.Info("Shutdown signal received. Exiting.")
}
