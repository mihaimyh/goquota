package goquota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockFlakeyStorage struct {
	Storage
	fail bool
}

func (m *mockFlakeyStorage) GetEntitlement(_ context.Context, userID string) (*Entitlement, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return &Entitlement{UserID: userID, Tier: "explorer"}, nil
}

func TestManagerWithCircuitBreaker(t *testing.T) {
	storage := &mockFlakeyStorage{fail: false}
	config := Config{
		DefaultTier: "explorer",
		Tiers: map[string]TierConfig{
			"explorer": {
				Name: "explorer",
				DailyQuotas: map[string]int{
					"resource1": 100,
				},
			},
		},
		CircuitBreakerConfig: &CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 2,
			ResetTimeout:     100 * time.Millisecond,
		},
	}

	mgr, err := NewManager(storage, &config)
	assert.NoError(t, err)

	ctx := context.Background()

	// 1. Works fine initially
	ent, err := mgr.GetEntitlement(ctx, "user1")
	assert.NoError(t, err)
	assert.NotNil(t, ent)

	// 2. Start failing
	storage.fail = true

	// First failure
	_, err = mgr.GetEntitlement(ctx, "user1")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrCircuitOpen)

	// Second failure -> Circuit opens
	_, err = mgr.GetEntitlement(ctx, "user1")
	assert.Error(t, err)

	// Third call -> Fast fail with ErrCircuitOpen
	_, err = mgr.GetEntitlement(ctx, "user1")
	assert.ErrorIs(t, err, ErrCircuitOpen)

	// 3. Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// 4. Recover
	storage.fail = false
	ent, err = mgr.GetEntitlement(ctx, "user1")
	assert.NoError(t, err)
	assert.NotNil(t, ent)

	// Circuit should be closed again
	ent, err = mgr.GetEntitlement(ctx, "user1")
	assert.NoError(t, err)

	// 5. Test other methods through CircuitBreakerStorage
	// Consume
	_, err = mgr.Consume(ctx, "user1", "resource1", 1, PeriodTypeDaily)
	assert.NoError(t, err)

	// GetQuota
	_, err = mgr.GetQuota(ctx, "user1", "resource1", PeriodTypeDaily)
	assert.NoError(t, err)

	// Refund
	err = mgr.Refund(ctx, &RefundRequest{UserID: "user1", Resource: "resource1", Amount: 1, PeriodType: PeriodTypeDaily})
	assert.NoError(t, err)

	// SetEntitlement
	err = mgr.SetEntitlement(ctx, ent)
	assert.NoError(t, err)

	// ApplyTierChange
	err = mgr.ApplyTierChange(ctx, "user1", "free", "pro", "resource1")
	assert.NoError(t, err)

	// 6. Test circuit opening with another method
	storage.fail = true
	for i := 0; i < 2; i++ {
		_, err = mgr.Consume(ctx, "user1", "resource1", 1, PeriodTypeDaily)
		assert.Error(t, err)
	}

	// Should be open now
	_, err = mgr.GetQuota(ctx, "user1", "resource1", PeriodTypeDaily)
	assert.ErrorIs(t, err, ErrCircuitOpen)
}

func (m *mockFlakeyStorage) SetEntitlement(_ context.Context, _ *Entitlement) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) GetUsage(_ context.Context, _, _ string, _ Period) (*Usage, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return &Usage{}, nil
}

func (m *mockFlakeyStorage) ConsumeQuota(_ context.Context, _ *ConsumeRequest) (int, error) {
	if m.fail {
		return 0, errors.New("db error")
	}
	return 1, nil
}

func (m *mockFlakeyStorage) ApplyTierChange(_ context.Context, _ *TierChangeRequest) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) SetUsage(_ context.Context, _, _ string, _ *Usage, _ Period) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) RefundQuota(_ context.Context, _ *RefundRequest) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) GetRefundRecord(_ context.Context, _ string) (*RefundRecord, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return nil, nil
}

func (m *mockFlakeyStorage) GetConsumptionRecord(_ context.Context, _ string) (*ConsumptionRecord, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return nil, nil
}

// Phase 7.3: Storage Failure Handling

func TestManager_StorageFailure_PartialWrite(t *testing.T) {
	storage := &mockFlakeyStorage{fail: false}
	config := Config{
		DefaultTier: "scholar", // Set default tier to avoid fallback issues
		Tiers: map[string]TierConfig{
			"scholar": {
				Name:          "scholar",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
		},
		CircuitBreakerConfig: &CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 5,
			ResetTimeout:     100 * time.Millisecond,
		},
	}

	mgr, err := NewManager(storage, &config)
	assert.NoError(t, err)

	ctx := context.Background()

	// Set entitlement
	ent := &Entitlement{
		UserID:                "user_partial",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = mgr.SetEntitlement(ctx, ent)
	assert.NoError(t, err)

	// Consume some quota
	_, err = mgr.Consume(ctx, "user_partial", "api_calls", 100, PeriodTypeMonthly)
	assert.NoError(t, err)

	// Simulate storage failure
	storage.fail = true

	// Try to consume - should fail and trigger circuit breaker
	_, err = mgr.Consume(ctx, "user_partial", "api_calls", 50, PeriodTypeMonthly)
	assert.Error(t, err)

	// After enough failures, circuit should open
	for i := 0; i < 4; i++ {
		_, _ = mgr.Consume(ctx, "user_partial", "api_calls", 50, PeriodTypeMonthly)
	}

	// Next call should fast-fail
	_, err = mgr.Consume(ctx, "user_partial", "api_calls", 50, PeriodTypeMonthly)
	assert.ErrorIs(t, err, ErrCircuitOpen)
}

func TestManager_StorageFailure_NetworkTimeout(t *testing.T) {
	storage := &mockFlakeyStorage{fail: false}
	config := Config{
		DefaultTier: "scholar",
		Tiers: map[string]TierConfig{
			"scholar": {
				Name:          "scholar",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
		},
		CircuitBreakerConfig: &CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 2,
			ResetTimeout:     50 * time.Millisecond,
		},
	}

	mgr, err := NewManager(storage, &config)
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Set entitlement
	ent := &Entitlement{
		UserID:                "user_timeout",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = mgr.SetEntitlement(ctx, ent)
	assert.NoError(t, err)

	// Simulate timeout by using canceled context
	canceledCtx, cancel2 := context.WithCancel(context.Background())
	cancel2()

	// Operations with canceled context should handle gracefully
	_, err = mgr.Consume(canceledCtx, "user_timeout", "api_calls", 10, PeriodTypeMonthly)
	// Error is expected for canceled context
	assert.Error(t, err)
}

func TestManager_StorageFailure_CircuitBreaker(t *testing.T) {
	storage := &mockFlakeyStorage{fail: false}
	config := Config{
		DefaultTier: "scholar", // Set default tier to avoid fallback issues
		Tiers: map[string]TierConfig{
			"scholar": {
				Name:          "scholar",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
		},
		CircuitBreakerConfig: &CircuitBreakerConfig{
			Enabled:          true,
			FailureThreshold: 3,
			ResetTimeout:     100 * time.Millisecond,
		},
	}

	mgr, err := NewManager(storage, &config)
	assert.NoError(t, err)

	ctx := context.Background()

	// Set entitlement
	ent := &Entitlement{
		UserID:                "user_cb",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = mgr.SetEntitlement(ctx, ent)
	assert.NoError(t, err)

	// Normal operation
	_, err = mgr.Consume(ctx, "user_cb", "api_calls", 100, PeriodTypeMonthly)
	assert.NoError(t, err)

	// Start failing
	storage.fail = true

	// Failures should accumulate
	for i := 0; i < 3; i++ {
		_, err = mgr.Consume(ctx, "user_cb", "api_calls", 50, PeriodTypeMonthly)
		assert.Error(t, err)
	}

	// Circuit should be open now
	_, err = mgr.Consume(ctx, "user_cb", "api_calls", 50, PeriodTypeMonthly)
	assert.ErrorIs(t, err, ErrCircuitOpen)

	// Wait for reset
	time.Sleep(150 * time.Millisecond)

	// Recover
	storage.fail = false

	// Should work again (half-open -> closed on success)
	_, err = mgr.Consume(ctx, "user_cb", "api_calls", 50, PeriodTypeMonthly)
	assert.NoError(t, err)
}
