package agones

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"agones.dev/agones/sdks/go"
	"github.com/pegnia/sidecar/internal/config"
)

// RunManager connects to the Agones SDK and manages the game server lifecycle.
func RunManager(ctx context.Context, cfg config.AgonesConfig, agonesSDK *sdk.SDK) {
	slog.Info("Starting Agones manager...")

	slog.Info("Waiting for initial delay before probing", "duration", cfg.InitialDelay)
	time.Sleep(cfg.InitialDelay)

	slog.Info("Starting readiness probe...", "target", fmt.Sprintf("%s:%s", cfg.PingHost, cfg.PingPort))
	if err := probeGameServer(ctx, cfg); err != nil {
		slog.Error("Readiness probe failed, game server will not be marked as Ready", "error", err)
		// We exit here because if the probe fails, the server can't become Ready.
		// Agones will eventually shut down the Unhealthy pod.
		return
	}

	if err := agonesSDK.Ready(); err != nil {
		slog.Error("Failed to send Ready signal to Agones", "error", err)
		return
	}
	slog.Info(">>> Server is Ready! Starting health checks. <<<")

	ticker := time.NewTicker(cfg.HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := agonesSDK.Health(); err != nil {
				slog.Warn("Failed to send health ping", "error", err)
			} else {
				slog.Debug("Health ping sent successfully")
			}
		case <-ctx.Done():
			slog.Info("Shutdown signal received. Stopping Agones manager.")
			return
		}
	}
}

// probeGameServer continuously tries to connect to the game server port.
func probeGameServer(ctx context.Context, cfg config.AgonesConfig) error {
	protocol := strings.ToLower(cfg.PingProtocol)
	address := net.JoinHostPort(cfg.PingHost, cfg.PingPort)
	ticker := time.NewTicker(2 * time.Second) // Retry every 2 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			conn, err := net.DialTimeout(protocol, address, cfg.PingTimeout)
			if err == nil {
				conn.Close()
				slog.Info("Readiness probe successful!")
				return nil
			}
			slog.Warn("Readiness probe attempt failed, retrying...", "error", err)
		}
	}
}
