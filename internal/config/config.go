package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// GatewayConfig represents the entire gateway configuration
type GatewayConfig struct {
	Global   GlobalSettings   `yaml:"global"`
	Headers  HeaderSettings   `yaml:"headers"`
	Services []Service        `yaml:"services"`
	Logging  LoggingSettings  `yaml:"logging"`
	CORS     CORSSettings     `yaml:"cors"`
}

// Gateway defaults
const (
	DefaultTimeout             = 30 * time.Second
	DefaultRetryMaxAttempts    = 3
	DefaultRetryBackoff        = "exponential"
	DefaultRetryInitialInterval = 100 * time.Millisecond
	DefaultRetryMaxInterval    = 2 * time.Second
	DefaultRetryMultiplier     = 2.0
	DefaultMaxIdleConns        = 100
	DefaultMaxIdleConnsPerHost = 10
	DefaultIdleConnTimeout     = 90 * time.Second
	DefaultLogLevel            = "info"
	DefaultMaxBodySize         = 1024
	DefaultRouteMatchType      = "prefix"
)

// Default slice values (vars, not consts, because slices can't be const)
var (
	DefaultRouteAllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	DefaultCORSOrigins         = []string{"*"}
	DefaultCORSMethods         = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	DefaultCORSHeaders         = []string{"Origin", "Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "X-Request-ID"}
	DefaultCORSExposed         = []string{"Content-Length"}
)

// CORSSettings configures Cross-Origin Resource Sharing
type CORSSettings struct {
	Origins          []string `yaml:"origins"`
	Methods          []string `yaml:"methods"`
	Headers          []string `yaml:"headers"`
	ExposedHeaders   []string `yaml:"exposed_headers"`
	AllowCredentials bool     `yaml:"allow_credentials"`
}

// GlobalSettings contains global gateway settings
type GlobalSettings struct {
	DefaultTimeout time.Duration  `yaml:"timeout"`
	DefaultRetry   RetryPolicy    `yaml:"retry"`
	ConnectionPool ConnectionPool `yaml:"connection_pool"`
	ForwardAuth    *ForwardAuth   `yaml:"forward_auth,omitempty"`
}

// ConnectionPool configures HTTP connection pooling
type ConnectionPool struct {
	MaxIdleConns        int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost int           `yaml:"max_idle_conns_per_host"`
	IdleConnTimeout     time.Duration `yaml:"idle_conn_timeout"`
}

// ForwardAuth configures forward authentication
type ForwardAuth struct {
	Enabled               bool          `yaml:"enabled"`
	URL                   string        `yaml:"url"`
	Timeout               time.Duration `yaml:"timeout,omitempty"`
	ForwardHeaders        []string      `yaml:"forward_headers,omitempty"`
	ResponseHeaders       []string      `yaml:"response_headers,omitempty"`
	TrustForwardedHeaders bool          `yaml:"trust_forwarded_headers"`
}

// HeaderSettings configures header manipulation
type HeaderSettings struct {
	Add               map[string]string `yaml:"add"`
	Forward           []string          `yaml:"forward"`
	Remove            []string          `yaml:"remove"`
	XForwardedHeaders bool              `yaml:"x_forwarded_headers"`
}

// Service represents a backend microservice
type Service struct {
	Name    string       `yaml:"name"`
	BaseURL string       `yaml:"base_url"`
	Timeout time.Duration `yaml:"timeout,omitempty"`
	Retry   *RetryPolicy `yaml:"retry,omitempty"`
	Routes  []Route      `yaml:"routes"`
}

// Route represents a routing rule
type Route struct {
	Path        string             `yaml:"path"`
	Method      []string           `yaml:"method,omitempty"`
	MatchType   string             `yaml:"match_type"` // "prefix" or "exact"
	StripPath   bool               `yaml:"strip_path"`
	Timeout     time.Duration      `yaml:"timeout,omitempty"`
	Headers     *RouteHeaders      `yaml:"headers,omitempty"`
	ForwardAuth *RouteForwardAuth  `yaml:"forward_auth,omitempty"`
}

// RouteForwardAuth contains route-specific forward auth settings
type RouteForwardAuth struct {
	Enabled *bool  `yaml:"enabled,omitempty"` // nil = use global, true = require auth, false = skip auth
	URL     string `yaml:"url,omitempty"`     // Override global auth service URL
}

// RouteHeaders contains route-specific header overrides
type RouteHeaders struct {
	Add map[string]string `yaml:"add,omitempty"`
}

// RetryPolicy configures retry behavior
type RetryPolicy struct {
	MaxAttempts     int           `yaml:"max_attempts"`
	Backoff         string        `yaml:"backoff"` // exponential, linear, constant
	InitialInterval time.Duration `yaml:"initial_interval"`
	MaxInterval     time.Duration `yaml:"max_interval"`
	Multiplier      float64       `yaml:"multiplier,omitempty"`
}

// LoggingSettings configures proxy logging
type LoggingSettings struct {
	Enabled     bool     `yaml:"enabled"`
	Level       string   `yaml:"level"`
	LogBody     bool     `yaml:"log_body"`
	MaxBodySize int      `yaml:"max_body_size"`
	Include     []string `yaml:"include"`
}

// Validate performs validation on the configuration
func (c *GatewayConfig) Validate() error {
	// Validate gateway settings
	if c.Global.DefaultTimeout <= 0 {
		return errors.New("global.timeout must be greater than 0")
	}

	if err := c.Global.DefaultRetry.Validate(); err != nil {
		return fmt.Errorf("global.retry: %w", err)
	}

	if err := c.Global.ConnectionPool.Validate(); err != nil {
		return fmt.Errorf("gateway.connection_pool: %w", err)
	}

	// Validate forward auth if configured
	if c.Global.ForwardAuth != nil {
		if err := c.Global.ForwardAuth.Validate(); err != nil {
			return fmt.Errorf("gateway.forward_auth: %w", err)
		}
	}

	// Validate services
	if len(c.Services) == 0 {
		return errors.New("at least one service must be defined")
	}

	serviceNames := make(map[string]bool)
	for i, service := range c.Services {
		if service.Name == "" {
			return fmt.Errorf("services[%d]: name is required", i)
		}

		if serviceNames[service.Name] {
			return fmt.Errorf("services[%d]: duplicate service name '%s'", i, service.Name)
		}
		serviceNames[service.Name] = true

		if service.BaseURL == "" {
			return fmt.Errorf("services[%d] (%s): base_url is required", i, service.Name)
		}

		if len(service.Routes) == 0 {
			return fmt.Errorf("services[%d] (%s): at least one route must be defined", i, service.Name)
		}

		// Validate service-level retry policy if present
		if service.Retry != nil {
			if err := service.Retry.Validate(); err != nil {
				return fmt.Errorf("services[%d] (%s).retry: %w", i, service.Name, err)
			}
		}

		// Validate routes
		for j, route := range service.Routes {
			if err := route.Validate(); err != nil {
				return fmt.Errorf("services[%d] (%s).routes[%d]: %w", i, service.Name, j, err)
			}
		}
	}

	return nil
}

// Validate validates the connection pool settings
func (cp *ConnectionPool) Validate() error {
	if cp.MaxIdleConns < 0 {
		return errors.New("max_idle_conns must be >= 0")
	}
	if cp.MaxIdleConnsPerHost < 0 {
		return errors.New("max_idle_conns_per_host must be >= 0")
	}
	if cp.IdleConnTimeout < 0 {
		return errors.New("idle_conn_timeout must be >= 0")
	}
	return nil
}

// Validate validates the forward auth settings
func (fa *ForwardAuth) Validate() error {
	if !fa.Enabled {
		return nil // If disabled, no validation needed
	}

	if fa.URL == "" {
		return errors.New("url is required when forward_auth is enabled")
	}

	if !strings.HasPrefix(fa.URL, "http://") && !strings.HasPrefix(fa.URL, "https://") {
		return errors.New("url must start with http:// or https://")
	}

	if fa.Timeout < 0 {
		return errors.New("timeout must be >= 0")
	}

	// Set default timeout if not specified
	if fa.Timeout == 0 {
		fa.Timeout = 5 * time.Second
	}

	return nil
}

// Validate validates the retry policy
func (rp *RetryPolicy) Validate() error {
	if rp.MaxAttempts < 0 {
		return errors.New("max_attempts must be >= 0")
	}

	if rp.Backoff != "exponential" && rp.Backoff != "linear" && rp.Backoff != "constant" {
		return fmt.Errorf("backoff must be 'exponential', 'linear', or 'constant', got '%s'", rp.Backoff)
	}

	if rp.InitialInterval <= 0 {
		return errors.New("initial_interval must be greater than 0")
	}

	if rp.MaxInterval <= 0 {
		return errors.New("max_interval must be greater than 0")
	}

	if rp.MaxInterval < rp.InitialInterval {
		return errors.New("max_interval must be >= initial_interval")
	}

	if rp.Backoff == "exponential" && rp.Multiplier <= 1.0 {
		return errors.New("multiplier must be > 1.0 for exponential backoff")
	}

	return nil
}

// Validate validates the route configuration
func (r *Route) Validate() error {
	if r.Path == "" {
		return errors.New("path is required")
	}

	if r.MatchType != "prefix" && r.MatchType != "exact" {
		return fmt.Errorf("match_type must be 'prefix' or 'exact', got '%s'", r.MatchType)
	}

	// Validate HTTP methods if specified
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true,
		"PATCH": true, "HEAD": true, "OPTIONS": true,
	}

	for _, method := range r.Method {
		if !validMethods[method] {
			return fmt.Errorf("invalid HTTP method: %s", method)
		}
	}

	return nil
}

// GetTimeout returns the effective timeout for a route
// Priority: route timeout > service timeout > gateway default timeout
func (r *Route) GetTimeout(service *Service, gateway *GlobalSettings) time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	if service.Timeout > 0 {
		return service.Timeout
	}
	return gateway.DefaultTimeout
}

// GetRetryPolicy returns the effective retry policy for a service
// Priority: service retry > gateway default retry
func (s *Service) GetRetryPolicy(gateway *GlobalSettings) RetryPolicy {
	if s.Retry != nil {
		return *s.Retry
	}
	return gateway.DefaultRetry
}

// IsForwardAuthEnabled returns true if forward auth is enabled for this route
// Priority: route setting > global setting
func (r *Route) IsForwardAuthEnabled(gateway *GlobalSettings) bool {
	// Route-specific override takes precedence
	if r.ForwardAuth != nil && r.ForwardAuth.Enabled != nil {
		return *r.ForwardAuth.Enabled
	}

	// Fall back to global setting
	if gateway.ForwardAuth != nil {
		return gateway.ForwardAuth.Enabled
	}

	// Default: no auth
	return false
}

// GetAuthServiceURL returns the auth service URL for this route
// Priority: route override > global setting
func (r *Route) GetAuthServiceURL(gateway *GlobalSettings) string {
	// Route-specific override
	if r.ForwardAuth != nil && r.ForwardAuth.URL != "" {
		return r.ForwardAuth.URL
	}

	// Global setting
	if gateway.ForwardAuth != nil {
		return gateway.ForwardAuth.URL
	}

	return ""
}
