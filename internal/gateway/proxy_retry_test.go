package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"entriq/internal/config"
)

func TestServeHTTPRetriesOnServerError(t *testing.T) {
	var attempts atomic.Int32

	// Backend returns 502 on the first two attempts, then 200.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("bad gateway"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	cfg := &config.GatewayConfig{
		Global: config.GlobalSettings{
			DefaultTimeout: 30 * time.Second,
			DefaultRetry: config.RetryPolicy{
				MaxAttempts:     3,
				Backoff:         "constant",
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     50 * time.Millisecond,
			},
		},
		Services: []config.Service{
			{
				Name: "retry-test",
				URL:  backend.URL,
				Routes: []config.Route{
					{
						Path:      "/test",
						Methods:   []string{"GET"},
						MatchType: "prefix",
					},
				},
			},
		},
	}

	transport := CreateTransport(cfg.Global.ConnectionPool)
	router := NewRouter(cfg)

	match, err := router.Match("GET", "/test")
	if err != nil {
		t.Fatalf("route match failed: %v", err)
	}

	handler := NewProxyHandler(match, cfg, transport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestServeHTTPNoRetryForPostRequests(t *testing.T) {
	var attempts atomic.Int32

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer backend.Close()

	cfg := &config.GatewayConfig{
		Global: config.GlobalSettings{
			DefaultTimeout: 30 * time.Second,
			DefaultRetry: config.RetryPolicy{
				MaxAttempts:     3,
				Backoff:         "constant",
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     50 * time.Millisecond,
			},
		},
		Services: []config.Service{
			{
				Name: "retry-test",
				URL:  backend.URL,
				Routes: []config.Route{
					{
						Path:      "/test",
						Methods:   []string{"POST"},
						MatchType: "prefix",
					},
				},
			},
		},
	}

	transport := CreateTransport(cfg.Global.ConnectionPool)
	router := NewRouter(cfg)

	match, err := router.Match("POST", "/test")
	if err != nil {
		t.Fatalf("route match failed: %v", err)
	}

	handler := NewProxyHandler(match, cfg, transport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/test", nil)
	handler.ServeHTTP(rec, req)

	// POST is not idempotent — retry logic won't retry, returns after 1st attempt
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (POST should not be retried)", got)
	}
}

func TestServeHTTPNoRetryWhenMaxAttemptsIsOne(t *testing.T) {
	var attempts atomic.Int32

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer backend.Close()

	cfg := &config.GatewayConfig{
		Global: config.GlobalSettings{
			DefaultTimeout: 30 * time.Second,
			DefaultRetry: config.RetryPolicy{
				MaxAttempts: 1,
			},
		},
		Services: []config.Service{
			{
				Name: "no-retry-test",
				URL:  backend.URL,
				Routes: []config.Route{
					{
						Path:      "/test",
						Methods:   []string{"GET"},
						MatchType: "prefix",
					},
				},
			},
		},
	}

	transport := CreateTransport(cfg.Global.ConnectionPool)
	router := NewRouter(cfg)

	match, err := router.Match("GET", "/test")
	if err != nil {
		t.Fatalf("route match failed: %v", err)
	}

	handler := NewProxyHandler(match, cfg, transport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	// MaxAttempts=1 takes the direct (non-retry) path
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}
