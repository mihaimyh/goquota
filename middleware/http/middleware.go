// Package http provides HTTP middleware for quota enforcement
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// UserIDExtractor extracts the user ID from an HTTP request
// Return empty string if user is not authenticated
type UserIDExtractor func(r *http.Request) string

// ResourceExtractor extracts the resource name from an HTTP request
// For example: "api_calls", "audio_seconds", "tts_characters"
type ResourceExtractor func(r *http.Request) string

// AmountExtractor calculates the quota amount to consume from the request
// For example: count API calls as 1, or calculate TTS characters from request body
type AmountExtractor func(r *http.Request) (int, error)

// Config holds middleware configuration
type Config struct {
	// Manager is the quota manager instance
	Manager *goquota.Manager

	// GetUserID extracts user ID from request (required)
	GetUserID UserIDExtractor

	// GetResource extracts resource name from request (required)
	GetResource ResourceExtractor

	// GetAmount calculates quota amount from request (required)
	GetAmount AmountExtractor

	// PeriodType specifies the quota period (daily or monthly)
	// Default: PeriodTypeMonthly
	PeriodType goquota.PeriodType

	// OnQuotaExceeded is called when quota is exceeded
	// If nil, returns 429 Too Many Requests
	OnQuotaExceeded func(w http.ResponseWriter, r *http.Request, usage *goquota.Usage)

	// OnUnauthorized is called when user is not authenticated
	// If nil, returns 401 Unauthorized
	OnUnauthorized func(w http.ResponseWriter, r *http.Request)

	// OnError is called when an internal error occurs
	// If nil, returns 500 Internal Server Error
	OnError func(w http.ResponseWriter, r *http.Request, err error)

	// OnWarning is called when a soft limit warning threshold is crossed.
	// Use this to add custom headers or log warnings.
	// If nil, a default X-Quota-Warning header is added.
	OnWarning func(w http.ResponseWriter, r *http.Request, usage *goquota.Usage, threshold float64)
}

// Middleware creates an HTTP middleware that enforces quota limits
//
//nolint:gocyclo // Complex function handles rate limiting, quota consumption, and multiple error cases
func Middleware(config *Config) func(http.Handler) http.Handler {
	// Set defaults
	if config.PeriodType == "" {
		config.PeriodType = goquota.PeriodTypeMonthly
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract user ID
			userID := config.GetUserID(r)
			if userID == "" {
				if config.OnUnauthorized != nil {
					config.OnUnauthorized(w, r)
				} else {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
				}
				return
			}

			// Extract resource and amount
			resource := config.GetResource(r)
			amount, err := config.GetAmount(r)
			if err != nil || amount <= 0 {
				if err == nil && amount <= 0 {
					err = fmt.Errorf("invalid amount: %d", amount)
				}
				if config.OnError != nil {
					config.OnError(w, r, err)
				} else {
					http.Error(w, "Bad Request", http.StatusBadRequest)
				}
				return
			}

			// Check and consume quota
			ctx := r.Context()

			// Set up warning handler if needed
			if config.OnWarning != nil {
				ctx = goquota.WithWarningHandler(ctx, &warningHandler{
					w: w,
					r: r,
					f: config.OnWarning,
				})
			} else {
				// Default warning behavior: add headers
				ctx = goquota.WithWarningHandler(ctx, &warningHandler{
					w: w,
					r: r,
					f: DefaultWarningHandler,
				})
			}

			_, err = config.Manager.Consume(ctx, userID, resource, amount, config.PeriodType)
			if err != nil {
				// Check for rate limit exceeded error
				var rateLimitErr *goquota.RateLimitExceededError
				if errors.As(err, &rateLimitErr) {
					// Add rate limit headers
					if rateLimitErr.Info != nil {
						w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", rateLimitErr.Info.Limit))
						w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", rateLimitErr.Info.Remaining))
						w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", rateLimitErr.Info.ResetTime.Unix()))
					}
					if rateLimitErr.RetryAfter > 0 {
						w.Header().Set("Retry-After", fmt.Sprintf("%.0f", rateLimitErr.RetryAfter.Seconds()))
					}
					http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
					return
				}

				if err == goquota.ErrQuotaExceeded {
					// Get current usage for error response
					usage, err := config.Manager.GetQuota(ctx, userID, resource, config.PeriodType)
					if err == nil && config.OnQuotaExceeded != nil {
						config.OnQuotaExceeded(w, r, usage)
					} else {
						msg := fmt.Sprintf("Quota exceeded: %d/%d %s used", usage.Used, usage.Limit, resource)
						http.Error(w, msg, http.StatusTooManyRequests)
					}
				} else {
					if config.OnError != nil {
						config.OnError(w, r, err)
					} else {
						http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					}
				}
				return
			}

			// Add rate limit headers on success (if available from rate limit check)
			// Note: We could enhance this to get rate limit info even on success

			// Quota consumed successfully, proceed to handler
			next.ServeHTTP(w, r)
		})
	}
}

type warningHandler struct {
	w http.ResponseWriter
	r *http.Request
	f func(http.ResponseWriter, *http.Request, *goquota.Usage, float64)
}

func (h *warningHandler) OnWarning(_ context.Context, usage *goquota.Usage, threshold float64) {
	if h.f != nil {
		h.f(h.w, h.r, usage, threshold)
	}
}

// DefaultWarningHandler is the default OnWarning implementation.
// It adds X-Quota-Warning-Threshold and X-Quota-Warning-Used headers.
func DefaultWarningHandler(w http.ResponseWriter, _ *http.Request, usage *goquota.Usage, threshold float64) {
	w.Header().Add("X-Quota-Warning-Threshold", fmt.Sprintf("%.2f", threshold))
	w.Header().Add("X-Quota-Warning-Used", fmt.Sprintf("%d", usage.Used))
	w.Header().Add("X-Quota-Warning-Limit", fmt.Sprintf("%d", usage.Limit))
}

// HandlerFunc creates an HTTP middleware that enforces quota limits (HandlerFunc version)
func HandlerFunc(config *Config) func(http.HandlerFunc) http.HandlerFunc {
	middleware := Middleware(config)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			middleware(next).ServeHTTP(w, r)
		}
	}
}

// Common extractors for convenience

// FixedAmount returns an AmountExtractor that always returns a fixed amount
func FixedAmount(amount int) AmountExtractor {
	return func(_ *http.Request) (int, error) {
		return amount, nil
	}
}

// BodyLength returns an AmountExtractor that uses the request body length
// Useful for tracking bytes consumed
func BodyLength() AmountExtractor {
	return func(r *http.Request) (int, error) {
		if r.Body == nil {
			return 0, nil
		}

		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return 0, err
		}

		// Restore body for next handler
		r.Body = io.NopCloser(io.Reader(newBytesReader(body)))

		return len(body), nil
	}
}

// bytesReader is a simple bytes reader
type bytesReader struct {
	data []byte
	pos  int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data}
}

func (br *bytesReader) Read(p []byte) (n int, err error) {
	if br.pos >= len(br.data) {
		return 0, io.EOF
	}
	n = copy(p, br.data[br.pos:])
	br.pos += n
	return n, nil
}

// ContextKey is a type for context keys
type ContextKey string

const (
	// UserIDKey is the context key for user ID
	UserIDKey ContextKey = "quota:userID"
)

// FromContext returns an UserIDExtractor that gets user ID from request context
func FromContext(key ContextKey) UserIDExtractor {
	return func(r *http.Request) string {
		if userID, ok := r.Context().Value(key).(string); ok {
			return userID
		}
		return ""
	}
}

// FromHeader returns an UserIDExtractor that gets user ID from a header
func FromHeader(headerName string) UserIDExtractor {
	return func(r *http.Request) string {
		return r.Header.Get(headerName)
	}
}

// FixedResource returns a ResourceExtractor that always returns a fixed resource name
func FixedResource(resource string) ResourceExtractor {
	return func(_ *http.Request) string {
		return resource
	}
}

// WithUserID adds user ID to request context
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// FromBody returns an AmountExtractor that reads the request body and applies the cost function.
// It restores the body so it can be read again by subsequent handlers.
func FromBody(costFunc func([]byte) (int, error)) AmountExtractor {
	return func(r *http.Request) (int, error) {
		if r.Body == nil {
			return 0, nil
		}

		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return 0, err
		}

		// Restore body for next handler
		r.Body = io.NopCloser(bytes.NewBuffer(body))

		return costFunc(body)
	}
}

// JSONIntField returns an AmountExtractor that extracts an integer amount from a JSON field in the request body.
func JSONIntField(field string) AmountExtractor {
	return FromBody(func(body []byte) (int, error) {
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return 0, fmt.Errorf("failed to parse JSON: %w", err)
		}

		val, ok := data[field]
		if !ok {
			return 0, fmt.Errorf("field %q not found", field)
		}

		switch v := val.(type) {
		case float64:
			return int(v), nil
		case int:
			return v, nil
		default:
			return 0, fmt.Errorf("field %q is not a number: %T", field, v)
		}
	})
}

// JSONDurationMillisToSeconds returns an AmountExtractor that extracts a duration in milliseconds from a JSON field
// and converts it to seconds (rounding up).
func JSONDurationMillisToSeconds(field string) AmountExtractor {
	return FromBody(func(body []byte) (int, error) {
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return 0, fmt.Errorf("failed to parse JSON: %w", err)
		}

		val, ok := data[field]
		if !ok {
			return 0, fmt.Errorf("field %q not found", field)
		}

		var millis float64
		switch v := val.(type) {
		case float64:
			millis = v
		case int:
			millis = float64(v)
		default:
			return 0, fmt.Errorf("field %q is not a number: %T", field, v)
		}

		return int(math.Ceil(millis / 1000.0)), nil
	})
}

// JSONStringByteLength returns an AmountExtractor that returns the byte length of a string field in a JSON body.
func JSONStringByteLength(field string) AmountExtractor {
	return FromBody(func(body []byte) (int, error) {
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return 0, fmt.Errorf("failed to parse JSON: %w", err)
		}

		val, ok := data[field]
		if !ok {
			return 0, fmt.Errorf("field %q not found", field)
		}

		s, ok := val.(string)
		if !ok {
			return 0, fmt.Errorf("field %q is not a string: %T", field, val)
		}

		return len(s), nil
	})
}
