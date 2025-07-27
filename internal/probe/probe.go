package probe

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/koutselakismanos/agnostic-agones-sidecar/internal/config"
)

// ReadinessProbe is the interface that all readiness checking strategies must implement.
type ReadinessProbe interface {
	Probe(ctx context.Context) error
}

type PingProbe struct {
	Config config.PingConfig
}

func (p *PingProbe) Probe(ctx context.Context) error {
	protocol := strings.ToLower(p.Config.Protocol)
	address := net.JoinHostPort(p.Config.Host, p.Config.Port)
	slog.Info("Starting ping probe", "address", address, "protocol", protocol)

	ticker := time.NewTicker(5 * time.Second) // Retry interval
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var err error
			if protocol == "tcp" {
				var conn net.Conn
				conn, err = net.DialTimeout(protocol, address, p.Config.Timeout)
				if conn != nil {
					conn.Close()
				}
			} else { // Handles "udp"
				var conn net.Conn
				conn, err = net.DialTimeout(protocol, address, p.Config.Timeout)
				if err == nil {
					_, err = conn.Write([]byte("ping"))
					if conn != nil {
						conn.Close()
					}
				}
			}

			if err == nil {
				slog.Info("Ping probe successful")
				return nil
			}
			slog.Warn("Ping attempt failed, retrying...", "error", err)
		}
	}
}
