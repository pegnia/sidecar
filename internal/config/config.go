// Package config reads the sidecar's settings from SIDECAR_* environment variables.
package config

import (
	"os"
	"strconv"
)

// Config holds all settings of the sidecar.
type Config struct {
	API  APIConfig
	Data DataConfig
}

// APIConfig holds settings for the file management API.
type APIConfig struct {
	ListenAddress string
	// APIKey, when set, must be sent in the X-API-Key header of every request except /health.
	APIKey string
	// RateLimit is the number of requests per minute allowed per client IP.
	RateLimit int
}

// DataConfig specifies the data directory and log file paths.
type DataConfig struct {
	Root string
	// StdoutFile is the game's console log, relative to Root.
	StdoutFile string
}

// LoadFromEnv loads configuration from environment variables.
func LoadFromEnv() *Config {
	return &Config{
		API: APIConfig{
			ListenAddress: getEnv("SIDECAR_API_ADDR", ":9999"),
			APIKey:        getEnv("SIDECAR_API_KEY", ""),
			RateLimit:     getEnvInt("SIDECAR_RATE_LIMIT", 60),
		},
		Data: DataConfig{
			Root:       getEnv("SIDECAR_DATA_ROOT", "/data"),
			StdoutFile: getEnv("SIDECAR_STDOUT_FILE", "logs/stdout.log"),
		},
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
