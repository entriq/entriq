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

// MergeDefaults applies default values to the configuration.
// User-supplied values always take precedence; defaults are only applied to zero/empty fields.
func MergeDefaults(cfg *GatewayConfig) {
	// Gateway defaults
	if cfg.Global.DefaultTimeout == 0 {
		cfg.Global.DefaultTimeout = DefaultTimeout
	}

	// Default retry policy
	if cfg.Global.DefaultRetry.MaxAttempts == 0 {
		cfg.Global.DefaultRetry.MaxAttempts = DefaultRetryMaxAttempts
	}
	if cfg.Global.DefaultRetry.Backoff == "" {
		cfg.Global.DefaultRetry.Backoff = DefaultRetryBackoff
	}
	if cfg.Global.DefaultRetry.InitialInterval == 0 {
		cfg.Global.DefaultRetry.InitialInterval = DefaultRetryInitialInterval
	}
	if cfg.Global.DefaultRetry.MaxInterval == 0 {
		cfg.Global.DefaultRetry.MaxInterval = DefaultRetryMaxInterval
	}
	if cfg.Global.DefaultRetry.Multiplier == 0 {
		cfg.Global.DefaultRetry.Multiplier = DefaultRetryMultiplier
	}

	// Connection pool defaults
	if cfg.Global.ConnectionPool.MaxIdleConns == 0 {
		cfg.Global.ConnectionPool.MaxIdleConns = DefaultMaxIdleConns
	}
	if cfg.Global.ConnectionPool.MaxIdleConnsPerHost == 0 {
		cfg.Global.ConnectionPool.MaxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	}
	if cfg.Global.ConnectionPool.IdleConnTimeout == 0 {
		cfg.Global.ConnectionPool.IdleConnTimeout = DefaultIdleConnTimeout
	}

	// CORS defaults — only applied when the field is not set in config
	if len(cfg.CORS.Origins) == 0 {
		cfg.CORS.Origins = DefaultCORSOrigins
	}
	if len(cfg.CORS.Methods) == 0 {
		cfg.CORS.Methods = DefaultCORSMethods
	}
	if len(cfg.CORS.Headers) == 0 {
		cfg.CORS.Headers = DefaultCORSHeaders
	}
	if len(cfg.CORS.ExposedHeaders) == 0 {
		cfg.CORS.ExposedHeaders = DefaultCORSExposed
	}

	// Logging defaults
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = DefaultLogLevel
	}
	if cfg.Logging.MaxBodySize == 0 {
		cfg.Logging.MaxBodySize = DefaultMaxBodySize
	}

	// Service and route defaults
	for i := range cfg.Services {
		service := &cfg.Services[i]

		for j := range service.Routes {
			route := &service.Routes[j]

			if route.MatchType == "" {
				route.MatchType = DefaultRouteMatchType
			}
			if len(route.Method) == 0 {
				route.Method = DefaultRouteAllowedMethods
			}
		}
	}
}
