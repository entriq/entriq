package gateway

import (
	"fmt"
	"log"
	"net/http"
	"entriq/internal/config"
	"entriq/internal/middleware"
	"time"

	"github.com/gin-gonic/gin"
)

// Gateway coordinates all gateway components
type Gateway struct {
	config            *config.GatewayConfig
	router            *Router
	transport         *http.Transport
	headerManipulator *middleware.HeaderManipulator
	proxyLogger       *middleware.ProxyLogger
}

// New creates a new gateway instance
func New(cfg *config.GatewayConfig) (*Gateway, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create router
	router := NewRouter(cfg)

	// Create HTTP transport with connection pooling
	transport := CreateTransport(cfg.Gateway.ConnectionPool)

	// Create header manipulator
	headerManipulator := middleware.NewHeaderManipulator(cfg.Headers)

	// Create proxy logger
	proxyLogger := middleware.NewProxyLogger(cfg.Logging)

	gateway := &Gateway{
		config:            cfg,
		router:            router,
		transport:         transport,
		headerManipulator: headerManipulator,
		proxyLogger:       proxyLogger,
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

		// Get client IP
		clientIP := middleware.GetClientIP(c.Request)

		// Apply header manipulation
		routeHeaders := match.Route.Headers
		g.headerManipulator.Apply(c.Request, routeHeaders, clientIP)

		// Log request start
		requestID := g.proxyLogger.LogRequest(c.Request, match.Service.Name, match.Service.BaseURL)

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
		g.proxyLogger.LogResponse(requestID, match.Service.Name, match.Service.BaseURL, wrappedWriter.StatusCode, latency, nil)
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
