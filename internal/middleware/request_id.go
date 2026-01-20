package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// RequestIDHeader is the header name for request ID
	RequestIDHeader = "X-Request-ID"
	// RequestIDContextKey is the context key for storing request ID
	RequestIDContextKey = "request_id"
)

// RequestID is a middleware that generates or preserves a request ID
// and sets it on the request headers and gin context
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if client already sent X-Request-ID
		requestID := c.GetHeader(RequestIDHeader)

		// If no request ID from client, generate a new UUID
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Set request ID in request header (for forwarding to downstream services)
		c.Request.Header.Set(RequestIDHeader, requestID)

		// Store in gin context for easy access throughout the request lifecycle
		c.Set(RequestIDContextKey, requestID)

		// Set in response header so client can track their request
		c.Header(RequestIDHeader, requestID)

		c.Next()
	}
}

// GetRequestID retrieves the request ID from gin context
func GetRequestID(c *gin.Context) string {
	if requestID, exists := c.Get(RequestIDContextKey); exists {
		if id, ok := requestID.(string); ok {
			return id
		}
	}
	// Fallback: try to get from header
	if requestID := c.GetHeader(RequestIDHeader); requestID != "" {
		return requestID
	}
	// Last resort: generate new ID (shouldn't happen if middleware is properly configured)
	return uuid.New().String()
}
