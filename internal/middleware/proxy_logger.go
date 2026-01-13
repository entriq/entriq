package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"entriq/internal/config"
	"time"

	"github.com/google/uuid"
)

// ProxyLogger logs requests and responses for proxied traffic
type ProxyLogger struct {
	config config.LoggingSettings
}

// NewProxyLogger creates a new proxy logger
func NewProxyLogger(cfg config.LoggingSettings) *ProxyLogger {
	return &ProxyLogger{
		config: cfg,
	}
}

// ProxyLog represents a structured log entry for a proxied request
type ProxyLog struct {
	Timestamp   string        `json:"timestamp"`
	RequestID   string        `json:"request_id"`
	Method      string        `json:"method"`
	Path        string        `json:"path"`
	ServiceName string        `json:"service_name"`
	Backend     string        `json:"backend"`
	StatusCode  int           `json:"status_code,omitempty"`
	Latency     time.Duration `json:"latency_ms,omitempty"`
	Error       string        `json:"error,omitempty"`
	RequestBody string        `json:"request_body,omitempty"`
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	StatusCode int
	written    bool
}

// newResponseWriter creates a new responseWriter
func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		StatusCode:     http.StatusOK, // Default to 200 if WriteHeader is never called
	}
}

// WriteHeader captures the status code
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.StatusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures that a write happened (status code defaults to 200)
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// CloseNotify implements gin.ResponseWriter
func (rw *responseWriter) CloseNotify() <-chan bool {
	return rw.ResponseWriter.(http.CloseNotifier).CloseNotify()
}

// Status implements gin.ResponseWriter
func (rw *responseWriter) Status() int {
	return rw.StatusCode
}

// Size implements gin.ResponseWriter
func (rw *responseWriter) Size() int {
	return 0
}

// WriteString implements gin.ResponseWriter
func (rw *responseWriter) WriteString(s string) (int, error) {
	return rw.Write([]byte(s))
}

// Written implements gin.ResponseWriter
func (rw *responseWriter) Written() bool {
	return rw.written
}

// WriteHeaderNow implements gin.ResponseWriter
func (rw *responseWriter) WriteHeaderNow() {
	if !rw.written {
		rw.written = true
		rw.ResponseWriter.WriteHeader(rw.StatusCode)
	}
}

// Pusher implements gin.ResponseWriter
func (rw *responseWriter) Pusher() http.Pusher {
	if pusher, ok := rw.ResponseWriter.(http.Pusher); ok {
		return pusher
	}
	return nil
}

// Flush implements gin.ResponseWriter
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements gin.ResponseWriter
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported")
}

// LogRequest logs the start of a proxied request and returns a request ID
func (l *ProxyLogger) LogRequest(r *http.Request, serviceName, backend string) string {
	if !l.config.Enabled {
		return ""
	}

	// Generate request ID
	requestID := uuid.New().String()

	entry := ProxyLog{
		Timestamp:   time.Now().Format(time.RFC3339),
		RequestID:   requestID,
		Method:      r.Method,
		Path:        r.URL.Path,
		ServiceName: serviceName,
		Backend:     backend,
	}

	// Optionally log request body
	if l.config.LogBody && l.shouldInclude("request_body") {
		if r.Body != nil {
			// Read body
			bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, int64(l.config.MaxBodySize)))
			if err == nil {
				entry.RequestBody = string(bodyBytes)
				// Restore body for further reading
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}
	}

	// Log request start
	if l.config.Level == "debug" {
		l.logJSON(entry)
	}

	return requestID
}

// LogResponse logs the completion of a proxied request
func (l *ProxyLogger) LogResponse(requestID, serviceName, backend string, statusCode int, latency time.Duration, err error) {
	if !l.config.Enabled {
		return
	}

	entry := ProxyLog{
		Timestamp:   time.Now().Format(time.RFC3339),
		RequestID:   requestID,
		ServiceName: serviceName,
		Backend:     backend,
		StatusCode:  statusCode,
		Latency:     latency / time.Millisecond, // Convert to milliseconds
	}

	if err != nil {
		entry.Error = err.Error()
	}

	l.logJSON(entry)
}

// WrapResponseWriter wraps an http.ResponseWriter to capture status code
func (l *ProxyLogger) WrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return newResponseWriter(w)
}

// logJSON logs a structured JSON entry
func (l *ProxyLogger) logJSON(entry ProxyLog) {
	// Filter fields based on config
	filteredEntry := l.filterFields(entry)

	// Marshal to JSON
	jsonBytes, err := json.Marshal(filteredEntry)
	if err != nil {
		log.Printf("Error marshaling log entry: %v", err)
		return
	}

	// Log based on level
	switch l.config.Level {
	case "error":
		if entry.Error != "" || entry.StatusCode >= 500 {
			log.Println(string(jsonBytes))
		}
	case "warn":
		if entry.Error != "" || entry.StatusCode >= 400 {
			log.Println(string(jsonBytes))
		}
	case "info", "debug":
		log.Println(string(jsonBytes))
	}
}

// filterFields filters log fields based on configuration
func (l *ProxyLogger) filterFields(entry ProxyLog) map[string]interface{} {
	result := make(map[string]interface{})

	if l.shouldInclude("timestamp") {
		result["timestamp"] = entry.Timestamp
	}
	if l.shouldInclude("request_id") {
		result["request_id"] = entry.RequestID
	}
	if l.shouldInclude("method") {
		result["method"] = entry.Method
	}
	if l.shouldInclude("path") {
		result["path"] = entry.Path
	}
	if l.shouldInclude("service_name") {
		result["service_name"] = entry.ServiceName
	}
	if l.shouldInclude("backend") {
		result["backend"] = entry.Backend
	}
	if l.shouldInclude("status_code") && entry.StatusCode > 0 {
		result["status_code"] = entry.StatusCode
	}
	if l.shouldInclude("latency") && entry.Latency > 0 {
		result["latency_ms"] = entry.Latency
	}
	if l.shouldInclude("error") && entry.Error != "" {
		result["error"] = entry.Error
	}
	if l.shouldInclude("request_body") && entry.RequestBody != "" {
		result["request_body"] = entry.RequestBody
	}

	return result
}

// shouldInclude checks if a field should be included in the log
func (l *ProxyLogger) shouldInclude(field string) bool {
	// If no include list specified, include everything
	if len(l.config.Include) == 0 {
		return true
	}

	// Check if field is in include list
	for _, f := range l.config.Include {
		if f == field {
			return true
		}
	}

	return false
}
