package goquota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockStorage is a mock storage implementation for testing
type mockStorage struct {
	setUsageErr             error
	getRefundRecordErr      error
	getConsumptionRecordErr error
	refundRecord            *RefundRecord
	consumptionRecord       *ConsumptionRecord
}

func (m *mockStorage) GetEntitlement(_ context.Context, _ string) (*Entitlement, error) {
	return nil, nil
}

func (m *mockStorage) SetEntitlement(_ context.Context, _ *Entitlement) error {
	return nil
}

func (m *mockStorage) GetUsage(_ context.Context, _, _ string, _ Period) (*Usage, error) {
	return nil, nil
}

func (m *mockStorage) ConsumeQuota(_ context.Context, _ *ConsumeRequest) (int, error) {
	return 0, nil
}

func (m *mockStorage) ApplyTierChange(_ context.Context, _ *TierChangeRequest) error {
	return nil
}

func (m *mockStorage) SetUsage(_ context.Context, _, _ string, _ *Usage, _ Period) error {
	return m.setUsageErr
}

func (m *mockStorage) RefundQuota(_ context.Context, _ *RefundRequest) error {
	return nil
}

func (m *mockStorage) GetRefundRecord(_ context.Context, _ string) (*RefundRecord, error) {
	if m.getRefundRecordErr != nil {
		return nil, m.getRefundRecordErr
	}
	return m.refundRecord, nil
}

func (m *mockStorage) GetConsumptionRecord(_ context.Context, _ string) (*ConsumptionRecord, error) {
	if m.getConsumptionRecordErr != nil {
		return nil, m.getConsumptionRecordErr
	}
	return m.consumptionRecord, nil
}

//nolint:gocritic // Named return values would reduce readability here
func (m *mockStorage) CheckRateLimit(_ context.Context, _ *RateLimitRequest) (bool, int, time.Time, error) {
	return true, 100, time.Now().Add(time.Hour), nil
}

func (m *mockStorage) RecordRateLimitRequest(_ context.Context, _ *RateLimitRequest) error {
	return nil
}

// Phase 2: Circuit Breaker Storage Tests

func TestCircuitBreakerStorage_SetUsage_OpenCircuit(t *testing.T) {
	ctx := context.Background()
	mock := &mockStorage{}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	// Open the circuit
	cb.Failure(errors.New("test error"))
	cb.Failure(errors.New("test error"))

	// Verify circuit is open
	assert.Equal(t, StateOpen, cb.State())

	storage := NewCircuitBreakerStorage(mock, cb)

	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	usage := &Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.ErrorIs(t, err, ErrCircuitOpen)
}

func TestCircuitBreakerStorage_SetUsage_ClosedCircuit(t *testing.T) {
	ctx := context.Background()
	mock := &mockStorage{}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	usage := &Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.NoError(t, err)
}

func TestCircuitBreakerStorage_SetUsage_StorageError(t *testing.T) {
	ctx := context.Background()
	storageErr := errors.New("storage error")
	mock := &mockStorage{setUsageErr: storageErr}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	usage := &Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.Error(t, err)
	assert.Equal(t, storageErr, err)
}

func TestCircuitBreakerStorage_GetRefundRecord_OpenCircuit(t *testing.T) {
	ctx := context.Background()
	mock := &mockStorage{}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	// Open the circuit
	cb.Failure(errors.New("test error"))
	cb.Failure(errors.New("test error"))

	assert.Equal(t, StateOpen, cb.State())

	storage := NewCircuitBreakerStorage(mock, cb)

	record, err := storage.GetRefundRecord(ctx, "key-123")
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.Nil(t, record)
}

func TestCircuitBreakerStorage_GetRefundRecord_ClosedCircuit(t *testing.T) {
	ctx := context.Background()
	expectedRecord := &RefundRecord{
		RefundID:       "key-123",
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         20,
		IdempotencyKey: "key-123",
	}
	mock := &mockStorage{refundRecord: expectedRecord}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	record, err := storage.GetRefundRecord(ctx, "key-123")
	assert.NoError(t, err)
	assert.Equal(t, expectedRecord, record)
}

func TestCircuitBreakerStorage_GetRefundRecord_StorageError(t *testing.T) {
	ctx := context.Background()
	storageErr := errors.New("storage error")
	mock := &mockStorage{getRefundRecordErr: storageErr}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	record, err := storage.GetRefundRecord(ctx, "key-123")
	assert.Error(t, err)
	assert.Equal(t, storageErr, err)
	assert.Nil(t, record)
}

func TestCircuitBreakerStorage_GetConsumptionRecord_OpenCircuit(t *testing.T) {
	ctx := context.Background()
	mock := &mockStorage{}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	// Open the circuit
	cb.Failure(errors.New("test error"))
	cb.Failure(errors.New("test error"))

	assert.Equal(t, StateOpen, cb.State())

	storage := NewCircuitBreakerStorage(mock, cb)

	record, err := storage.GetConsumptionRecord(ctx, "key-123")
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.Nil(t, record)
}

func TestCircuitBreakerStorage_GetConsumptionRecord_ClosedCircuit(t *testing.T) {
	ctx := context.Background()
	expectedRecord := &ConsumptionRecord{
		ConsumptionID:  "key-123",
		UserID:         "user1",
		Resource:       "api_calls",
		Amount:         10,
		IdempotencyKey: "key-123",
		NewUsed:        10,
	}
	mock := &mockStorage{consumptionRecord: expectedRecord}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	record, err := storage.GetConsumptionRecord(ctx, "key-123")
	assert.NoError(t, err)
	assert.Equal(t, expectedRecord, record)
}

func TestCircuitBreakerStorage_GetConsumptionRecord_StorageError(t *testing.T) {
	ctx := context.Background()
	storageErr := errors.New("storage error")
	mock := &mockStorage{getConsumptionRecordErr: storageErr}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	record, err := storage.GetConsumptionRecord(ctx, "key-123")
	assert.Error(t, err)
	assert.Equal(t, storageErr, err)
	assert.Nil(t, record)
}

func TestCircuitBreakerStorage_StateTransitions(t *testing.T) {
	ctx := context.Background()
	storageErr := errors.New("storage error")
	mock := &mockStorage{setUsageErr: storageErr}
	cb := NewDefaultCircuitBreaker(2, 100*time.Millisecond, nil)

	storage := NewCircuitBreakerStorage(mock, cb)

	period := Period{
		Start: time.Now().UTC(),
		End:   time.Now().UTC().Add(24 * time.Hour),
		Type:  PeriodTypeDaily,
	}

	usage := &Usage{
		UserID:    "user1",
		Resource:  "api_calls",
		Used:      50,
		Limit:     100,
		Period:    period,
		Tier:      "pro",
		UpdatedAt: time.Now().UTC(),
	}

	// First failure - circuit still closed
	err := storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.Error(t, err)
	assert.Equal(t, StateClosed, cb.State())

	// Second failure - circuit opens
	err = storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.Error(t, err)
	assert.Equal(t, StateOpen, cb.State())

	// Third call - fast fail
	err = storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.ErrorIs(t, err, ErrCircuitOpen)

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Circuit should be half-open
	assert.Equal(t, StateHalfOpen, cb.State())

	// Fix storage
	mock.setUsageErr = nil

	// Successful call should close circuit
	err = storage.SetUsage(ctx, "user1", "api_calls", usage, period)
	assert.NoError(t, err)
	assert.Equal(t, StateClosed, cb.State())
}
