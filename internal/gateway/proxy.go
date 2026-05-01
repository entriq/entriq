package gateway

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"entriq/internal/config"
	"strings"
	"time"
)

// ProxyHandler handles proxying requests to backend services
type ProxyHandler struct {
	match     *RouteMatch
	transport *http.Transport
	config    *config.GatewayConfig
}

// NewProxyHandler creates a new proxy handler for a route match
func NewProxyHandler(match *RouteMatch, cfg *config.GatewayConfig, transport *http.Transport) *ProxyHandler {
	return &ProxyHandler{
		match:     match,
		transport: transport,
		config:    cfg,
	}
}

// ServeHTTP implements the http.Handler interface
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse backend URL
	backend, err := url.Parse(p.match.Service.BaseURL)
	if err != nil {
		http.Error(w, "Invalid backend URL", http.StatusInternalServerError)
		return
	}

	// Determine effective timeout
	timeout := p.match.Route.GetTimeout(p.match.Service, &p.config.Global)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Update request with timeout context
	r = r.WithContext(ctx)

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.Transport = p.transport

	// Customize the Director function to handle path rewriting
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// Call original director to set up basic proxying
		originalDirector(req)

		// Handle path stripping if configured
		if p.match.Route.StripPath {
			// Remove the matched prefix from the path
			newPath := strings.TrimPrefix(req.URL.Path, p.match.Route.Path)
			if newPath == "" {
				newPath = "/"
			}
			req.URL.Path = newPath
		}

		// Update the request URL to point to the backend
		req.URL.Scheme = backend.Scheme
		req.URL.Host = backend.Host

		// Preserve original path if not stripping
		if !p.match.Route.StripPath {
			req.URL.Path = r.URL.Path
		}

		// Preserve query parameters
		req.URL.RawQuery = r.URL.RawQuery
	}

	// Handle errors from the backend
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Check if it's a timeout error
		if err == context.DeadlineExceeded {
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			return
		}

		// Check if it's a canceled context (client disconnected)
		if err == context.Canceled {
			return
		}

		// Generic backend error
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	// Proxy the request
	proxy.ServeHTTP(w, r)
}

// CreateTransport creates an HTTP transport with connection pooling settings
func CreateTransport(poolConfig config.ConnectionPool) *http.Transport {
	return &http.Transport{
		MaxIdleConns:        poolConfig.MaxIdleConns,
		MaxIdleConnsPerHost: poolConfig.MaxIdleConnsPerHost,
		IdleConnTimeout:     poolConfig.IdleConnTimeout,
		DisableKeepAlives:   false,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,

		// Additional timeouts for robustness
		DialContext: (&http.Transport{
			DialContext: (&http.Transport{}).DialContext,
		}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
