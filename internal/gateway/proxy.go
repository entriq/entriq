package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"entriq/internal/config"
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
	backend, err := url.Parse(p.match.Service.URL)
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

// IsWebSocketUpgrade checks if the request is a WebSocket upgrade request.
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "Upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// ServeWebSocket hijacks the client connection, dials the backend directly,
// forwards the original handshake, and pumps bytes bidirectionally until
// either side closes or the server context is cancelled.
func (p *ProxyHandler) ServeWebSocket(w http.ResponseWriter, r *http.Request) error {
	// Parse backend URL
	backend, err := url.Parse(p.match.Service.URL)
	if err != nil {
		http.Error(w, "Invalid backend URL", http.StatusInternalServerError)
		return fmt.Errorf("invalid backend URL: %w", err)
	}

	// Determine backend dial address
	backendHost := backend.Host
	if !strings.Contains(backendHost, ":") {
		if backend.Scheme == "https" || backend.Scheme == "wss" {
			backendHost += ":443"
		} else {
			backendHost += ":80"
		}
	}

	// Build the request path for the backend
	reqPath := r.URL.Path
	if p.match.Route.StripPath {
		reqPath = strings.TrimPrefix(reqPath, p.match.Route.Path)
		if reqPath == "" {
			reqPath = "/"
		}
	}
	if r.URL.RawQuery != "" {
		reqPath += "?" + r.URL.RawQuery
	}

	// Dial the backend directly, bypassing the pooled transport
	var backendConn net.Conn
	if backend.Scheme == "https" || backend.Scheme == "wss" {
		backendConn, err = tls.Dial("tcp", backendHost, &tls.Config{
			ServerName: backend.Hostname(),
		})
	} else {
		backendConn, err = net.DialTimeout("tcp", backendHost, 10*time.Second)
	}
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return fmt.Errorf("dial backend: %w", err)
	}
	defer backendConn.Close()

	// Write the original HTTP request (the upgrade handshake) to the backend
	handshake := fmt.Sprintf("%s %s HTTP/1.1\r\n", r.Method, reqPath)
	handshake += fmt.Sprintf("Host: %s\r\n", backend.Host)
	for key, vals := range r.Header {
		for _, val := range vals {
			handshake += fmt.Sprintf("%s: %s\r\n", key, val)
		}
	}
	handshake += "\r\n"

	if _, err := backendConn.Write([]byte(handshake)); err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return fmt.Errorf("write handshake to backend: %w", err)
	}

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return fmt.Errorf("hijacking not supported")
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Hijack failed", http.StatusInternalServerError)
		return fmt.Errorf("hijack: %w", err)
	}
	defer clientConn.Close()

	// Relay the backend's 101 response and then pump bytes bidirectionally.
	// Use the request context to detect server shutdown.
	ctx := r.Context()

	var wg sync.WaitGroup
	wg.Add(2)

	// backend → client
	go func() {
		defer wg.Done()
		copyUntilDone(ctx, clientConn, backendConn)
		// When backend closes, half-close client write side if possible
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// client → backend
	go func() {
		defer wg.Done()
		copyUntilDone(ctx, backendConn, clientConn)
		if tc, ok := backendConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
	return nil
}

// copyUntilDone copies from src to dst until src is exhausted, dst errors,
// or the context is cancelled.
func copyUntilDone(ctx context.Context, dst, src net.Conn) {
	done := make(chan struct{})
	go func() {
		io.Copy(dst, src)
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		// Server shutting down — close both ends to unblock io.Copy
		src.Close()
		dst.Close()
		<-done
	}
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
