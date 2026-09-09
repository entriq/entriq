package gateway

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"entriq/internal/config"
	"entriq/internal/middleware"

	"github.com/gin-gonic/gin"
)

// Gateway coordinates all gateway components
type Gateway struct {
	config            *config.GatewayConfig
	router            *Router
	transport         *http.Transport
	headerManipulator *middleware.HeaderManipulator
	proxyLogger       *middleware.ProxyLogger
	// Cache of forward auth middleware instances by config signature
	authMiddlewareCache map[string]gin.HandlerFunc
	authMiddlewareMutex sync.RWMutex
}

// New creates a new gateway instance
func New(cfg *config.GatewayConfig) (*Gateway, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create router
	router := NewRouter(cfg)

	// Create HTTP transport with connection pooling
	transport := CreateTransport(cfg.Global.ConnectionPool)

	// Create header manipulator
	headerManipulator := middleware.NewHeaderManipulator(cfg.Headers)

	// Create proxy logger
	proxyLogger := middleware.NewProxyLogger(cfg.Logging)

	gateway := &Gateway{
		config:              cfg,
		router:              router,
		transport:           transport,
		headerManipulator:   headerManipulator,
		proxyLogger:         proxyLogger,
		authMiddlewareCache: make(map[string]gin.HandlerFunc),
	}

	log.Printf("Gateway initialized with %d services", len(cfg.Services))
	if cfg.Logging.Level == "debug" {
		log.Println(router.DebugRoutes())
	}

	return gateway, nil
}

// ProxyHandler returns a Gin handler function that proxies requests
func (g *Gateway) ProxyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// Match route
		match, err := g.router.Match(c.Request.Method, c.Request.URL.Path)
		if err != nil {
			// No route matched, return 404
			c.JSON(http.StatusNotFound, gin.H{
				"status": "failed",
				"error": gin.H{
					"code":    404,
					"message": "Resource not found",
				},
			})
			return
		}

		// Check if forward auth is enabled for this route
		if match.Route.IsForwardAuthEnabled(&g.config.Global) {
			// Get auth service URL
			authURL := match.Route.GetAuthServiceURL(&g.config.Global)
			if authURL == "" {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Forward auth enabled but no auth service URL configured",
				})
				return
			}

			// Get or create cached forward auth middleware
			authMiddleware := g.getOrCreateAuthMiddleware(authURL, match.Route)
			authMiddleware(c)

			// If auth middleware aborted the request, log and stop here
			if c.IsAborted() {
				// Log the auth failure with full context
				latency := time.Since(startTime)
				statusCode := c.Writer.Status()
				requestID := c.Request.Header.Get("X-Request-ID")
				g.proxyLogger.LogFailure(
					requestID,
					c.Request.Method,
					c.Request.URL.Path,
					match.Service.Name,
					statusCode,
					latency,
					"forward auth failed",
				)
				return
			}
		}

		// Get client IP
		clientIP := middleware.GetClientIP(c.Request)

		// Apply header manipulation
		routeHeaders := match.Route.Headers
		g.headerManipulator.Apply(c.Request, routeHeaders, clientIP)

		// Log request start
		requestID := g.proxyLogger.LogRequest(c.Request, match.Service.Name, match.Service.URL)

		// Wrap response writer to capture status code
		wrappedWriter := g.proxyLogger.WrapResponseWriter(c.Writer)
		c.Writer = wrappedWriter

		// Create proxy handler
		proxyHandler := NewProxyHandler(match, g.config, g.transport)

		// Execute proxy (this handles the actual request forwarding)
		proxyHandler.ServeHTTP(c.Writer, c.Request)

		// Calculate latency
		latency := time.Since(startTime)

		// Log response
		g.proxyLogger.LogResponse(requestID, match.Service.Name, match.Service.URL, wrappedWriter.StatusCode, latency, nil)
	}
}

// GetConfig returns the gateway configuration
func (g *Gateway) GetConfig() *config.GatewayConfig {
	return g.config
}

// GetStats returns gateway statistics
func (g *Gateway) GetStats() map[string]interface{} {
	stats := g.router.GetStats()
	stats["services"] = len(g.config.Services)
	return stats
}

// buildForwardAuthConfig creates a ForwardAuthConfig from the gateway configuration
// and any per-route overrides.
func (g *Gateway) buildForwardAuthConfig(authURL string, route *config.Route) *middleware.ForwardAuthConfig {
	cfg := middleware.DefaultForwardAuthConfig()
	cfg.URL = authURL

	// Apply global forward auth settings if configured
	if g.config.Global.ForwardAuth != nil {
		fa := g.config.Global.ForwardAuth

		if fa.Timeout > 0 {
			cfg.Timeout = fa.Timeout
		}

		if len(fa.ForwardHeaders) > 0 {
			cfg.ForwardHeaders = fa.ForwardHeaders
		}

		if len(fa.ResponseHeaders) > 0 {
			cfg.ResponseHeaders = fa.ResponseHeaders
		}

		cfg.TrustForwardedHeaders = fa.TrustForwardedHeaders
	}

	// Per-route overrides would be applied here when RouteForwardAuth
	// is extended with Timeout, ForwardHeaders, ResponseHeaders fields.
	_ = route

	return cfg
}

// authConfigSignature returns a stable cache key derived from the full effective
// ForwardAuthConfig, so that two routes sharing an auth URL but differing in
// headers or timeout get distinct middleware instances.
func authConfigSignature(cfg *middleware.ForwardAuthConfig) string {
	fwd := make([]string, len(cfg.ForwardHeaders))
	copy(fwd, cfg.ForwardHeaders)
	sort.Strings(fwd)

	resp := make([]string, len(cfg.ResponseHeaders))
	copy(resp, cfg.ResponseHeaders)
	sort.Strings(resp)

	return fmt.Sprintf("%s|%s|%s|%s|%v",
		cfg.URL,
		cfg.Timeout,
		strings.Join(fwd, ","),
		strings.Join(resp, ","),
		cfg.TrustForwardedHeaders,
	)
}

// getOrCreateAuthMiddleware retrieves or creates a forward auth middleware instance.
// Middlewares are cached by the full effective config signature (URL + headers + timeout)
// to avoid creating new HTTP clients per request while correctly handling per-route
// config differences.
func (g *Gateway) getOrCreateAuthMiddleware(authURL string, route *config.Route) gin.HandlerFunc {
	authConfig := g.buildForwardAuthConfig(authURL, route)
	cacheKey := authConfigSignature(authConfig)

	// Try read lock first (fast path for existing middlewares)
	g.authMiddlewareMutex.RLock()
	if mw, exists := g.authMiddlewareCache[cacheKey]; exists {
		g.authMiddlewareMutex.RUnlock()
		return mw
	}
	g.authMiddlewareMutex.RUnlock()

	// Need to create new middleware - acquire write lock
	g.authMiddlewareMutex.Lock()
	defer g.authMiddlewareMutex.Unlock()

	// Double-check after acquiring write lock (another goroutine might have created it)
	if mw, exists := g.authMiddlewareCache[cacheKey]; exists {
		return mw
	}

	// Create new middleware instance
	mw := middleware.ForwardAuth(authConfig)
	g.authMiddlewareCache[cacheKey] = mw

	log.Printf("Created forward auth middleware for URL: %s", authURL)
	return mw
}
