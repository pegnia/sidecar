package config

import (
	"log/slog"
	"os"
	"time"
)

// Config holds all settings for the application.
type Config struct {
	InitialDelay   time.Duration
	HealthInterval time.Duration
	Ping           PingConfig
}

type PingConfig struct {
	Host     string
	Port     string
	Protocol string
	Timeout  time.Duration
}

// FromEnv loads the configuration from environment variables.
func FromEnv() *Config {
	return &Config{
		InitialDelay:   getEnvDurationOr("INITIAL_DELAY", 30*time.Second),
		HealthInterval: getEnvDurationOr("HEALTH_INTERVAL", 15*time.Second),
		Ping: PingConfig{
			Host:     getEnvOr("PING_HOST", "127.0.0.1"),
			Port:     getEnvOr("PING_PORT", "7777"),
			Protocol: getEnvOr("PING_PROTOCOL", "tcp"),
			Timeout:  getEnvDurationOr("PING_TIMEOUT", 5*time.Second),
		},
	}
}

func getEnvOr(key, fallback string) string {
	if value, ok := os.LookupEnv("AGNOSTIC_SIDECAR_" + key); ok {
		return value
	}
	return fallback
}

func getEnvDurationOr(key string, fallback time.Duration) time.Duration {
	if value, ok := os.LookupEnv("AGNOSTIC_SIDECAR_" + key); ok {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
		slog.Warn("Invalid duration format in environment variable, using default", "key", key, "value", value)
	}
	return fallback
}
