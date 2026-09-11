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
	"entriq/internal/retry"
)

// corsResponseHeaders lists the CORS-related headers stripped from backend
// responses so they don't duplicate the ones set by the gateway's CORS middleware.
var corsResponseHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Expose-Headers",
}

// stripCORSHeaders removes CORS headers set by the backend so the gateway's
// own CORS middleware remains the single source of truth for the client.
func stripCORSHeaders(h http.Header) {
	for _, header := range corsResponseHeaders {
		h.Del(header)
	}

	// Drop only the "Origin" value from Vary, preserving any other
	// values the backend may have legitimately set for caching.
	if vary := h.Values("Vary"); len(vary) > 0 {
		remaining := make([]string, 0, len(vary))
		for _, v := range vary {
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if part != "" && !strings.EqualFold(part, "Origin") {
					remaining = append(remaining, part)
				}
			}
		}
		h.Del("Vary")
		if len(remaining) > 0 {
			h.Set("Vary", strings.Join(remaining, ", "))
		}
	}
}

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

	// Check if retry should be used
	retryPolicy := p.match.Service.GetRetryPolicy(&p.config.Global)
	if retryPolicy.MaxAttempts > 1 {
		p.serveWithRetry(w, r, backend, retryPolicy)
		return
	}

	p.serveDirect(w, r, backend)
}

// serveWithRetry proxies the request using retry.ExecuteWithRetry.
func (p *ProxyHandler) serveWithRetry(w http.ResponseWriter, r *http.Request, backend *url.URL, policy config.RetryPolicy) {
	outReq := p.buildBackendRequest(r, backend)

	client := &http.Client{Transport: p.transport}
	resp, err := retry.ExecuteWithRetry(outReq.Context(), outReq, client, policy)
	if err != nil {
		if err == context.DeadlineExceeded {
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			return
		}
		if err == context.Canceled {
			return
		}
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	stripCORSHeaders(resp.Header)

	// Copy response headers
	for key, vals := range resp.Header {
		for _, val := range vals {
			w.Header().Add(key, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// serveDirect proxies the request through httputil.ReverseProxy (no retry).
func (p *ProxyHandler) serveDirect(w http.ResponseWriter, r *http.Request, backend *url.URL) {
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.Transport = p.transport

	// Customize the Director function to handle path rewriting
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		p.applyDirector(req, r, backend)
	}

	// Strip any CORS headers set by the backend so the gateway's own
	// CORS middleware remains the single source of truth for the client.
	proxy.ModifyResponse = func(resp *http.Response) error {
		stripCORSHeaders(resp.Header)
		return nil
	}

	// Handle errors from the backend
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if err == context.DeadlineExceeded {
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			return
		}
		if err == context.Canceled {
			return
		}
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

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

// buildBackendRequest creates an outbound request for the backend, applying
// the same path rewriting and header forwarding as the Director.
func (p *ProxyHandler) buildBackendRequest(r *http.Request, backend *url.URL) *http.Request {
	outReq := r.Clone(r.Context())
	outReq.URL.Scheme = backend.Scheme
	outReq.URL.Host = backend.Host
	outReq.Host = backend.Host
	outReq.RequestURI = "" // http.Client.Do rejects requests with RequestURI set
	p.applyDirector(outReq, r, backend)
	return outReq
}

// applyDirector applies path stripping and URL rewriting to an outbound request.
func (p *ProxyHandler) applyDirector(req *http.Request, originalReq *http.Request, backend *url.URL) {
	if p.match.Route.StripPath {
		newPath := strings.TrimPrefix(req.URL.Path, p.match.Route.Path)
		if newPath == "" {
			newPath = "/"
		}
		req.URL.Path = newPath
	}

	req.URL.Scheme = backend.Scheme
	req.URL.Host = backend.Host

	if !p.match.Route.StripPath {
		req.URL.Path = originalReq.URL.Path
	}

	req.URL.RawQuery = originalReq.URL.RawQuery
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
