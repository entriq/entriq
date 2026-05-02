package gateway

import (
	"errors"
	"fmt"
	"entriq/internal/config"
	"sort"
	"strings"
)

var (
	// ErrNoRouteMatch is returned when no route matches the request
	ErrNoRouteMatch = errors.New("no route matches request")
)

// RouteMatch represents a successful route match
type RouteMatch struct {
	Service *config.Service
	Route   *config.Route
}

// Router matches incoming requests to configured routes
type Router struct {
	// Map of exact path matches: path -> method -> RouteMatch
	exactRoutes map[string]map[string]*RouteMatch

	// Slice of prefix routes, sorted by path length (longest first)
	prefixRoutes []prefixRoute
}

// prefixRoute represents a prefix-based route with its match data
type prefixRoute struct {
	path  string
	match *RouteMatch
}

// NewRouter creates a new router from the gateway configuration
func NewRouter(cfg *config.GatewayConfig) *Router {
	router := &Router{
		exactRoutes:  make(map[string]map[string]*RouteMatch),
		prefixRoutes: make([]prefixRoute, 0),
	}

	// Build routing tables from config
	for i := range cfg.Services {
		service := &cfg.Services[i]

		for j := range service.Routes {
			route := &service.Routes[j]
			match := &RouteMatch{
				Service: service,
				Route:   route,
			}

			if route.MatchType == "exact" {
				router.addExactRoute(route.Path, route.Methods, match)
			} else {
				router.addPrefixRoute(route.Path, route.Methods, match)
			}
		}
	}

	// Sort prefix routes by path length (longest first) for proper matching
	sort.Slice(router.prefixRoutes, func(i, j int) bool {
		return len(router.prefixRoutes[i].path) > len(router.prefixRoutes[j].path)
	})

	return router
}

// addExactRoute adds an exact match route to the router
func (r *Router) addExactRoute(path string, methods []string, match *RouteMatch) {
	if r.exactRoutes[path] == nil {
		r.exactRoutes[path] = make(map[string]*RouteMatch)
	}

	// Add route for each specified method
	for _, method := range methods {
		r.exactRoutes[path][method] = match
	}
}

// addPrefixRoute adds a prefix match route to the router
func (r *Router) addPrefixRoute(path string, methods []string, match *RouteMatch) {
	// For prefix routes, we store each method separately
	for range methods {
		// Create a new match for each method to allow independent matching
		methodMatch := &RouteMatch{
			Service: match.Service,
			Route:   match.Route,
		}

		r.prefixRoutes = append(r.prefixRoutes, prefixRoute{
			path:  path,
			match: methodMatch,
		})
	}
}

// Match finds a matching route for the given HTTP method and path
// Priority: exact matches first, then longest prefix match
func (r *Router) Match(method, path string) (*RouteMatch, error) {
	// Normalize path (ensure it starts with /)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Try exact match first
	if methodMap, ok := r.exactRoutes[path]; ok {
		if match, ok := methodMap[method]; ok {
			return match, nil
		}
	}

	// Try prefix matches (already sorted by length, longest first)
	for _, pr := range r.prefixRoutes {
		if strings.HasPrefix(path, pr.path) {
			// Check if method matches
			if r.methodMatches(method, pr.match.Route.Methods) {
				return pr.match, nil
			}
		}
	}

	return nil, ErrNoRouteMatch
}

// methodMatches checks if the request method matches any of the allowed methods
func (r *Router) methodMatches(requestMethod string, allowedMethods []string) bool {
	for _, method := range allowedMethods {
		if method == requestMethod {
			return true
		}
	}
	return false
}

// GetStats returns statistics about the router
func (r *Router) GetStats() map[string]interface{} {
	exactCount := 0
	for _, methodMap := range r.exactRoutes {
		exactCount += len(methodMap)
	}

	return map[string]interface{}{
		"exact_routes":  exactCount,
		"prefix_routes": len(r.prefixRoutes),
		"total_routes":  exactCount + len(r.prefixRoutes),
	}
}

// DebugRoutes returns a string representation of all routes (for debugging)
func (r *Router) DebugRoutes() string {
	var sb strings.Builder

	sb.WriteString("Exact Routes:\n")
	for path, methodMap := range r.exactRoutes {
		for method := range methodMap {
			sb.WriteString(fmt.Sprintf("  %s %s (exact)\n", method, path))
		}
	}

	sb.WriteString("\nPrefix Routes:\n")
	for _, pr := range r.prefixRoutes {
		for _, method := range pr.match.Route.Methods {
			sb.WriteString(fmt.Sprintf("  %s %s (prefix)\n", method, pr.path))
		}
	}

	return sb.String()
}
