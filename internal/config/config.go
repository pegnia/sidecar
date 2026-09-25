// Package config reads the sidecar's settings from SIDECAR_* environment variables.
package config

import (
	"errors"
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
	// Insecure allows running without an API key (local experiments only).
	Insecure bool
}

// DataConfig specifies the data directory and log file paths.
type DataConfig struct {
	Root string
}

// LoadFromEnv loads configuration from environment variables.
func LoadFromEnv() *Config {
	return &Config{
		API: APIConfig{
			ListenAddress: getEnv("SIDECAR_API_ADDR", ":9999"),
			APIKey:        getEnv("SIDECAR_API_KEY", ""),
			RateLimit:     getEnvInt("SIDECAR_RATE_LIMIT", 60),
			Insecure:      getEnv("SIDECAR_INSECURE", "") == "true",
		},
		Data: DataConfig{
			Root: getEnv("SIDECAR_DATA_ROOT", "/data"),
		},
	}
}

// Validate refuses a configuration that would serve the game's files to anyone: without
// an API key the sidecar only starts when SIDECAR_INSECURE=true says so explicitly.
func (c *Config) Validate() error {
	if c.API.APIKey == "" && !c.API.Insecure {
		return errors.New("SIDECAR_API_KEY is not set; set it, or SIDECAR_INSECURE=true to run without authentication")
	}
	return nil
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
