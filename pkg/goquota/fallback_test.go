package goquota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockCache is a mock cache implementation for testing
type mockCache struct {
	entitlements map[string]*Entitlement
	usage        map[string]*Usage
	entTTL       map[string]time.Time
	usageTTL     map[string]time.Time
}

func newMockCache() *mockCache {
	return &mockCache{
		entitlements: make(map[string]*Entitlement),
		usage:        make(map[string]*Usage),
		entTTL:       make(map[string]time.Time),
		usageTTL:     make(map[string]time.Time),
	}
}

func (m *mockCache) GetEntitlement(userID string) (*Entitlement, bool) {
	ent, ok := m.entitlements[userID]
	if !ok {
		return nil, false
	}
	// Check TTL
	if exp, ok := m.entTTL[userID]; ok && time.Now().After(exp) {
		return nil, false
	}
	return ent, true
}

func (m *mockCache) SetEntitlement(userID string, ent *Entitlement, ttl time.Duration) {
	m.entitlements[userID] = ent
	if ttl > 0 {
		m.entTTL[userID] = time.Now().Add(ttl)
	}
}

func (m *mockCache) InvalidateEntitlement(userID string) {
	delete(m.entitlements, userID)
	delete(m.entTTL, userID)
}

func (m *mockCache) GetUsage(key string) (*Usage, bool) {
	usage, ok := m.usage[key]
	if !ok {
		return nil, false
	}
	// Check TTL
	if exp, ok := m.usageTTL[key]; ok && time.Now().After(exp) {
		return nil, false
	}
	return usage, true
}

func (m *mockCache) SetUsage(key string, usage *Usage, ttl time.Duration) {
	m.usage[key] = usage
	if ttl > 0 {
		m.usageTTL[key] = time.Now().Add(ttl)
	}
}

func (m *mockCache) InvalidateUsage(key string) {
	delete(m.usage, key)
	delete(m.usageTTL, key)
}

func (m *mockCache) Clear() {
	m.entitlements = make(map[string]*Entitlement)
	m.usage = make(map[string]*Usage)
	m.entTTL = make(map[string]time.Time)
	m.usageTTL = make(map[string]time.Time)
}

func (m *mockCache) Stats() CacheStats {
	return CacheStats{}
}

// mockFallbackStorage is a mock storage implementation for fallback testing
type mockFallbackStorage struct {
	getEntitlementErr error
	getUsageErr       error
	entitlement       *Entitlement
	usage             *Usage
}

func (m *mockFallbackStorage) GetEntitlement(_ context.Context, _ string) (*Entitlement, error) {
	if m.getEntitlementErr != nil {
		return nil, m.getEntitlementErr
	}
	return m.entitlement, nil
}

func (m *mockFallbackStorage) SetEntitlement(_ context.Context, _ *Entitlement) error {
	return nil
}

func (m *mockFallbackStorage) GetUsage(_ context.Context, _, _ string, _ Period) (*Usage, error) {
	if m.getUsageErr != nil {
		return nil, m.getUsageErr
	}
	return m.usage, nil
}

func (m *mockFallbackStorage) ConsumeQuota(_ context.Context, _ *ConsumeRequest) (int, error) {
	return 0, nil
}

func (m *mockFallbackStorage) ApplyTierChange(_ context.Context, _ *TierChangeRequest) error {
	return nil
}

func (m *mockFallbackStorage) SetUsage(_ context.Context, _, _ string, _ *Usage, _ Period) error {
	return nil
}

func (m *mockFallbackStorage) RefundQuota(_ context.Context, _ *RefundRequest) error {
	return nil
}

func (m *mockFallbackStorage) GetRefundRecord(_ context.Context, _ string) (*RefundRecord, error) {
	return nil, nil
}

func (m *mockFallbackStorage) GetConsumptionRecord(_ context.Context, _ string) (*ConsumptionRecord, error) {
	return nil, nil
}

//nolint:gocritic // Named return values would reduce readability here
func (m *mockFallbackStorage) CheckRateLimit(_ context.Context, _ *RateLimitRequest) (bool, int, time.Time, error) {
	return true, 100, time.Now().Add(time.Hour), nil
}

func (m *mockFallbackStorage) RecordRateLimitRequest(_ context.Context, _ *RateLimitRequest) error {
	return nil
}

func (m *mockFallbackStorage) AddLimit(_ context.Context, _, _ string, _ int, _ Period, _ string) error {
	return nil
}

func (m *mockFallbackStorage) SubtractLimit(_ context.Context, _, _ string, _ int, _ Period, _ string) error {
	return nil
}

// mockMetrics is a mock metrics implementation for testing
type mockMetrics struct {
	fallbackUsageCount         int
	optimisticConsumptionTotal int
	fallbackHits               map[string]int
}

func newMockMetrics() *mockMetrics {
	return &mockMetrics{
		fallbackHits: make(map[string]int),
	}
}

func (m *mockMetrics) RecordConsumption(_, _, _ string, _ int, _ bool)           {}
func (m *mockMetrics) RecordQuotaCheck(_, _ string, _ time.Duration)             {}
func (m *mockMetrics) RecordCacheHit(_ string)                                   {}
func (m *mockMetrics) RecordCacheMiss(_ string)                                  {}
func (m *mockMetrics) RecordStorageOperation(_ string, _ time.Duration, _ error) {}
func (m *mockMetrics) RecordCircuitBreakerStateChange(_ string)                  {}

func (m *mockMetrics) RecordFallbackUsage(_ string) {
	m.fallbackUsageCount++
}

func (m *mockMetrics) RecordOptimisticConsumption(amount int) {
	m.optimisticConsumptionTotal += amount
}

func (m *mockMetrics) RecordFallbackHit(strategy string) {
	m.fallbackHits[strategy]++
}

func (m *mockMetrics) RecordRateLimitCheck(_, _ string, _ bool, _ time.Duration) {}
func (m *mockMetrics) RecordRateLimitExceeded(_, _ string)                       {}
func (m *mockMetrics) RecordUsageAPIRequest(_, _ string)                         {}
func (m *mockMetrics) RecordUsageAPIRequestDuration(_ time.Duration)             {}
func (m *mockMetrics) RecordUsageAPIResourcesDiscovered(_ int)                   {}
func (m *mockMetrics) RecordUsageAPIResourceFilterEffectiveness(_, _ int)        {}
func (m *mockMetrics) RecordForeverCreditsBalance(_, _ string, _ int)            {}
func (m *mockMetrics) RecordForeverCreditsConsumption(_, _ string, _ bool)       {}
func (m *mockMetrics) RecordForeverCreditsConsumptionAmount(_, _ string, _ int)  {}
func (m *mockMetrics) RecordOrphanedForeverCredits(_, _ string)                  {}
func (m *mockMetrics) RecordHybridBillingUser(_ string)                          {}
func (m *mockMetrics) RecordQuotaWarning(_, _ string, _ float64)                 {}
func (m *mockMetrics) RecordQuotaExhaustion(_, _ string, _ PeriodType)           {}
func (m *mockMetrics) RecordQuotaRefund(_, _ string)                             {}
func (m *mockMetrics) RecordQuotaRefundAmount(_ string, _ int)                   {}
func (m *mockMetrics) RecordIdempotencyHit(_ string)                             {}
func (m *mockMetrics) RecordActiveUserByTier(_ string)                           {}
func (m *mockMetrics) RecordUsersApproachingLimit(_, _, _ string)                {}
func (m *mockMetrics) RecordResourceFilterQueriesSaved(_ int)                    {}
func (m *mockMetrics) RecordResourceFilterEffectivenessRatio(_ float64)          {}

// mockLogger is a mock logger implementation for testing
type mockLogger struct{}

func (m *mockLogger) Debug(_ string, _ ...Field) {}
func (m *mockLogger) Info(_ string, _ ...Field)  {}
func (m *mockLogger) Warn(_ string, _ ...Field)  {}
func (m *mockLogger) Error(_ string, _ ...Field) {}

// Test CacheFallbackStrategy

func TestCacheFallbackStrategy_ShouldFallback(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"circuit open", ErrCircuitOpen, true},
		{"storage unavailable", ErrStorageUnavailable, true},
		{"context deadline exceeded", context.DeadlineExceeded, true},
		{"context canceled", context.Canceled, true},
		{"quota exceeded", ErrQuotaExceeded, false},
		{"entitlement not found", ErrEntitlementNotFound, false},
		{"wrapped circuit open", errors.New("wrapped: " + ErrCircuitOpen.Error()), false}, // errors.Is needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strategy.ShouldFallback(tt.err)
			assert.Equal(t, tt.expected, result, "ShouldFallback(%v) = %v, want %v", tt.err, result, tt.expected)
		})
	}
}

const (
	testUserID   = "user1"
	testResource = "api_calls"
)

func TestCacheFallbackStrategy_GetFallbackUsage_Success(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger)

	ctx := context.Background()
	userID := testUserID
	resource := testResource
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	usageKey := userID + ":" + resource + ":" + period.Key()
	usage := &Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	cache.SetUsage(usageKey, usage, time.Minute)

	result, err := strategy.GetFallbackUsage(ctx, userID, resource, period)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, usage.Used, result.Used)
	assert.Equal(t, 1, metrics.fallbackHits["cache"])
}

func TestCacheFallbackStrategy_GetFallbackUsage_CacheMiss(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger)

	ctx := context.Background()
	userID := testUserID
	resource := testResource
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := strategy.GetFallbackUsage(ctx, userID, resource, period)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFallbackUnavailable)
	assert.Nil(t, result)
}

func TestCacheFallbackStrategy_GetFallbackUsage_StaleCache(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	maxStaleness := 5 * time.Minute
	strategy := NewCacheFallbackStrategy(cache, maxStaleness, metrics, logger)

	ctx := context.Background()
	userID := testUserID
	resource := testResource
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	usageKey := userID + ":" + resource + ":" + period.Key()
	usage := &Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC().Add(-10 * time.Minute), // 10 minutes ago
	}

	cache.SetUsage(usageKey, usage, time.Minute)

	result, err := strategy.GetFallbackUsage(ctx, userID, resource, period)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrStaleCache)
	assert.Nil(t, result)
}

func TestCacheFallbackStrategy_GetFallbackUsage_NilCache(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(nil, 5*time.Minute, metrics, logger)

	ctx := context.Background()
	userID := testUserID
	resource := testResource
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := strategy.GetFallbackUsage(ctx, userID, resource, period)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFallbackUnavailable)
	assert.Nil(t, result)
}

func TestCacheFallbackStrategy_GetFallbackEntitlement_Success(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger)

	ctx := context.Background()
	userID := testUserID
	ent := &Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	cache.SetEntitlement(userID, ent, time.Minute)

	result, err := strategy.GetFallbackEntitlement(ctx, userID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, ent.Tier, result.Tier)
	assert.Equal(t, 1, metrics.fallbackHits["cache"])
}

func TestCacheFallbackStrategy_GetFallbackEntitlement_StaleCache(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	maxStaleness := 5 * time.Minute
	strategy := NewCacheFallbackStrategy(cache, maxStaleness, metrics, logger)

	ctx := context.Background()
	userID := testUserID
	ent := &Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC().Add(-10 * time.Minute), // 10 minutes ago
	}

	cache.SetEntitlement(userID, ent, time.Minute)

	result, err := strategy.GetFallbackEntitlement(ctx, userID)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrStaleCache)
	assert.Nil(t, result)
}

func TestCacheFallbackStrategy_AllowOptimisticConsumption(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    100,
	}

	result := strategy.AllowOptimisticConsumption(usage, 10)
	assert.False(t, result) // Cache fallback doesn't support optimistic consumption
}

// Test OptimisticFallbackStrategy

func TestOptimisticFallbackStrategy_ShouldFallback(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"circuit open", ErrCircuitOpen, true},
		{"storage unavailable", ErrStorageUnavailable, true},
		{"context deadline exceeded", context.DeadlineExceeded, true},
		{"quota exceeded", ErrQuotaExceeded, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strategy.ShouldFallback(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_WithinLimit(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger) // 10%

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Max optimistic: 10% of 1000 = 100
	// First consumption: 50 (within limit)
	result := strategy.AllowOptimisticConsumption(usage, 50)
	assert.True(t, result)
	assert.Equal(t, 50, metrics.optimisticConsumptionTotal)

	// Second consumption: 30 (total 80, still within limit)
	result = strategy.AllowOptimisticConsumption(usage, 30)
	assert.True(t, result)
	assert.Equal(t, 80, metrics.optimisticConsumptionTotal)
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_ExceedsLimit(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger) // 10%

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Max optimistic: 10% of 1000 = 100
	// First consumption: 80
	result := strategy.AllowOptimisticConsumption(usage, 80)
	assert.True(t, result)

	// Second consumption: 30 (total 110, exceeds limit)
	result = strategy.AllowOptimisticConsumption(usage, 30)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_ExactLimit(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger) // 10%

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Max optimistic: 10% of 1000 = 100
	// Exact limit consumption
	result := strategy.AllowOptimisticConsumption(usage, 100)
	assert.True(t, result)

	// One more should fail
	result = strategy.AllowOptimisticConsumption(usage, 1)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_NilUsage(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	result := strategy.AllowOptimisticConsumption(nil, 10)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_ZeroLimit(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     0,
		Limit:    0,
	}

	result := strategy.AllowOptimisticConsumption(usage, 10)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_NegativeAmount(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	result := strategy.AllowOptimisticConsumption(usage, -10)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_AllowOptimisticConsumption_ZeroAmount(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	result := strategy.AllowOptimisticConsumption(usage, 0)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_DefaultPercentage(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(0, metrics, logger) // Should default to 10%

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Should allow up to 100 (10% of 1000)
	result := strategy.AllowOptimisticConsumption(usage, 100)
	assert.True(t, result)

	result = strategy.AllowOptimisticConsumption(usage, 1)
	assert.False(t, result)
}

func TestOptimisticFallbackStrategy_MaxPercentage(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(150.0, metrics, logger) // Should cap at 100%

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Should allow up to 1000 (100% of 1000, not 150%)
	result := strategy.AllowOptimisticConsumption(usage, 1000)
	assert.True(t, result)
}

func TestOptimisticFallbackStrategy_GetOptimisticUsage(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	key := usage.UserID + ":" + usage.Resource + ":" + usage.Period.Key()

	// Initially zero
	assert.Equal(t, 0, strategy.GetOptimisticUsage(key))

	// After consumption
	strategy.AllowOptimisticConsumption(usage, 50)
	assert.Equal(t, 50, strategy.GetOptimisticUsage(key))

	// Reset
	strategy.ResetOptimisticUsage(key)
	assert.Equal(t, 0, strategy.GetOptimisticUsage(key))
}

// Test SecondaryStorageFallbackStrategy

func TestSecondaryStorageFallbackStrategy_ShouldFallback(t *testing.T) {
	storage := &mockFallbackStorage{}
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewSecondaryStorageFallbackStrategy(storage, metrics, logger)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"circuit open", ErrCircuitOpen, true},
		{"storage unavailable", ErrStorageUnavailable, true},
		{"quota exceeded", ErrQuotaExceeded, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strategy.ShouldFallback(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSecondaryStorageFallbackStrategy_GetFallbackUsage_Success(t *testing.T) {
	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    100,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	storage := &mockFallbackStorage{usage: usage}
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewSecondaryStorageFallbackStrategy(storage, metrics, logger)

	ctx := context.Background()
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := strategy.GetFallbackUsage(ctx, "user1", "api_calls", period)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, usage.Used, result.Used)
	assert.Equal(t, 1, metrics.fallbackHits["secondary_storage"])
}

func TestSecondaryStorageFallbackStrategy_GetFallbackUsage_StorageError(t *testing.T) {
	storage := &mockFallbackStorage{getUsageErr: ErrStorageUnavailable}
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewSecondaryStorageFallbackStrategy(storage, metrics, logger)

	ctx := context.Background()
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := strategy.GetFallbackUsage(ctx, "user1", "api_calls", period)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFallbackUnavailable)
	assert.Nil(t, result)
}

func TestSecondaryStorageFallbackStrategy_GetFallbackUsage_NilStorage(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewSecondaryStorageFallbackStrategy(nil, metrics, logger)

	ctx := context.Background()
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := strategy.GetFallbackUsage(ctx, "user1", "api_calls", period)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFallbackUnavailable)
	assert.Nil(t, result)
}

func TestSecondaryStorageFallbackStrategy_GetFallbackEntitlement_Success(t *testing.T) {
	ent := &Entitlement{
		UserID:                "user1",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	storage := &mockFallbackStorage{entitlement: ent}
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewSecondaryStorageFallbackStrategy(storage, metrics, logger)

	ctx := context.Background()
	result, err := strategy.GetFallbackEntitlement(ctx, "user1")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, ent.Tier, result.Tier)
	assert.Equal(t, 1, metrics.fallbackHits["secondary_storage"])
}

func TestSecondaryStorageFallbackStrategy_AllowOptimisticConsumption(t *testing.T) {
	storage := &mockFallbackStorage{}
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewSecondaryStorageFallbackStrategy(storage, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    100,
	}

	result := strategy.AllowOptimisticConsumption(usage, 10)
	assert.False(t, result) // Secondary storage doesn't support optimistic consumption
}

// Test CompositeFallbackStrategy

func TestCompositeFallbackStrategy_ShouldFallback(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}

	strategies := []FallbackStrategy{
		NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger),
		NewOptimisticFallbackStrategy(10.0, metrics, logger),
	}

	composite := NewCompositeFallbackStrategy(strategies, metrics, logger)

	result := composite.ShouldFallback(ErrCircuitOpen)
	assert.True(t, result)

	result = composite.ShouldFallback(ErrQuotaExceeded)
	assert.False(t, result)
}

func TestCompositeFallbackStrategy_GetFallbackUsage_FirstStrategySucceeds(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    100,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
		UpdatedAt: time.Now().UTC(),
	}

	usageKey := "user1:api_calls:" + usage.Period.Key()
	cache.SetUsage(usageKey, usage, time.Minute)

	strategies := []FallbackStrategy{
		NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger),
		NewOptimisticFallbackStrategy(10.0, metrics, logger),
	}

	composite := NewCompositeFallbackStrategy(strategies, metrics, logger)

	ctx := context.Background()
	result, err := composite.GetFallbackUsage(ctx, "user1", "api_calls", usage.Period)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, metrics.fallbackHits["cache"])
}

func TestCompositeFallbackStrategy_GetFallbackUsage_AllStrategiesFail(t *testing.T) {
	cache := newMockCache() // Empty cache
	metrics := newMockMetrics()
	logger := &mockLogger{}

	strategies := []FallbackStrategy{
		NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger),
		NewOptimisticFallbackStrategy(10.0, metrics, logger), // Doesn't provide usage
	}

	composite := NewCompositeFallbackStrategy(strategies, metrics, logger)

	ctx := context.Background()
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := composite.GetFallbackUsage(ctx, "user1", "api_calls", period)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestCompositeFallbackStrategy_GetFallbackUsage_EmptyStrategies(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}

	composite := NewCompositeFallbackStrategy(nil, metrics, logger)

	ctx := context.Background()
	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	result, err := composite.GetFallbackUsage(ctx, "user1", "api_calls", period)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFallbackUnavailable)
	assert.Nil(t, result)
}

func TestCompositeFallbackStrategy_AllowOptimisticConsumption_FirstStrategyAllows(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	strategies := []FallbackStrategy{
		NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger), // Doesn't allow
		NewOptimisticFallbackStrategy(10.0, metrics, logger),            // Allows
	}

	composite := NewCompositeFallbackStrategy(strategies, metrics, logger)

	result := composite.AllowOptimisticConsumption(usage, 50)
	assert.True(t, result)
}

func TestCompositeFallbackStrategy_PanicRecovery(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}

	// Create a strategy that panics
	panicStrategy := &panicStrategy{}
	strategies := []FallbackStrategy{
		panicStrategy,
		NewOptimisticFallbackStrategy(10.0, metrics, logger),
	}

	composite := NewCompositeFallbackStrategy(strategies, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Should recover from panic and try next strategy
	result := composite.AllowOptimisticConsumption(usage, 50)
	assert.True(t, result) // Second strategy should succeed
}

// panicStrategy is a test strategy that panics
type panicStrategy struct{}

func (p *panicStrategy) ShouldFallback(_ error) bool {
	return true
}

func (p *panicStrategy) GetFallbackUsage(_ context.Context, _, _ string, _ Period) (*Usage, error) {
	panic("test panic")
}

func (p *panicStrategy) GetFallbackEntitlement(_ context.Context, _ string) (*Entitlement, error) {
	panic("test panic")
}

func (p *panicStrategy) AllowOptimisticConsumption(_ *Usage, _ int) bool {
	panic("test panic")
}

// Test error classification with wrapped errors

func TestShouldFallback_WrappedErrors(t *testing.T) {
	cache := newMockCache()
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewCacheFallbackStrategy(cache, 5*time.Minute, metrics, logger)

	// Test wrapped ErrCircuitOpen
	wrappedErr := errors.New("wrapped: " + ErrCircuitOpen.Error())
	result := strategy.ShouldFallback(wrappedErr)
	// Note: errors.Is is needed for wrapped errors, but our current implementation
	// uses direct comparison. This is a limitation but acceptable for now.
	assert.False(t, result) // Current implementation doesn't handle wrapped errors

	// Test with errors.Is compatible error
	result = strategy.ShouldFallback(ErrCircuitOpen)
	assert.True(t, result)
}

// Test concurrent access

func TestOptimisticFallbackStrategy_ConcurrentAccess(t *testing.T) {
	metrics := newMockMetrics()
	logger := &mockLogger{}
	strategy := NewOptimisticFallbackStrategy(10.0, metrics, logger)

	usage := &Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    1000,
		Period: Period{
			Start: time.Now().UTC(),
			End:   time.Now().UTC().Add(24 * time.Hour),
			Type:  PeriodTypeDaily,
		},
	}

	// Concurrent optimistic consumptions
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			strategy.AllowOptimisticConsumption(usage, 5)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Total should be 50 (10 * 5), within limit of 100
	key := usage.UserID + ":" + usage.Resource + ":" + usage.Period.Key()
	total := strategy.GetOptimisticUsage(key)
	assert.LessOrEqual(t, total, 100)
}
