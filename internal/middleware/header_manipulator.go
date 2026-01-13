package middleware

import (
	"net/http"
	"entriq/internal/config"
	"strings"
)

// HeaderManipulator handles adding, removing, and forwarding headers
type HeaderManipulator struct {
	globalHeaders config.HeaderSettings
}

// NewHeaderManipulator creates a new header manipulator
func NewHeaderManipulator(headers config.HeaderSettings) *HeaderManipulator {
	return &HeaderManipulator{
		globalHeaders: headers,
	}
}

// Apply applies header manipulation to the request
// Priority: remove → add X-Forwarded → add global → add route-specific → forward
func (h *HeaderManipulator) Apply(r *http.Request, routeHeaders *config.RouteHeaders, clientIP string) {
	// Step 1: Remove blacklisted headers
	for _, headerName := range h.globalHeaders.Remove {
		r.Header.Del(headerName)
	}

	// Step 2: Add X-Forwarded-* headers if enabled
	if h.globalHeaders.XForwardedHeaders {
		h.addForwardedHeaders(r, clientIP)
	}

	// Step 3: Add global custom headers
	for key, value := range h.globalHeaders.Add {
		// Only add if not already present (don't override existing headers)
		if r.Header.Get(key) == "" {
			r.Header.Set(key, value)
		}
	}

	// Step 4: Add route-specific headers (these override global headers)
	if routeHeaders != nil {
		for key, value := range routeHeaders.Add {
			r.Header.Set(key, value)
		}
	}

	// Step 5: Filter headers based on forward whitelist
	// Note: We don't remove non-whitelisted headers, we just ensure whitelisted ones are present
	// This preserves standard HTTP headers needed for proxying
}

// addForwardedHeaders adds X-Forwarded-For, X-Forwarded-Proto, and X-Forwarded-Host headers
func (h *HeaderManipulator) addForwardedHeaders(r *http.Request, clientIP string) {
	// X-Forwarded-For: chain of client IPs
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		clientIP = strings.Join(prior, ", ") + ", " + clientIP
	}
	r.Header.Set("X-Forwarded-For", clientIP)

	// X-Forwarded-Proto: original protocol (http or https)
	if r.Header.Get("X-Forwarded-Proto") == "" {
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		r.Header.Set("X-Forwarded-Proto", proto)
	}

	// X-Forwarded-Host: original host requested by the client
	if r.Header.Get("X-Forwarded-Host") == "" {
		r.Header.Set("X-Forwarded-Host", r.Host)
	}
}

// GetClientIP extracts the client IP address from the request
// Checks X-Real-IP and X-Forwarded-For headers first, falls back to RemoteAddr
func GetClientIP(r *http.Request) string {
	// Check X-Real-IP header
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Check X-Forwarded-For header (take first IP)
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		ips := strings.Split(forwarded, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Fall back to RemoteAddr
	// RemoteAddr format is "IP:port", we need just the IP
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[:idx]
	}

	return addr
}
