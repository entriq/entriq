package config

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

const (
	// DefaultConfigPath is the default configuration file path
	DefaultConfigPath = "./config/entriq.yaml"
	// ConfigPathEnvVar is the environment variable name for config path override
	ConfigPathEnvVar = "CONFIG_PATH"
)

// Load loads the gateway configuration from the default path or CONFIG_PATH env var
func Load() (*GatewayConfig, error) {
	configPath := DefaultConfigPath

	// Check if CONFIG_PATH environment variable is set
	if envPath := os.Getenv(ConfigPathEnvVar); envPath != "" {
		configPath = envPath
	}

	return LoadConfig(configPath)
}

// LoadConfig loads the gateway configuration from the specified path
func LoadConfig(path string) (*GatewayConfig, error) {
	// Read the config file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file '%s': %w", path, err)
	}

	// Parse YAML
	var config GatewayConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file '%s': %w", path, err)
	}

	// Apply default values
	MergeDefaults(&config)

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration in '%s': %w", path, err)
	}

	return &config, nil
}

// MergeDefaults applies default values to the configuration
func MergeDefaults(cfg *GatewayConfig) {
	// Gateway defaults
	if cfg.Gateway.DefaultTimeout == 0 {
		cfg.Gateway.DefaultTimeout = 30000000000 // 30 seconds in nanoseconds
	}

	// Default retry policy
	if cfg.Gateway.DefaultRetry.MaxAttempts == 0 {
		cfg.Gateway.DefaultRetry.MaxAttempts = 3
	}
	if cfg.Gateway.DefaultRetry.Backoff == "" {
		cfg.Gateway.DefaultRetry.Backoff = "exponential"
	}
	if cfg.Gateway.DefaultRetry.InitialInterval == 0 {
		cfg.Gateway.DefaultRetry.InitialInterval = 100000000 // 100ms in nanoseconds
	}
	if cfg.Gateway.DefaultRetry.MaxInterval == 0 {
		cfg.Gateway.DefaultRetry.MaxInterval = 2000000000 // 2s in nanoseconds
	}
	if cfg.Gateway.DefaultRetry.Multiplier == 0 {
		cfg.Gateway.DefaultRetry.Multiplier = 2.0
	}

	// Connection pool defaults
	if cfg.Gateway.ConnectionPool.MaxIdleConns == 0 {
		cfg.Gateway.ConnectionPool.MaxIdleConns = 100
	}
	if cfg.Gateway.ConnectionPool.MaxIdleConnsPerHost == 0 {
		cfg.Gateway.ConnectionPool.MaxIdleConnsPerHost = 10
	}
	if cfg.Gateway.ConnectionPool.IdleConnTimeout == 0 {
		cfg.Gateway.ConnectionPool.IdleConnTimeout = 90000000000 // 90s in nanoseconds
	}

	// CORS defaults
	if len(cfg.CORS.AllowedOrigins) == 0 {
		cfg.CORS.AllowedOrigins = []string{}
	}
	if len(cfg.CORS.AllowedMethods) == 0 {
		cfg.CORS.AllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	}
	if len(cfg.CORS.AllowedHeaders) == 0 {
		cfg.CORS.AllowedHeaders = []string{"Origin", "Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "X-Request-ID"}
	}
	if len(cfg.CORS.ExposedHeaders) == 0 {
		cfg.CORS.ExposedHeaders = []string{"Content-Length"}
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.MaxBodySize == 0 {
		cfg.Logging.MaxBodySize = 1024
	}

	// Service and route defaults
	for i := range cfg.Services {
		service := &cfg.Services[i]

		for j := range service.Routes {
			route := &service.Routes[j]

			// Default match type
			if route.MatchType == "" {
				route.MatchType = "prefix"
			}

			// If methods not specified, allow all methods
			if len(route.Method) == 0 {
				route.Method = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
			}
		}
	}
}
