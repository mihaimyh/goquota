package goquota

import (
	"context"
	"time"
)

// CircuitBreakerStorage wraps a Storage implementation with circuit breaker protection.
type CircuitBreakerStorage struct {
	storage Storage
	cb      CircuitBreaker
}

// NewCircuitBreakerStorage creates a new storage wrapper with circuit breaker.
func NewCircuitBreakerStorage(storage Storage, cb CircuitBreaker) *CircuitBreakerStorage {
	return &CircuitBreakerStorage{
		storage: storage,
		cb:      cb,
	}
}

func (s *CircuitBreakerStorage) GetEntitlement(ctx context.Context, userID string) (*Entitlement, error) {
	var ent *Entitlement
	err := s.cb.Execute(ctx, func() error {
		var e error
		ent, e = s.storage.GetEntitlement(ctx, userID)
		return e
	})
	return ent, err
}

func (s *CircuitBreakerStorage) SetEntitlement(ctx context.Context, ent *Entitlement) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.SetEntitlement(ctx, ent)
	})
}

func (s *CircuitBreakerStorage) GetUsage(ctx context.Context, userID, resource string, period Period) (*Usage, error) {
	var usage *Usage
	err := s.cb.Execute(ctx, func() error {
		var e error
		usage, e = s.storage.GetUsage(ctx, userID, resource, period)
		return e
	})
	return usage, err
}

func (s *CircuitBreakerStorage) ConsumeQuota(ctx context.Context, req *ConsumeRequest) (int, error) {
	var used int
	err := s.cb.Execute(ctx, func() error {
		var e error
		used, e = s.storage.ConsumeQuota(ctx, req)
		return e
	})
	return used, err
}

func (s *CircuitBreakerStorage) ApplyTierChange(ctx context.Context, req *TierChangeRequest) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.ApplyTierChange(ctx, req)
	})
}

func (s *CircuitBreakerStorage) SetUsage(ctx context.Context, userID, resource string,
	usage *Usage, period Period) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.SetUsage(ctx, userID, resource, usage, period)
	})
}

func (s *CircuitBreakerStorage) RefundQuota(ctx context.Context, req *RefundRequest) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.RefundQuota(ctx, req)
	})
}

func (s *CircuitBreakerStorage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*RefundRecord, error) {
	var record *RefundRecord
	err := s.cb.Execute(ctx, func() error {
		var e error
		record, e = s.storage.GetRefundRecord(ctx, idempotencyKey)
		return e
	})
	return record, err
}

func (s *CircuitBreakerStorage) GetConsumptionRecord(ctx context.Context,
	idempotencyKey string) (*ConsumptionRecord, error) {
	var record *ConsumptionRecord
	err := s.cb.Execute(ctx, func() error {
		var e error
		record, e = s.storage.GetConsumptionRecord(ctx, idempotencyKey)
		return e
	})
	return record, err
}

//nolint:gocritic // Named return values would reduce readability here
func (s *CircuitBreakerStorage) CheckRateLimit(
	ctx context.Context, req *RateLimitRequest,
) (bool, int, time.Time, error) {
	var allowed bool
	var remaining int
	var resetTime time.Time
	err := s.cb.Execute(ctx, func() error {
		var e error
		allowed, remaining, resetTime, e = s.storage.CheckRateLimit(ctx, req)
		return e
	})
	return allowed, remaining, resetTime, err
}

func (s *CircuitBreakerStorage) RecordRateLimitRequest(ctx context.Context, req *RateLimitRequest) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.RecordRateLimitRequest(ctx, req)
	})
}

func (s *CircuitBreakerStorage) AddLimit(
	ctx context.Context, userID, resource string, amount int, period Period, idempotencyKey string,
) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.AddLimit(ctx, userID, resource, amount, period, idempotencyKey)
	})
}

func (s *CircuitBreakerStorage) SubtractLimit(
	ctx context.Context, userID, resource string, amount int, period Period, idempotencyKey string,
) error {
	return s.cb.Execute(ctx, func() error {
		return s.storage.SubtractLimit(ctx, userID, resource, amount, period, idempotencyKey)
	})
}
