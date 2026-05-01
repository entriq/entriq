package middleware

import (
	"strings"

	"entriq/internal/config"

	"github.com/gin-gonic/gin"
)

func CORS(cfg config.CORSSettings) gin.HandlerFunc {
	// Check if wildcard is explicitly configured
	wildcardOrigin := false
	allowedOrigins := make(map[string]struct{}, len(cfg.Origins))
	for _, o := range cfg.Origins {
		if o == "*" {
			wildcardOrigin = true
			break
		}
		allowedOrigins[o] = struct{}{}
	}

	allowedMethods := strings.Join(cfg.Methods, ", ")
	allowedHeaders := strings.Join(cfg.Headers, ", ")
	exposedHeaders := strings.Join(cfg.ExposedHeaders, ", ")

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		if wildcardOrigin {
			// Explicit opt-in to wildcard — allow all origins
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			if _, ok := allowedOrigins[origin]; ok {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Vary", "Origin")
				if cfg.AllowCredentials {
					c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
		}

		c.Writer.Header().Set("Access-Control-Allow-Methods", allowedMethods)
		c.Writer.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		if exposedHeaders != "" {
			c.Writer.Header().Set("Access-Control-Expose-Headers", exposedHeaders)
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
