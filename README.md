# Entriq

A high-performance API gateway built with Go and Gin framework that routes requests to backend microservices with advanced features like retry logic, header manipulation, and request/response logging.

## Features

- ✅ **Path-based Routing**: Both exact and prefix matching
- ✅ **Load Configuration**: YAML-based configuration with environment variable override
- ✅ **Header Manipulation**: Add, remove, and forward headers (including X-Forwarded-*)
- ✅ **Request/Response Logging**: Structured JSON logging with request IDs
- ✅ **Retry Logic**: Exponential backoff with jitter for failed requests
- ✅ **Timeouts**: Configurable per route, service, or gateway level
- ✅ **Connection Pooling**: Efficient HTTP connection reuse
- ✅ **Health Probes**: Kubernetes-ready liveness and readiness endpoints

## Quick Start

### 1. Configure Services

Edit `config/entriq.yaml` to define your backend services:

```yaml
global:
  timeout: 30s
  default_retry:
    max_attempts: 3
    backoff: exponential
    initial_interval: 100ms
    max_interval: 2s

services:
  - name: user-service
    base_url: http://user-service:8081
    routes:
      - path: /users

  - name: transaction-service
    base_url: http://transaction-service:8082
    routes:
      - path: /v1.0/transaction
```

### 2. Run the Gateway

```bash
# Use default config (./config/entriq.yaml)
./entriq

# Or specify custom config path
CONFIG_PATH=/path/to/custom/entriq.yaml ./entriq
```

### 3. Test the Gateway

```bash
# Health checks
curl http://localhost:8080/probes/live
curl http://localhost:8080/probes/ready

# Route to services (based on your config)
curl http://localhost:8080/users
curl http://localhost:8080/v1.0/transaction
```

## Configuration

### Environment Variables

- `CONFIG_PATH`: Path to config file (default: `./config/entriq.yaml`)

### Config Structure

See `config/entriq.example.yaml` for a complete configuration reference with all options documented.

#### Key Sections:

**Global Settings**:
- `timeout`: Timeout for all services
- `default_retry`: Retry policy (max attempts, backoff strategy)
- `connection_pool`: HTTP connection pool settings

**Headers**:
- `add`: Headers to add to all requests
- `forward`: Headers to forward from client
- `remove`: Headers to remove
- `x_forwarded_headers`: Auto-add X-Forwarded-* headers

**Services**:
- `name`: Service identifier
- `base_url`: Backend service URL
- `timeout`: Service-specific timeout (optional)
- `retry`: Service-specific retry policy (optional)
- `routes`: List of routing rules

**Routes**:
- `path`: URL path to match
- `method`: HTTP methods (optional, defaults to all)
- `match_type`: "exact" or "prefix"
- `strip_path`: Remove matched prefix before forwarding
- `timeout`: Route-specific timeout (optional)

**Logging**:
- `enabled`: Enable/disable logging
- `level`: Log level (debug, info, warn, error)
- `log_body`: Include request/response bodies
- `max_body_size`: Max body size to log

## Architecture

```
HTTP Request
    ↓
Gin Middleware Stack (Logger, Recovery, CORS)
    ↓
Gateway Router (match route to service)
    ↓
Header Manipulator (add/remove/modify headers)
    ↓
Proxy Logger (log request start)
    ↓
HTTP Reverse Proxy (forward with timeout)
    ↓
Proxy Logger (log response)
    ↓
HTTP Response
```

## Routing Priority

1. **Kubernetes Probes**: `/probes/live`, `/probes/ready` (not proxied)
2. **Exact Matches**: Highest priority
3. **Prefix Matches**: Longest prefix wins
4. **404 Not Found**: No match found

## Example Requests

### With api.evinta.net Domain

```bash
# Routes to user-service
curl https://api.evinta.net/users
curl https://api.evinta.net/users/123

# Routes to transaction-service
curl https://api.evinta.net/v1.0/transaction
```

## Development

### Build

```bash
go build -o entriq .
```

### Run with Make

```bash
make run
```

### Project Structure

```
entriq/
├── config/                      # Configuration files
│   ├── entriq.yaml              # Active config
│   └── entriq.example.yaml      # Example with docs
├── internal/
│   ├── config/                  # Config loading & validation
│   ├── gateway/                 # Router & proxy logic
│   ├── middleware/              # Header & logging middleware
│   └── retry/                   # Retry logic
├── middlewares/                 # CORS middleware
├── app.go                       # Main application
├── routes.go                    # Route registration
└── go.mod                       # Dependencies
```

## Dependencies

- `github.com/gin-gonic/gin` - Web framework
- `github.com/goccy/go-yaml` - YAML parsing
- `github.com/google/uuid` - Request ID generation

## Production Deployment

### Docker

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o entriq .

FROM alpine:latest
COPY --from=builder /app/entriq /entriq
COPY --from=builder /app/config /config
EXPOSE 8080
CMD ["/entriq"]
```

### Kubernetes

Mount config as ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: entriq-config
data:
  entriq.yaml: |
    # Your config here

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: entriq
spec:
  template:
    spec:
      containers:
      - name: gateway
        image: ghcr.io/entriq/entriq
        ports:
        - containerPort: 8080
        volumeMounts:
        - name: config
          mountPath: /app/config
      volumes:
      - name: config
        configMap:
          name: entriq-config
```
