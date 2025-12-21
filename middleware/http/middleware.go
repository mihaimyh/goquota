// Package http provides HTTP middleware for quota enforcement
package http

import (
"context"
"fmt"
"io"
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
}

// Middleware creates an HTTP middleware that enforces quota limits
func Middleware(config Config) func(http.Handler) http.Handler {
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
if err != nil {
if config.OnError != nil {
config.OnError(w, r, err)
} else {
http.Error(w, "Bad Request", http.StatusBadRequest)
}
return
}

// Check and consume quota
ctx := r.Context()
err = config.Manager.Consume(ctx, userID, resource, amount, config.PeriodType)
if err != nil {
if err == goquota.ErrQuotaExceeded {
// Get current usage for error response
usage, _ := config.Manager.GetQuota(ctx, userID, resource, config.PeriodType)
if config.OnQuotaExceeded != nil {
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

// Quota consumed successfully, proceed to handler
next.ServeHTTP(w, r)
})
}
}

// HandlerFunc creates an HTTP middleware that enforces quota limits (HandlerFunc version)
func HandlerFunc(config Config) func(http.HandlerFunc) http.HandlerFunc {
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
return func(r *http.Request) (int, error) {
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
return func(r *http.Request) string {
return resource
}
}

// WithUserID adds user ID to request context
func WithUserID(ctx context.Context, userID string) context.Context {
return context.WithValue(ctx, UserIDKey, userID)
}