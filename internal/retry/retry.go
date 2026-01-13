package retry

import (
	"context"
	"math"
	"math/rand"
	"net/http"
	"entriq/internal/config"
	"time"
)

// ExecuteWithRetry executes an HTTP request with retry logic and backoff
func ExecuteWithRetry(ctx context.Context, req *http.Request, client *http.Client, policy config.RetryPolicy) (*http.Response, error) {
	var lastErr error
	var resp *http.Response

	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		// Check if context is cancelled
		if ctx.Err() != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ctx.Err()
		}

		// Execute the request
		resp, lastErr = client.Do(req.Clone(ctx))

		// If no error and response is successful, return immediately
		if lastErr == nil && !shouldRetry(req.Method, resp.StatusCode) {
			return resp, nil
		}

		// If we have a response, check if we should retry based on status code
		if resp != nil {
			// Close the response body to free resources
			if resp.Body != nil {
				resp.Body.Close()
			}

			// Don't retry if status code indicates we shouldn't
			if !shouldRetryStatusCode(resp.StatusCode) {
				return resp, lastErr
			}
		}

		// Don't sleep after the last attempt
		if attempt < policy.MaxAttempts-1 {
			// Calculate backoff duration
			backoff := calculateBackoff(attempt, policy)

			// Sleep with context awareness
			select {
			case <-time.After(backoff):
				// Continue to next attempt
			case <-ctx.Done():
				// Context cancelled during backoff
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, ctx.Err()
			}
		}
	}

	// All retries exhausted
	if resp != nil {
		return resp, lastErr
	}
	return nil, lastErr
}

// shouldRetry determines if a request should be retried based on method and status code
func shouldRetry(method string, statusCode int) bool {
	// Only retry idempotent methods by default
	idempotent := method == "GET" || method == "HEAD" || method == "OPTIONS" ||
		method == "PUT" || method == "DELETE"

	if !idempotent {
		return false
	}

	// Retry on retriable status codes
	return shouldRetryStatusCode(statusCode)
}

// shouldRetryStatusCode determines if a status code is retriable
func shouldRetryStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError,     // 500
		http.StatusBadGateway,              // 502
		http.StatusServiceUnavailable,      // 503
		http.StatusGatewayTimeout:          // 504
		return true
	default:
		return false
	}
}

// calculateBackoff calculates the backoff duration for a retry attempt
func calculateBackoff(attempt int, policy config.RetryPolicy) time.Duration {
	var backoff time.Duration

	switch policy.Backoff {
	case "exponential":
		// Exponential backoff: initial * (multiplier ^ attempt)
		backoff = time.Duration(float64(policy.InitialInterval) * math.Pow(policy.Multiplier, float64(attempt)))

	case "linear":
		// Linear backoff: initial + (initial * attempt)
		backoff = policy.InitialInterval * time.Duration(attempt+1)

	case "constant":
		// Constant backoff: always use initial interval
		backoff = policy.InitialInterval

	default:
		// Default to exponential
		backoff = time.Duration(float64(policy.InitialInterval) * math.Pow(2.0, float64(attempt)))
	}

	// Cap at max interval
	if backoff > policy.MaxInterval {
		backoff = policy.MaxInterval
	}

	// Add jitter to prevent thundering herd
	// Jitter is ±25% of the backoff duration
	jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
	backoff = backoff - (backoff / 4) + jitter

	return backoff
}

// IsRetriableError checks if an error is retriable (network errors, timeouts, etc.)
func IsRetriableError(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are not retriable (they indicate cancellation or timeout)
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}

	// Most other errors (network errors, connection refused, etc.) are retriable
	return true
}
