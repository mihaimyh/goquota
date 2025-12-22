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

func (m *mockFlakeyStorage) GetEntitlement(ctx context.Context, userID string) (*Entitlement, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return &Entitlement{UserID: userID, Tier: "explorer"}, nil
}

func TestManagerWithCircuitBreaker(t *testing.T) {
	storage := &mockFlakeyStorage{fail: false}
	config := Config{
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

	mgr, err := NewManager(storage, config)
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

func (m *mockFlakeyStorage) SetEntitlement(ctx context.Context, ent *Entitlement) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) GetUsage(ctx context.Context, userID, resource string, period Period) (*Usage, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return &Usage{}, nil
}

func (m *mockFlakeyStorage) ConsumeQuota(ctx context.Context, req *ConsumeRequest) (int, error) {
	if m.fail {
		return 0, errors.New("db error")
	}
	return 1, nil
}

func (m *mockFlakeyStorage) ApplyTierChange(ctx context.Context, req *TierChangeRequest) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) SetUsage(ctx context.Context, userID, resource string, usage *Usage, period Period) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) RefundQuota(ctx context.Context, req *RefundRequest) error {
	if m.fail {
		return errors.New("db error")
	}
	return nil
}

func (m *mockFlakeyStorage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*RefundRecord, error) {
	if m.fail {
		return nil, errors.New("db error")
	}
	return nil, nil
}
