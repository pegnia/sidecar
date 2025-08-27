package config

import (
	"os"
	"time"
)

// Config holds all settings for the unified sidecar.
type Config struct {
	Agones AgonesConfig
	API    APIConfig
	Data   DataConfig
}

// AgonesConfig holds settings for the Agones SDK interaction.
type AgonesConfig struct {
	InitialDelay   time.Duration
	HealthInterval time.Duration
	PingHost       string
	PingPort       string
	PingProtocol   string
	PingTimeout    time.Duration
}

// APIConfig holds settings for the internal file management API.
type APIConfig struct {
	ListenAddress string
}

// DataConfig specifies the data directory and log file paths.
type DataConfig struct {
	Root       string
	StdoutFile string
}

// LoadFromEnv loads configuration from environment variables.
func LoadFromEnv() *Config {
	return &Config{
		Agones: AgonesConfig{
			InitialDelay:   getEnvDuration("SIDECAR_INITIAL_DELAY", 30*time.Second),
			HealthInterval: getEnvDuration("SIDECAR_HEALTH_INTERVAL", 15*time.Second),
			PingHost:       getEnv("SIDECAR_PING_HOST", "127.0.0.1"),
			PingPort:       getEnv("SIDECAR_PING_PORT", "25565"),
			PingProtocol:   getEnv("SIDECAR_PING_PROTOCOL", "tcp"),
			PingTimeout:    getEnvDuration("SIDECAR_PING_TIMEOUT", 5*time.Second),
		},
		API: APIConfig{
			ListenAddress: getEnv("SIDECAR_API_ADDR", ":9999"),
		},
		Data: DataConfig{
			Root:       getEnv("SIDECAR_DATA_ROOT", "/data"),
			StdoutFile: getEnv("SIDECAR_STDOUT_FILE", "logs/stdout.log"),
		},
	}
}

// Helper functions to read environment variables with defaults.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return fallback
}
