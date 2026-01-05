// Package memory provides an in-memory implementation of the goquota.Storage interface.
// This implementation is primarily intended for testing and development.
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// tokenBucketState represents the state of a token bucket
type tokenBucketState struct {
	mu         sync.Mutex
	tokens     int
	lastRefill time.Time
	capacity   int
	refillRate int
	window     time.Duration
}

// slidingWindowState represents the state of a sliding window
type slidingWindowState struct {
	mu         sync.Mutex
	timestamps []time.Time
	window     time.Duration
	limit      int
}

// Storage implements goquota.Storage using in-memory maps
type Storage struct {
	mu             sync.RWMutex
	entitlements   map[string]*goquota.Entitlement
	usage          map[string]*goquota.Usage
	refunds        map[string]*goquota.RefundRecord      // keyed by idempotency key
	consumptions   map[string]*goquota.ConsumptionRecord // keyed by idempotency key
	topUps         map[string]bool                       // keyed by idempotency key (for idempotency checks)
	tokenBuckets   map[string]*tokenBucketState          // keyed by userID:resource
	slidingWindows map[string]*slidingWindowState        // keyed by userID:resource
}

// Now returns the current time.
// For in-memory storage, there's no clock skew issue since it's single-process,
// but we implement TimeSource for API consistency.
func (s *Storage) Now(_ context.Context) (time.Time, error) {
	return time.Now().UTC(), nil
}

// New creates a new in-memory storage adapter
func New() *Storage {
	return &Storage{
		entitlements:   make(map[string]*goquota.Entitlement),
		usage:          make(map[string]*goquota.Usage),
		refunds:        make(map[string]*goquota.RefundRecord),
		consumptions:   make(map[string]*goquota.ConsumptionRecord),
		topUps:         make(map[string]bool),
		tokenBuckets:   make(map[string]*tokenBucketState),
		slidingWindows: make(map[string]*slidingWindowState),
	}
}

// GetEntitlement implements goquota.Storage
func (s *Storage) GetEntitlement(_ context.Context, userID string) (*goquota.Entitlement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ent, ok := s.entitlements[userID]
	if !ok {
		return nil, goquota.ErrEntitlementNotFound
	}

	// Return a copy to prevent external mutations
	entCopy := *ent
	return &entCopy, nil
}

// SetEntitlement implements goquota.Storage
func (s *Storage) SetEntitlement(_ context.Context, ent *goquota.Entitlement) error {
	if ent == nil || ent.UserID == "" {
		return fmt.Errorf("invalid entitlement")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store a copy to prevent external mutations
	entCopy := *ent
	s.entitlements[ent.UserID] = &entCopy
	return nil
}

// GetUsage implements goquota.Storage
func (s *Storage) GetUsage(_ context.Context, userID, resource string, period goquota.Period) (*goquota.Usage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := usageKey(userID, resource, period)
	usage, ok := s.usage[key]
	if !ok {
		return nil, nil // No usage yet is not an error
	}

	// Return a copy
	usageCopy := *usage
	return &usageCopy, nil
}

// ConsumeQuota implements goquota.Storage with transaction-safe consumption
func (s *Storage) ConsumeQuota(_ context.Context, req *goquota.ConsumeRequest) (int, error) {
	if req.Amount < 0 {
		return 0, goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return 0, nil // No-op
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate consumption using idempotency key
	if req.IdempotencyKey != "" {
		if existing, exists := s.consumptions[req.IdempotencyKey]; exists {
			// Duplicate consumption - return cached result (idempotent)
			return existing.NewUsed, nil
		}
	}

	key := usageKey(req.UserID, req.Resource, req.Period)
	usage, ok := s.usage[key]

	currentUsed := 0
	if ok {
		currentUsed = usage.Used
	}

	newUsed := currentUsed + req.Amount
	if newUsed > req.Limit {
		return currentUsed, goquota.ErrQuotaExceeded
	}

	// Update or create usage
	s.usage[key] = &goquota.Usage{
		UserID:    req.UserID,
		Resource:  req.Resource,
		Used:      newUsed,
		Limit:     req.Limit,
		Period:    req.Period,
		Tier:      req.Tier,
		UpdatedAt: time.Now().UTC(),
	}

	// Store consumption record for idempotency (if key provided)
	if req.IdempotencyKey != "" {
		record := &goquota.ConsumptionRecord{
			ConsumptionID:  req.IdempotencyKey,
			UserID:         req.UserID,
			Resource:       req.Resource,
			Amount:         req.Amount,
			Period:         req.Period,
			Timestamp:      time.Now().UTC(),
			IdempotencyKey: req.IdempotencyKey,
			NewUsed:        newUsed,
		}
		s.consumptions[req.IdempotencyKey] = record
	}

	return newUsed, nil
}

// ApplyTierChange implements goquota.Storage
func (s *Storage) ApplyTierChange(_ context.Context, req *goquota.TierChangeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// For in-memory implementation, we just update the limit
	key := usageKey(req.UserID, req.Resource, req.Period)

	usage, ok := s.usage[key]
	if !ok {
		// Create new usage with new limit
		s.usage[key] = &goquota.Usage{
			UserID:    req.UserID,
			Resource:  req.Resource,
			Used:      req.CurrentUsed,
			Limit:     req.NewLimit,
			Period:    req.Period,
			Tier:      req.NewTier,
			UpdatedAt: time.Now().UTC(),
		}
		return nil
	}

	// Update existing usage
	usage.Limit = req.NewLimit
	usage.Tier = req.NewTier
	usage.UpdatedAt = time.Now().UTC()

	return nil
}

// usageKey generates a unique key for usage tracking
func usageKey(userID, resource string, period goquota.Period) string {
	return fmt.Sprintf("%s:%s:%s", userID, resource, period.Key())
}

// SetUsage implements goquota.Storage
func (s *Storage) SetUsage(_ context.Context, userID, resource string,
	usage *goquota.Usage, period goquota.Period) error {
	if usage == nil {
		return fmt.Errorf("usage is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := usageKey(userID, resource, period)
	usageCopy := *usage
	s.usage[key] = &usageCopy
	return nil
}

// RefundQuota implements goquota.Storage
func (s *Storage) RefundQuota(_ context.Context, req *goquota.RefundRequest) error {
	if req.Amount < 0 {
		return goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return nil // No-op
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate refund
	if req.IdempotencyKey != "" {
		if _, exists := s.refunds[req.IdempotencyKey]; exists {
			// Duplicate refund - return success (idempotent)
			return nil
		}
	}

	// Calculate period
	var period goquota.Period
	// Use period from request if available (populated by Manager)
	if !req.Period.Start.IsZero() {
		period = req.Period
	} else {
		// Fallback for direct usage (though Manager always sets it)
		now := time.Now().UTC()
		switch req.PeriodType {
		case goquota.PeriodTypeMonthly:
			// Use simple monthly period calculation
			start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 1, 0)
			period = goquota.Period{Start: start, End: end, Type: goquota.PeriodTypeMonthly}
		case goquota.PeriodTypeDaily:
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			end := start.Add(24 * time.Hour)
			period = goquota.Period{Start: start, End: end, Type: goquota.PeriodTypeDaily}
		default:
			return goquota.ErrInvalidPeriod
		}
	}

	key := usageKey(req.UserID, req.Resource, period)
	usage, ok := s.usage[key]

	if !ok {
		// No usage to refund - this is not an error
		return nil
	}

	// Refund the quota (decrease used amount)
	newUsed := usage.Used - req.Amount
	if newUsed < 0 {
		newUsed = 0
	}

	usage.Used = newUsed
	usage.UpdatedAt = time.Now().UTC()

	// Store refund record for audit trail
	if req.IdempotencyKey != "" {
		record := &goquota.RefundRecord{
			RefundID:       req.IdempotencyKey, // Using idempotency key as refund ID
			UserID:         req.UserID,
			Resource:       req.Resource,
			Amount:         req.Amount,
			Period:         period,
			Timestamp:      time.Now().UTC(),
			IdempotencyKey: req.IdempotencyKey,
			Reason:         req.Reason,
			Metadata:       req.Metadata,
		}
		s.refunds[req.IdempotencyKey] = record
	}

	return nil
}

// GetRefundRecord implements goquota.Storage
func (s *Storage) GetRefundRecord(_ context.Context, idempotencyKey string) (*goquota.RefundRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.refunds[idempotencyKey]
	if !ok {
		return nil, nil // No record found is not an error
	}

	// Return a copy
	recordCopy := *record
	return &recordCopy, nil
}

// GetConsumptionRecord implements goquota.Storage
func (s *Storage) GetConsumptionRecord(_ context.Context, idempotencyKey string) (*goquota.ConsumptionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.consumptions[idempotencyKey]
	if !ok {
		return nil, nil // No record found is not an error
	}

	// Return a copy
	recordCopy := *record
	return &recordCopy, nil
}

// CheckRateLimit implements goquota.Storage
//
//nolint:gocritic // Named return values would reduce readability here
func (s *Storage) CheckRateLimit(_ context.Context, req *goquota.RateLimitRequest) (bool, int, time.Time, error) {
	if req == nil {
		return false, 0, time.Time{}, fmt.Errorf("rate limit request is required")
	}

	key := rateLimitKey(req.UserID, req.Resource)

	switch req.Algorithm {
	case "token_bucket":
		return s.checkTokenBucket(key, req)
	case "sliding_window":
		return s.checkSlidingWindow(key, req)
	default:
		return false, 0, time.Time{}, fmt.Errorf("unknown rate limit algorithm: %s", req.Algorithm)
	}
}

//nolint:gocritic // Named return values would reduce readability here
func (s *Storage) checkTokenBucket(key string, req *goquota.RateLimitRequest) (bool, int, time.Time, error) {
	burst := req.Burst
	if burst <= 0 {
		burst = req.Rate
	}

	s.mu.Lock()
	bucket, exists := s.tokenBuckets[key]
	if !exists {
		bucket = &tokenBucketState{
			tokens:     burst,
			lastRefill: req.Now,
			capacity:   burst,
			refillRate: req.Rate,
			window:     req.Window,
		}
		s.tokenBuckets[key] = bucket
	}
	s.mu.Unlock()

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Refill tokens based on elapsed time
	elapsed := req.Now.Sub(bucket.lastRefill)
	if elapsed > 0 {
		tokensToAdd := int(float64(bucket.refillRate) * elapsed.Seconds() / bucket.window.Seconds())
		if tokensToAdd > 0 {
			bucket.tokens = intMin(bucket.tokens+tokensToAdd, bucket.capacity)
			bucket.lastRefill = req.Now
		}
	}

	// Check if we have tokens
	if bucket.tokens <= 0 {
		// Calculate when next token will be available
		nextTokenTime := bucket.lastRefill.Add(bucket.window / time.Duration(bucket.refillRate))
		if nextTokenTime.Before(req.Now) {
			nextTokenTime = req.Now.Add(bucket.window / time.Duration(bucket.refillRate))
		}
		return false, 0, nextTokenTime, nil
	}

	// Consume a token
	bucket.tokens--

	// Calculate reset time (when bucket will be full again)
	resetTime := req.Now.Add(bucket.window)
	if bucket.tokens < bucket.capacity {
		// Calculate time until full
		tokensNeeded := bucket.capacity - bucket.tokens
		timeToFull := time.Duration(float64(tokensNeeded) * float64(bucket.window) / float64(bucket.refillRate))
		resetTime = req.Now.Add(timeToFull)
	}

	return true, bucket.tokens, resetTime, nil
}

//nolint:gocritic // Named return values would reduce readability here
func (s *Storage) checkSlidingWindow(key string, req *goquota.RateLimitRequest) (bool, int, time.Time, error) {
	s.mu.Lock()
	window, exists := s.slidingWindows[key]
	if !exists {
		window = &slidingWindowState{
			timestamps: make([]time.Time, 0),
			window:     req.Window,
			limit:      req.Rate,
		}
		s.slidingWindows[key] = window
	}
	s.mu.Unlock()

	window.mu.Lock()
	defer window.mu.Unlock()

	// Remove timestamps outside the window
	cutoff := req.Now.Add(-window.window)
	validStart := 0
	for i, ts := range window.timestamps {
		if ts.After(cutoff) {
			validStart = i
			break
		}
	}
	window.timestamps = window.timestamps[validStart:]

	// Check if we're within the limit
	if len(window.timestamps) >= window.limit {
		// Find the oldest timestamp still in the window
		oldestInWindow := window.timestamps[0]
		resetTime := oldestInWindow.Add(window.window)
		return false, 0, resetTime, nil
	}

	// Add current timestamp
	window.timestamps = append(window.timestamps, req.Now)

	remaining := window.limit - len(window.timestamps)
	resetTime := req.Now.Add(window.window)
	if len(window.timestamps) > 0 {
		// Reset time is when the oldest request expires
		oldest := window.timestamps[0]
		resetTime = oldest.Add(window.window)
	}

	return true, remaining, resetTime, nil
}

// RecordRateLimitRequest implements goquota.Storage
func (s *Storage) RecordRateLimitRequest(_ context.Context, req *goquota.RateLimitRequest) error {
	if req == nil {
		return fmt.Errorf("rate limit request is required")
	}

	// For sliding window, timestamps are already recorded in CheckRateLimit
	// For token bucket, no additional recording is needed
	// This method is a no-op for Memory storage
	return nil
}

// rateLimitKey generates a unique key for rate limiting
func rateLimitKey(userID, resource string) string {
	return fmt.Sprintf("%s:%s", userID, resource)
}

// intMin returns the minimum of two integers
func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Clear removes all data (useful for testing)
func (s *Storage) Clear(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entitlements = make(map[string]*goquota.Entitlement)
	s.usage = make(map[string]*goquota.Usage)
	s.refunds = make(map[string]*goquota.RefundRecord)
	s.consumptions = make(map[string]*goquota.ConsumptionRecord)
	s.topUps = make(map[string]bool)
	s.tokenBuckets = make(map[string]*tokenBucketState)
	s.slidingWindows = make(map[string]*slidingWindowState)
	return nil
}

// AddLimit implements goquota.Storage
func (s *Storage) AddLimit(
	_ context.Context, userID, resource string, amount int, period goquota.Period, idempotencyKey string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check idempotency (if key provided)
	if idempotencyKey != "" {
		if s.topUps[idempotencyKey] {
			return goquota.ErrIdempotencyKeyExists
		}
		// Mark as processed
		s.topUps[idempotencyKey] = true
	}

	// 2. Get or create usage record
	key := usageKey(userID, resource, period)
	usage, ok := s.usage[key]
	if !ok {
		// Create new usage record
		usage = &goquota.Usage{
			UserID:    userID,
			Resource:  resource,
			Used:      0,
			Limit:     amount,
			Period:    period,
			Tier:      "default",
			UpdatedAt: time.Now().UTC(),
		}
		s.usage[key] = usage
		return nil
	}

	// 3. Increment limit atomically
	usage.Limit += amount
	usage.UpdatedAt = time.Now().UTC()

	return nil
}

// SubtractLimit implements goquota.Storage
func (s *Storage) SubtractLimit(
	_ context.Context, userID, resource string, amount int, period goquota.Period, idempotencyKey string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check idempotency (if key provided)
	if idempotencyKey != "" {
		// Check if refund record exists
		if _, exists := s.refunds[idempotencyKey]; exists {
			return goquota.ErrIdempotencyKeyExists
		}
		// Create refund record for idempotency
		s.refunds[idempotencyKey] = &goquota.RefundRecord{
			RefundID:       idempotencyKey,
			UserID:         userID,
			Resource:       resource,
			Amount:         amount,
			Period:         period,
			Timestamp:      time.Now().UTC(),
			IdempotencyKey: idempotencyKey,
		}
	}

	// 2. Get usage record
	key := usageKey(userID, resource, period)
	usage, ok := s.usage[key]
	if !ok {
		// No usage to refund - this is not an error
		return nil
	}

	// 3. Decrement limit atomically with clamp to 0
	newLimit := usage.Limit - amount
	if newLimit < 0 {
		newLimit = 0
	}
	usage.Limit = newLimit
	usage.UpdatedAt = time.Now().UTC()

	return nil
}
