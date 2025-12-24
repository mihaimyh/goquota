package api

import (
	"fmt"
	"net/http"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Config holds configuration for the Usage API handler
type Config struct {
	// Manager is the quota manager instance (required)
	Manager *goquota.Manager

	// GetUserID extracts user ID from HTTP request (required)
	// Similar to middleware/http pattern
	GetUserID func(*http.Request) string

	// ResourceFilter optionally filters which resources to include
	// Applied AFTER resource discovery phase
	// If nil, includes all discovered resources
	ResourceFilter func([]string) []string

	// KnownResources is an optional list of all known resources across all tiers
	// Used to detect orphaned credits when user downgrades to a tier without a resource
	// If nil, only checks current tier config and InitialForeverCredits
	KnownResources []string

	// OnError handles errors (auth, internal, etc.)
	// If nil, uses default error handling
	OnError func(http.ResponseWriter, *http.Request, error)

	// Metrics is optional metrics recorder for Usage API operations
	// If nil, metrics are not recorded
	Metrics goquota.Metrics
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	if c.Manager == nil {
		return fmt.Errorf("manager is required")
	}
	if c.GetUserID == nil {
		return fmt.Errorf("getUserID is required")
	}
	return nil
}

// NewHandler creates a new Usage API handler with the given configuration
func NewHandler(config Config) (*Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &Handler{
		config: config,
	}, nil
}

// Helper functions for common UserID extraction patterns

// FromHeader returns a GetUserID function that extracts user ID from a header
func FromHeader(headerName string) func(*http.Request) string {
	return func(r *http.Request) string {
		return r.Header.Get(headerName)
	}
}

// FromContext returns a GetUserID function that extracts user ID from request context
// Uses the same context key pattern as middleware/http
func FromContext(key interface{}) func(*http.Request) string {
	return func(r *http.Request) string {
		if userID, ok := r.Context().Value(key).(string); ok {
			return userID
		}
		return ""
	}
}
