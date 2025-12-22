// Package gin provides Gin middleware for quota enforcement
package gin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	gongin "github.com/gin-gonic/gin"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

// UserIDExtractor extracts the user ID from a Gin context
// Return empty string if user is not authenticated
type UserIDExtractor func(c *gongin.Context) string

// ResourceExtractor extracts the resource name from a Gin context
// For example: "api_calls", "audio_seconds", "tts_characters"
type ResourceExtractor func(c *gongin.Context) string

// AmountExtractor calculates the quota amount to consume from the Gin context
// For example: count API calls as 1, or calculate TTS characters from request body
type AmountExtractor func(c *gongin.Context) (int, error)

// IdempotencyKeyExtractor extracts the idempotency key from a Gin context
// Return empty string if no idempotency key is available
type IdempotencyKeyExtractor func(c *gongin.Context) string

// Config holds middleware configuration
type Config struct {
	// Manager is the quota manager instance
	Manager *goquota.Manager

	// GetUserID extracts user ID from context (required)
	GetUserID UserIDExtractor

	// GetResource extracts resource name from context (required)
	GetResource ResourceExtractor

	// GetAmount calculates quota amount from context (required)
	GetAmount AmountExtractor

	// GetIdempotencyKey extracts idempotency key from context (optional)
	// If nil, defaults to extracting from X-Request-ID header
	GetIdempotencyKey IdempotencyKeyExtractor

	// PeriodType specifies the quota period (daily or monthly)
	// Default: PeriodTypeMonthly
	PeriodType goquota.PeriodType

	// QuotaExceededStatusCode is the HTTP status code to return when quota is exceeded
	// Default: 429 (Too Many Requests)
	QuotaExceededStatusCode int

	// OnRateLimitExceeded is called when rate limit is exceeded
	// If nil, uses default response: 429 JSON with rate limit headers
	OnRateLimitExceeded func(c *gongin.Context, retryAfter time.Duration, info *goquota.RateLimitInfo)

	// OnQuotaExceeded is called when quota is exceeded
	// If nil, uses default response: QuotaExceededStatusCode JSON with usage info
	OnQuotaExceeded func(c *gongin.Context, usage *goquota.Usage)

	// OnUnauthorized is called when user is not authenticated
	// If nil, returns 401 Unauthorized
	OnUnauthorized func(c *gongin.Context)

	// OnError is called when an internal error occurs
	// If nil, returns 500 Internal Server Error
	OnError func(c *gongin.Context, err error)

	// OnWarning is called when a soft limit warning threshold is crossed.
	// Use this to add custom headers or log warnings.
	// If nil, a default X-Quota-Warning header is added.
	//
	// IMPORTANT: This function should ONLY set headers (c.Header).
	// Do NOT write to the response body (c.JSON, c.String, etc.) or status code,
	// as this will interfere with the actual request handler that runs after
	// the middleware completes.
	OnWarning func(c *gongin.Context, usage *goquota.Usage, threshold float64)
}

// Middleware creates a Gin middleware that enforces quota limits
//
//nolint:gocyclo // Complex function handles rate limiting, quota consumption, and multiple error cases
func Middleware(cfg Config) gongin.HandlerFunc {
	// Validate required configuration at startup (fail fast)
	if cfg.Manager == nil {
		panic("goquota/gin: Config.Manager is required")
	}
	if cfg.GetUserID == nil {
		panic("goquota/gin: Config.GetUserID is required")
	}
	if cfg.GetResource == nil {
		panic("goquota/gin: Config.GetResource is required")
	}
	if cfg.GetAmount == nil {
		panic("goquota/gin: Config.GetAmount is required")
	}

	// Set defaults
	if cfg.PeriodType == "" {
		cfg.PeriodType = goquota.PeriodTypeMonthly
	}
	if cfg.QuotaExceededStatusCode == 0 {
		cfg.QuotaExceededStatusCode = http.StatusTooManyRequests
	}
	if cfg.GetIdempotencyKey == nil {
		cfg.GetIdempotencyKey = IdempotencyKeyFromHeader("X-Request-ID")
	}

	return func(c *gongin.Context) {
		// Extract user ID
		userID := cfg.GetUserID(c)
		if userID == "" {
			if cfg.OnUnauthorized != nil {
				cfg.OnUnauthorized(c)
			} else {
				defaultUnauthorized(c)
			}
			c.Abort()
			return
		}

		// Extract resource and amount
		resource := cfg.GetResource(c)
		amount, err := cfg.GetAmount(c)
		if err != nil || amount <= 0 {
			if err == nil && amount <= 0 {
				err = fmt.Errorf("invalid amount: %d", amount)
			}
			if cfg.OnError != nil {
				cfg.OnError(c, err)
			} else {
				c.JSON(http.StatusBadRequest, gongin.H{"error": "Bad Request"})
			}
			c.Abort()
			return
		}

		// Extract idempotency key if available
		idempotencyKey := cfg.GetIdempotencyKey(c)

		// Check and consume quota
		ctx := c.Request.Context()

		// Set up warning handler if needed
		if cfg.OnWarning != nil {
			ctx = goquota.WithWarningHandler(ctx, &warningHandler{
				c: c,
				f: cfg.OnWarning,
			})
		} else {
			// Default warning behavior: add headers
			ctx = goquota.WithWarningHandler(ctx, &warningHandler{
				c: c,
				f: defaultWarningHandler,
			})
		}

		// Prepare consume options
		opts := []goquota.ConsumeOption{}
		if idempotencyKey != "" {
			opts = append(opts, goquota.WithIdempotencyKey(idempotencyKey))
		}

		// Consume quota (rate limit is checked internally by Manager.Consume)
		_, err = cfg.Manager.Consume(ctx, userID, resource, amount, cfg.PeriodType, opts...)
		if err != nil {
			// Check for rate limit exceeded error
			var rateLimitErr *goquota.RateLimitExceededError
			if errors.As(err, &rateLimitErr) {
				// Add rate limit headers
				if rateLimitErr.Info != nil {
					c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", rateLimitErr.Info.Limit))
					c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", rateLimitErr.Info.Remaining))
					c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", rateLimitErr.Info.ResetTime.Unix()))
				}
				if rateLimitErr.RetryAfter > 0 {
					c.Header("Retry-After", fmt.Sprintf("%.0f", rateLimitErr.RetryAfter.Seconds()))
				}

				if cfg.OnRateLimitExceeded != nil {
					cfg.OnRateLimitExceeded(c, rateLimitErr.RetryAfter, rateLimitErr.Info)
				} else {
					defaultRateLimitExceeded(c, rateLimitErr.RetryAfter)
				}
				c.Abort()
				return
			}

			if err == goquota.ErrQuotaExceeded {
				// Get current usage for error response
				usage, usageErr := cfg.Manager.GetQuota(ctx, userID, resource, cfg.PeriodType)
				if usageErr == nil && cfg.OnQuotaExceeded != nil {
					cfg.OnQuotaExceeded(c, usage)
				} else {
					defaultQuotaExceeded(c, usage, cfg.QuotaExceededStatusCode)
				}
				c.Abort()
				return
			}

			// Other errors (storage, etc.)
			if cfg.OnError != nil {
				cfg.OnError(c, err)
			} else {
				defaultError(c, err)
			}
			c.Abort()
			return
		}

		// Quota consumed successfully - add rate limit headers if available
		// This allows clients to know their remaining rate limit before hitting it
		addRateLimitHeadersOnSuccess(ctx, c, cfg.Manager, userID, resource)

		// Proceed to handler
		c.Next()
	}
}

// addRateLimitHeadersOnSuccess attempts to add rate limit headers on successful requests.
// This follows industry standards (GitHub, Stripe) where rate limit headers are included
// on all responses, not just errors.
// addRateLimitHeadersOnSuccess attempts to add rate limit headers on successful requests.
// This follows industry standards (GitHub, Stripe) where rate limit headers are included
// on all responses, not just errors.
//
// Note: Currently, this is a no-op because Manager doesn't expose rate limit info on success.
// The Manager.Consume method checks rate limits internally but only returns the info when
// rate limits are exceeded. To fully implement this feature, Manager would need to expose
// a method like GetRateLimitInfo(userID, resource) that returns current rate limit status
// without consuming tokens.
//
// For now, rate limit headers are only added when rate limits are exceeded (in error cases).
func addRateLimitHeadersOnSuccess(_ context.Context, _ *gongin.Context, _ *goquota.Manager, _, _ string) {
	// This is intentionally a no-op until Manager exposes rate limit info API
	// When Manager.GetRateLimitInfo() is available, this function should:
	// 1. Call manager.GetRateLimitInfo(ctx, userID, resource) 
	// 2. If info is available, add headers:
	//    c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", info.Limit))
	//    c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", info.Remaining))
	//    c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", info.ResetTime.Unix()))
}

type warningHandler struct {
	c *gongin.Context
	f func(*gongin.Context, *goquota.Usage, float64)
}

func (h *warningHandler) OnWarning(_ context.Context, usage *goquota.Usage, threshold float64) {
	if h.f != nil {
		h.f(h.c, usage, threshold)
	}
}

// Default error handlers

func defaultUnauthorized(c *gongin.Context) {
	c.JSON(http.StatusUnauthorized, gongin.H{"error": "Unauthorized"})
}

func defaultRateLimitExceeded(c *gongin.Context, retryAfter time.Duration) {
	c.JSON(http.StatusTooManyRequests, gongin.H{
		"error":       "Rate limit exceeded",
		"retry_after": retryAfter.Seconds(),
	})
}

func defaultQuotaExceeded(c *gongin.Context, usage *goquota.Usage, statusCode int) {
	if usage != nil {
		c.JSON(statusCode, gongin.H{
			"error": "Quota exceeded",
			"used":  usage.Used,
			"limit": usage.Limit,
		})
	} else {
		c.JSON(statusCode, gongin.H{"error": "Quota exceeded"})
	}
}

func defaultError(c *gongin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gongin.H{"error": "Internal Server Error"})
}

// DefaultWarningHandler is the default OnWarning implementation.
// It adds X-Quota-Warning-Threshold, X-Quota-Warning-Used, and X-Quota-Warning-Limit headers.
func defaultWarningHandler(c *gongin.Context, usage *goquota.Usage, threshold float64) {
	c.Header("X-Quota-Warning-Threshold", fmt.Sprintf("%.2f", threshold))
	c.Header("X-Quota-Warning-Used", fmt.Sprintf("%d", usage.Used))
	c.Header("X-Quota-Warning-Limit", fmt.Sprintf("%d", usage.Limit))
}

// Convenience extractors for User ID

// FromContext returns a UserIDExtractor that gets user ID from Gin context values
// This is the recommended approach for integrating with auth middleware that sets
// user information via c.Set("UserID", "...") or similar.
//
// Example:
//
//	// In your auth middleware:
//	c.Set("UserID", userID)
//
//	// In quota middleware config:
//	GetUserID: gin.FromContext("UserID")
func FromContext(key string) UserIDExtractor {
	return func(c *gongin.Context) string {
		if val, exists := c.Get(key); exists {
			if str, ok := val.(string); ok {
				return str
			}
		}
		return ""
	}
}

// FromHeader returns a UserIDExtractor that gets user ID from a header
func FromHeader(headerName string) UserIDExtractor {
	return func(c *gongin.Context) string {
		return c.GetHeader(headerName)
	}
}

// FromParam returns a UserIDExtractor that gets user ID from a route parameter
func FromParam(paramName string) UserIDExtractor {
	return func(c *gongin.Context) string {
		return c.Param(paramName)
	}
}

// FromQuery returns a UserIDExtractor that gets user ID from a query parameter
func FromQuery(queryName string) UserIDExtractor {
	return func(c *gongin.Context) string {
		return c.Query(queryName)
	}
}

// Convenience extractors for Resource

// FixedResource returns a ResourceExtractor that always returns a fixed resource name
func FixedResource(resource string) ResourceExtractor {
	return func(*gongin.Context) string {
		return resource
	}
}

// FromRoute returns a ResourceExtractor that extracts resource from the route path
func FromRoute() ResourceExtractor {
	return func(c *gongin.Context) string {
		return c.FullPath()
	}
}

// Convenience extractors for Amount

// FixedAmount returns an AmountExtractor that always returns a fixed amount
func FixedAmount(amount int) AmountExtractor {
	return func(*gongin.Context) (int, error) {
		return amount, nil
	}
}

// DynamicCost returns an AmountExtractor that calculates cost based on a function
func DynamicCost(costFunc func(*gongin.Context) int) AmountExtractor {
	return func(c *gongin.Context) (int, error) {
		return costFunc(c), nil
	}
}

// Convenience extractors for Idempotency Key

// IdempotencyKeyFromHeader returns an IdempotencyKeyExtractor that gets the key from a header
func IdempotencyKeyFromHeader(headerName string) IdempotencyKeyExtractor {
	return func(c *gongin.Context) string {
		return c.GetHeader(headerName)
	}
}

// IdempotencyKeyFromContext returns an IdempotencyKeyExtractor that gets the key from context values
func IdempotencyKeyFromContext(key string) IdempotencyKeyExtractor {
	return func(c *gongin.Context) string {
		if val, exists := c.Get(key); exists {
			if str, ok := val.(string); ok {
				return str
			}
		}
		return ""
	}
}
