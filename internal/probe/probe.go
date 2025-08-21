package probe

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/pegnia/sidecar/internal/config"
)

// ReadinessProbe is the interface that all readiness checking strategies must implement.
type ReadinessProbe interface {
	Probe(ctx context.Context) error
}

type PingProbe struct {
	Config config.AgonesConfig
}

func (p *PingProbe) Probe(ctx context.Context) error {
	protocol := strings.ToLower(p.Config.PingProtocol)
	address := net.JoinHostPort(p.Config.PingHost, p.Config.PingPort)
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
				// For TCP, just establishing a connection is sufficient
				var conn net.Conn
				conn, err = net.DialTimeout(protocol, address, p.Config.PingTimeout)
				if conn != nil {
					conn.Close()
				}
			} else if protocol == "udp" {
				// For UDP, we need to send data and try to receive a response
				// or at least verify the connection doesn't immediately error
				var conn net.Conn
				conn, err = net.DialTimeout(protocol, address, p.Config.PingTimeout)
				if err == nil && conn != nil {
					// Write a ping message
					_, err = conn.Write([]byte("ping"))
					if err != nil {
						slog.Warn("Failed to write to UDP connection", "error", err)
					} else {
						// Try to read a response, but don't require it
						// Some UDP servers don't respond to arbitrary data
						buf := make([]byte, 1024)
						conn.SetReadDeadline(time.Now().Add(p.Config.PingTimeout))
						_, readErr := conn.Read(buf)
						if readErr != nil {
							// If it's a timeout error, that's expected for many UDP services
							// We'll still consider the probe successful if we could write
							if !strings.Contains(readErr.Error(), "timeout") {
								slog.Debug("No response from UDP server (expected for many services)", "error", readErr)
							}
						} else {
							slog.Debug("Received response from UDP server")
						}
					}

					conn.Close()
				}
			} else {
				err = fmt.Errorf("unsupported protocol: %s", protocol)
			}

			if err == nil {
				slog.Info("Ping probe successful")
				return nil
			}
			slog.Warn("Ping attempt failed, retrying...", "error", err)
		}
	}
}
