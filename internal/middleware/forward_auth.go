package middleware

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ForwardAuthConfig holds configuration for forward authentication
type ForwardAuthConfig struct {
	// URL of the authentication service
	AuthServiceURL string
	// Timeout for auth service requests
	Timeout time.Duration
	// Headers to forward to the auth service (defaults to all)
	ForwardHeaders []string
	// Headers to copy from auth response to backend request
	ResponseHeaders []string
	// Trust forwarded headers (X-Forwarded-*)
	TrustForwardedHeaders bool
}

// DefaultForwardAuthConfig returns sensible defaults
func DefaultForwardAuthConfig() *ForwardAuthConfig {
	return &ForwardAuthConfig{
		Timeout: 5 * time.Second,
		ForwardHeaders: []string{
			"Authorization",
			"Cookie",
			"X-Forwarded-For",
			"X-Forwarded-Host",
			"X-Forwarded-Proto",
			"X-Forwarded-Uri",
		},
		ResponseHeaders: []string{
			"X-Forwarded-User",
			"X-Auth-User",
			"X-Auth-Email",
			"X-Auth-Name",
			"X-Auth-Groups",
		},
		TrustForwardedHeaders: true,
	}
}

// ForwardAuth creates a middleware that implements Traefik-style forward authentication
// It makes a request to an external auth service and:
// - On 2xx response: Extracts headers from auth response and continues to backend
// - On 4xx/5xx response: Returns the auth service response directly to client
func ForwardAuth(config *ForwardAuthConfig) gin.HandlerFunc {
	if config == nil {
		config = DefaultForwardAuthConfig()
	}

	// Create HTTP client for auth requests
	client := &http.Client{
		Timeout: config.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects, return them to client
			return http.ErrUseLastResponse
		},
	}

	return func(c *gin.Context) {
		// Create auth request
		authReq, err := http.NewRequest("GET", config.AuthServiceURL, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to create auth request",
			})
			c.Abort()
			return
		}

		// Forward headers to auth service
		forwardHeaders(c.Request, authReq, config)

		// Make request to auth service
		authResp, err := client.Do(authReq)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "Auth service unavailable",
			})
			c.Abort()
			return
		}
		defer authResp.Body.Close()

		// Check auth response status
		if authResp.StatusCode >= 200 && authResp.StatusCode < 300 {
			// Auth succeeded - copy response headers and continue
			copyAuthHeaders(authResp, c, config.ResponseHeaders)
			c.Next()
			return
		}

		// Auth failed - return auth service response to client
		// Copy status code
		c.Status(authResp.StatusCode)

		// Copy headers
		for key, values := range authResp.Header {
			for _, value := range values {
				c.Header(key, value)
			}
		}

		// Copy body
		body, err := io.ReadAll(authResp.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to read auth response",
			})
			c.Abort()
			return
		}

		// Write response body
		if len(body) > 0 {
			c.Data(authResp.StatusCode, authResp.Header.Get("Content-Type"), body)
		}

		c.Abort()
	}
}

// forwardHeaders copies specified headers from the original request to the auth request
func forwardHeaders(originalReq *http.Request, authReq *http.Request, config *ForwardAuthConfig) {
	// If no specific headers configured, forward all
	if len(config.ForwardHeaders) == 0 {
		for key, values := range originalReq.Header {
			for _, value := range values {
				authReq.Header.Add(key, value)
			}
		}
		return
	}

	// Forward only specified headers
	for _, headerName := range config.ForwardHeaders {
		if values := originalReq.Header.Values(headerName); len(values) > 0 {
			for _, value := range values {
				authReq.Header.Add(headerName, value)
			}
		}
	}

	// Add X-Forwarded-* headers if configured
	if config.TrustForwardedHeaders {
		addForwardedHeaders(originalReq, authReq)
	}
}

// addForwardedHeaders adds standard X-Forwarded-* headers to the auth request
func addForwardedHeaders(originalReq *http.Request, authReq *http.Request) {
	// X-Forwarded-Uri
	if authReq.Header.Get("X-Forwarded-Uri") == "" {
		uri := originalReq.RequestURI
		authReq.Header.Set("X-Forwarded-Uri", uri)
	}

	// X-Forwarded-Method
	if authReq.Header.Get("X-Forwarded-Method") == "" {
		authReq.Header.Set("X-Forwarded-Method", originalReq.Method)
	}

	// X-Forwarded-Host
	if authReq.Header.Get("X-Forwarded-Host") == "" && originalReq.Host != "" {
		authReq.Header.Set("X-Forwarded-Host", originalReq.Host)
	}

	// X-Forwarded-Proto
	if authReq.Header.Get("X-Forwarded-Proto") == "" {
		proto := "http"
		if originalReq.TLS != nil {
			proto = "https"
		}
		authReq.Header.Set("X-Forwarded-Proto", proto)
	}

	// X-Forwarded-For
	if authReq.Header.Get("X-Forwarded-For") == "" {
		clientIP := getClientIP(originalReq)
		if clientIP != "" {
			authReq.Header.Set("X-Forwarded-For", clientIP)
		}
	}
}

// copyAuthHeaders copies specified headers from auth response to the context
// These will be forwarded to the backend service
func copyAuthHeaders(authResp *http.Response, c *gin.Context, responseHeaders []string) {
	for _, headerName := range responseHeaders {
		if values := authResp.Header.Values(headerName); len(values) > 0 {
			for _, value := range values {
				c.Request.Header.Add(headerName, value)
			}
		}
	}
}

// getClientIP extracts the client IP from the request
func getClientIP(req *http.Request) string {
	// Check X-Forwarded-For
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP
	if xri := req.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	if idx := strings.LastIndex(req.RemoteAddr, ":"); idx > 0 {
		return req.RemoteAddr[:idx]
	}

	return req.RemoteAddr
}

// ValidateForwardAuthConfig validates the forward auth configuration
func ValidateForwardAuthConfig(config *ForwardAuthConfig) error {
	if config == nil {
		return fmt.Errorf("forward auth config cannot be nil")
	}

	if config.AuthServiceURL == "" {
		return fmt.Errorf("auth service URL is required")
	}

	if !strings.HasPrefix(config.AuthServiceURL, "http://") && !strings.HasPrefix(config.AuthServiceURL, "https://") {
		return fmt.Errorf("auth service URL must start with http:// or https://")
	}

	if config.Timeout < 0 {
		return fmt.Errorf("timeout cannot be negative")
	}

	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}

	return nil
}
