// Package fiber provides Fiber middleware for quota enforcement
package fiber

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// UserIDExtractor extracts the user ID from a Fiber context
// Return empty string if user is not authenticated
type UserIDExtractor func(c *fiber.Ctx) string

// ResourceExtractor extracts the resource name from a Fiber context
// For example: "api_calls", "audio_seconds", "tts_characters"
type ResourceExtractor func(c *fiber.Ctx) string

// AmountExtractor calculates the quota amount to consume from the Fiber context
// For example: count API calls as 1, or calculate TTS characters from request body
type AmountExtractor func(c *fiber.Ctx) (int, error)

// IdempotencyKeyExtractor extracts the idempotency key from a Fiber context
// Return empty string if no idempotency key is available
type IdempotencyKeyExtractor func(c *fiber.Ctx) string

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
	OnRateLimitExceeded func(c *fiber.Ctx, retryAfter time.Duration, info *goquota.RateLimitInfo) error

	// OnQuotaExceeded is called when quota is exceeded
	// If nil, uses default response: QuotaExceededStatusCode JSON with usage info
	OnQuotaExceeded func(c *fiber.Ctx, usage *goquota.Usage) error

	// OnUnauthorized is called when user is not authenticated
	// If nil, returns 401 Unauthorized
	OnUnauthorized func(c *fiber.Ctx) error

	// OnError is called when an internal error occurs
	// If nil, returns 500 Internal Server Error
	OnError func(c *fiber.Ctx, err error) error

	// OnWarning is called when a soft limit warning threshold is crossed.
	// Use this to add custom headers or log warnings.
	// If nil, a default X-Quota-Warning header is added.
	//
	// IMPORTANT: This function should ONLY set headers (c.Set).
	// Do NOT write to the response body (c.JSON, c.Send, etc.) or status code,
	// as this will interfere with the actual request handler that runs after
	// the middleware completes.
	OnWarning func(c *fiber.Ctx, usage *goquota.Usage, threshold float64)
}

// Middleware creates a Fiber middleware that enforces quota limits
//
//nolint:gocyclo // Complex function handles rate limiting, quota consumption, and multiple error cases
func Middleware(cfg Config) fiber.Handler {
	// Validate required configuration at startup (fail fast)
	if cfg.Manager == nil {
		panic("goquota/fiber: Config.Manager is required")
	}
	if cfg.GetUserID == nil {
		panic("goquota/fiber: Config.GetUserID is required")
	}
	if cfg.GetResource == nil {
		panic("goquota/fiber: Config.GetResource is required")
	}
	if cfg.GetAmount == nil {
		panic("goquota/fiber: Config.GetAmount is required")
	}

	// Set defaults
	if cfg.PeriodType == "" {
		cfg.PeriodType = goquota.PeriodTypeMonthly
	}
	if cfg.QuotaExceededStatusCode == 0 {
		cfg.QuotaExceededStatusCode = fiber.StatusTooManyRequests
	}
	if cfg.GetIdempotencyKey == nil {
		cfg.GetIdempotencyKey = IdempotencyKeyFromHeader("X-Request-ID")
	}

	return func(c *fiber.Ctx) error {
		// Extract user ID
		userID := cfg.GetUserID(c)
		if userID == "" {
			if cfg.OnUnauthorized != nil {
				return cfg.OnUnauthorized(c)
			}
			return defaultUnauthorized(c)
		}

		// Extract resource and amount
		resource := cfg.GetResource(c)
		amount, err := cfg.GetAmount(c)
		if err != nil || amount <= 0 {
			if err == nil && amount <= 0 {
				err = fmt.Errorf("invalid amount: %d", amount)
			}
			if cfg.OnError != nil {
				return cfg.OnError(c, err)
			}
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Bad Request"})
		}

		// Extract idempotency key if available
		idempotencyKey := cfg.GetIdempotencyKey(c)

		// Check and consume quota
		// CRITICAL: Fiber uses fasthttp, so we must use c.UserContext() to get context.Context
		ctx := c.UserContext()

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
					c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", rateLimitErr.Info.Limit))
					c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", rateLimitErr.Info.Remaining))
					c.Set("X-RateLimit-Reset", fmt.Sprintf("%d", rateLimitErr.Info.ResetTime.Unix()))
				}
				if rateLimitErr.RetryAfter > 0 {
					c.Set("Retry-After", fmt.Sprintf("%.0f", rateLimitErr.RetryAfter.Seconds()))
				}

				if cfg.OnRateLimitExceeded != nil {
					return cfg.OnRateLimitExceeded(c, rateLimitErr.RetryAfter, rateLimitErr.Info)
				}
				return defaultRateLimitExceeded(c, rateLimitErr.RetryAfter)
			}

			if err == goquota.ErrQuotaExceeded {
				// Get current usage for error response
				usage, usageErr := cfg.Manager.GetQuota(ctx, userID, resource, cfg.PeriodType)
				if usageErr == nil && cfg.OnQuotaExceeded != nil {
					return cfg.OnQuotaExceeded(c, usage)
				}
				return defaultQuotaExceeded(c, usage, cfg.QuotaExceededStatusCode)
			}

			// Other errors (storage, etc.)
			if cfg.OnError != nil {
				return cfg.OnError(c, err)
			}
			return defaultError(c, err)
		}

		// Quota consumed successfully - add rate limit headers if available
		// This allows clients to know their remaining rate limit before hitting it
		addRateLimitHeadersOnSuccess(ctx, c, cfg.Manager, userID, resource)

		// Proceed to handler
		return c.Next()
	}
}

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
func addRateLimitHeadersOnSuccess(_ context.Context, _ *fiber.Ctx, _ *goquota.Manager, _, _ string) {
	// This is intentionally a no-op until Manager exposes rate limit info API
	// When Manager.GetRateLimitInfo() is available, this function should:
	// 1. Call manager.GetRateLimitInfo(ctx, userID, resource)
	// 2. If info is available, add headers:
	//    c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", info.Limit))
	//    c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", info.Remaining))
	//    c.Set("X-RateLimit-Reset", fmt.Sprintf("%d", info.ResetTime.Unix()))
}

type warningHandler struct {
	c *fiber.Ctx
	f func(*fiber.Ctx, *goquota.Usage, float64)
}

func (h *warningHandler) OnWarning(_ context.Context, usage *goquota.Usage, threshold float64) {
	if h.f != nil {
		h.f(h.c, usage, threshold)
	}
}

// Default error handlers

func defaultUnauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
}

func defaultRateLimitExceeded(c *fiber.Ctx, retryAfter time.Duration) error {
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":       "Rate limit exceeded",
		"retry_after": retryAfter.Seconds(),
	})
}

func defaultQuotaExceeded(c *fiber.Ctx, usage *goquota.Usage, statusCode int) error {
	if usage != nil {
		return c.Status(statusCode).JSON(fiber.Map{
			"error": "Quota exceeded",
			"used":  usage.Used,
			"limit": usage.Limit,
		})
	}
	return c.Status(statusCode).JSON(fiber.Map{"error": "Quota exceeded"})
}

func defaultError(c *fiber.Ctx, _ error) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Internal Server Error"})
}

// DefaultWarningHandler is the default OnWarning implementation.
// It adds X-Quota-Warning-Threshold, X-Quota-Warning-Used, and X-Quota-Warning-Limit headers.
func defaultWarningHandler(c *fiber.Ctx, usage *goquota.Usage, threshold float64) {
	c.Set("X-Quota-Warning-Threshold", fmt.Sprintf("%.2f", threshold))
	c.Set("X-Quota-Warning-Used", fmt.Sprintf("%d", usage.Used))
	c.Set("X-Quota-Warning-Limit", fmt.Sprintf("%d", usage.Limit))
}

// Convenience extractors for User ID

// FromContext returns a UserIDExtractor that gets user ID from Fiber context values (Locals)
// This is the recommended approach for integrating with auth middleware that sets
// user information via c.Locals("UserID", "...") or similar.
//
// Example:
//
//	// In your auth middleware:
//	c.Locals("UserID", userID)
//
//	// In quota middleware config:
//	GetUserID: fiber.FromContext("UserID")
func FromContext(key string) UserIDExtractor {
	return func(c *fiber.Ctx) string {
		if val := c.Locals(key); val != nil {
			if str, ok := val.(string); ok {
				return str
			}
		}
		return ""
	}
}

// FromHeader returns a UserIDExtractor that gets user ID from a header
// Fiber v2 uses c.Get() for headers (not c.GetHeader())
func FromHeader(headerName string) UserIDExtractor {
	return func(c *fiber.Ctx) string {
		return c.Get(headerName)
	}
}

// FromParam returns a UserIDExtractor that gets user ID from a route parameter
func FromParam(paramName string) UserIDExtractor {
	return func(c *fiber.Ctx) string {
		return c.Params(paramName)
	}
}

// FromQuery returns a UserIDExtractor that gets user ID from a query parameter
func FromQuery(queryName string) UserIDExtractor {
	return func(c *fiber.Ctx) string {
		return c.Query(queryName)
	}
}

// Convenience extractors for Resource

// FixedResource returns a ResourceExtractor that always returns a fixed resource name
func FixedResource(resource string) ResourceExtractor {
	return func(*fiber.Ctx) string {
		return resource
	}
}

// FromRoute returns a ResourceExtractor that extracts resource from the route path
func FromRoute() ResourceExtractor {
	return func(c *fiber.Ctx) string {
		return c.Route().Path
	}
}

// Convenience extractors for Amount

// FixedAmount returns an AmountExtractor that always returns a fixed amount
func FixedAmount(amount int) AmountExtractor {
	return func(*fiber.Ctx) (int, error) {
		return amount, nil
	}
}

// DynamicCost returns an AmountExtractor that calculates cost based on a function
func DynamicCost(costFunc func(*fiber.Ctx) int) AmountExtractor {
	return func(c *fiber.Ctx) (int, error) {
		return costFunc(c), nil
	}
}

// Convenience extractors for Idempotency Key

// IdempotencyKeyFromHeader returns an IdempotencyKeyExtractor that gets the key from a header
func IdempotencyKeyFromHeader(headerName string) IdempotencyKeyExtractor {
	return func(c *fiber.Ctx) string {
		return c.Get(headerName)
	}
}

// IdempotencyKeyFromContext returns an IdempotencyKeyExtractor that gets the key from context values (Locals)
func IdempotencyKeyFromContext(key string) IdempotencyKeyExtractor {
	return func(c *fiber.Ctx) string {
		if val := c.Locals(key); val != nil {
			if str, ok := val.(string); ok {
				return str
			}
		}
		return ""
	}
}
