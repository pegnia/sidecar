package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agones.dev/agones/sdks/go"
	"github.com/koutselakismanos/agnostic-agones-sidecar/internal/config"
	"github.com/koutselakismanos/agnostic-agones-sidecar/internal/probe"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.FromEnv()
	p := &probe.PingProbe{Config: cfg.Ping}

	slog.Info("Starting Agnostic Agones Sidecar")
	if err := runMonitor(p, cfg); err != nil {
		slog.Error("Monitor failed", "error", err)
		os.Exit(1)
	}

	slog.Info("Monitor finished gracefully.")
}

func runMonitor(p probe.ReadinessProbe, cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	agonesSDK, err := sdk.NewSDK()
	if err != nil {
		return fmt.Errorf("could not connect to Agones SDK: %w", err)
	}
	slog.Info("Successfully connected to Agones SDK")

	slog.Info("Waiting for initial delay", "duration", cfg.InitialDelay)
	time.Sleep(cfg.InitialDelay)

	slog.Info("Starting readiness probe...")
	if err := p.Probe(ctx); err != nil {
		return fmt.Errorf("readiness probe failed: %w", err)
	}

	if err := agonesSDK.Ready(); err != nil {
		return fmt.Errorf("failed to send Ready signal: %w", err)
	}
	slog.Info(">>> Server is Ready! Starting health checks. <<<")

	ticker := time.NewTicker(cfg.HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := agonesSDK.Health(); err != nil {
				slog.Warn("Failed to send health ping", "error", err)
			}
		case <-ctx.Done():
			slog.Info("Shutdown signal received. Exiting.")
			return nil
		}
	}
}
