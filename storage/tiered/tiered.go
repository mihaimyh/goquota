// Package tiered provides a Hot/Cold tiered storage adapter that orchestrates
// fast ephemeral storage (Hot) with durable persistent storage (Cold) using
// different data strategies optimized for each operation type.
package tiered

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Config configures the tiered storage behavior
type Config struct {
	// Hot is the L1 cache storage (e.g., Redis, Memory) for high-frequency operations
	Hot goquota.Storage

	// Cold is the L2 persistence storage (e.g., Postgres, Firestore) as the source of truth
	Cold goquota.Storage

	// AsyncUsageSync enables non-blocking synchronization for high-frequency
	// operations (ConsumeQuota). If false, writes are synchronous (slower but safer).
	AsyncUsageSync bool

	// SyncBufferSize is the size of the buffered channel for async operations.
	// Default: 1000
	SyncBufferSize int

	// AsyncErrorHandler is called when an async operation fails.
	// Essential for monitoring consistency drift.
	AsyncErrorHandler func(error)
}

// Storage implements a Hot/Cold tiered storage architecture.
// It orchestrates two storage backends with different strategies per operation type:
// - Read-Through: Entitlements, Usage reads, Record retrieval (Hot → Cold)
// - Write-Through: Entitlements, Usage writes, Tier changes, Limits, Refunds (Cold → Hot)
// - Hot-Only: Rate limits (Hot only)
// - Hot-Primary/Async-Audit: Quota consumption (Hot atomic + async Cold sync)
type Storage struct {
	hot  goquota.Storage
	cold goquota.Storage
	conf Config

	// Channel for async synchronization
	syncQueue chan func() error
	shutdown  chan struct{}
	wg        sync.WaitGroup
}

// New creates a new tiered storage adapter.
func New(config Config) (*Storage, error) {
	if config.Hot == nil || config.Cold == nil {
		return nil, errors.New("tiered storage: both hot and cold storage are required")
	}

	if config.SyncBufferSize <= 0 {
		config.SyncBufferSize = 1000
	}

	s := &Storage{
		hot:       config.Hot,
		cold:      config.Cold,
		conf:      config,
		syncQueue: make(chan func() error, config.SyncBufferSize),
		shutdown:  make(chan struct{}),
	}

	if config.AsyncUsageSync {
		s.startWorker()
	}

	return s, nil
}

// Close gracefully shuts down the async worker (if enabled).
func (s *Storage) Close() error {
	if s.conf.AsyncUsageSync {
		select {
		case <-s.shutdown:
			// Already closed
		default:
			close(s.shutdown)
			s.wg.Wait()
		}
	}
	return nil
}

// startWorker runs the background synchronization loop.
// Strategy: Sequential processing to maintain causal ordering per user.
func (s *Storage) startWorker() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case job := <-s.syncQueue:
				if err := job(); err != nil {
					if s.conf.AsyncErrorHandler != nil {
						s.conf.AsyncErrorHandler(fmt.Errorf("tiered sync failed: %w", err))
					}
				}
			case <-s.shutdown:
				// Drain queue on shutdown (best effort)
				for {
					select {
					case job := <-s.syncQueue:
						// Best effort - ignore errors during shutdown
						_ = job() //nolint:errcheck // Best effort during shutdown
					default:
						return
					}
				}
			}
		}
	}()
}

// --- Strategy: Read-Through (Hot → Cold → Populate Hot) ---

// GetEntitlement implements goquota.Storage with read-through strategy.
func (s *Storage) GetEntitlement(ctx context.Context, userID string) (*goquota.Entitlement, error) {
	// 1. Try Hot
	ent, err := s.hot.GetEntitlement(ctx, userID)
	if err == nil {
		return ent, nil
	}

	// 2. Try Cold (Source of Truth)
	ent, err = s.cold.GetEntitlement(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 3. Populate Hot (Read-Repair)
	// We ignore errors here as it's just a cache fill
	_ = s.hot.SetEntitlement(ctx, ent) //nolint:errcheck // Cache fill - errors are non-critical

	return ent, nil
}

// GetUsage implements goquota.Storage with read-through strategy.
func (s *Storage) GetUsage(
	ctx context.Context,
	userID, resource string,
	period goquota.Period,
) (*goquota.Usage, error) {
	// 1. Try Hot
	usage, err := s.hot.GetUsage(ctx, userID, resource, period)
	if err == nil && usage != nil {
		return usage, nil
	}

	// 2. Try Cold
	usage, err = s.cold.GetUsage(ctx, userID, resource, period)
	if err != nil {
		return nil, err
	}

	// 3. Populate Hot
	if usage != nil {
		_ = s.hot.SetUsage(ctx, userID, resource, usage, period) //nolint:errcheck // Cache fill - errors are non-critical
	}

	return usage, nil
}

// GetRefundRecord implements goquota.Storage with read-through strategy.
// Critical: Must check Hot first for idempotency during async sync lag.
func (s *Storage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*goquota.RefundRecord, error) {
	// Try Hot first
	rec, err := s.hot.GetRefundRecord(ctx, idempotencyKey)
	if err == nil && rec != nil {
		return rec, nil
	}
	// Try Cold
	return s.cold.GetRefundRecord(ctx, idempotencyKey)
}

// GetConsumptionRecord implements goquota.Storage with read-through strategy.
// Critical: Must check Hot first because ConsumeQuota writes to Hot immediately
// but syncs to Cold asynchronously. This ensures idempotency checks work correctly
// during the async sync lag window.
func (s *Storage) GetConsumptionRecord(ctx context.Context, idempotencyKey string) (*goquota.ConsumptionRecord, error) {
	// Try Hot first (Crucial for async consumption consistency)
	rec, err := s.hot.GetConsumptionRecord(ctx, idempotencyKey)
	if err == nil && rec != nil {
		return rec, nil
	}
	// Try Cold
	return s.cold.GetConsumptionRecord(ctx, idempotencyKey)
}

// --- Strategy: Write-Through (Cold → Hot) ---
// Critical data must be durable first.

// SetEntitlement implements goquota.Storage with write-through strategy.
func (s *Storage) SetEntitlement(ctx context.Context, ent *goquota.Entitlement) error {
	// 1. Write Cold (Durability)
	if err := s.cold.SetEntitlement(ctx, ent); err != nil {
		return err
	}
	// 2. Write Hot (Availability)
	// If Hot fails, we log it but don't fail the operation since Cold succeeded
	_ = s.hot.SetEntitlement(ctx, ent) //nolint:errcheck // Best effort - Cold is source of truth
	return nil
}

// SetUsage implements goquota.Storage with write-through strategy.
func (s *Storage) SetUsage(
	ctx context.Context,
	userID, resource string,
	usage *goquota.Usage,
	period goquota.Period,
) error {
	// 1. Write Cold (Durability)
	if err := s.cold.SetUsage(ctx, userID, resource, usage, period); err != nil {
		return err
	}
	// 2. Write Hot (Availability)
	_ = s.hot.SetUsage(ctx, userID, resource, usage, period) //nolint:errcheck // Best effort - Cold is source of truth
	return nil
}

// ApplyTierChange implements goquota.Storage with write-through strategy.
func (s *Storage) ApplyTierChange(ctx context.Context, req *goquota.TierChangeRequest) error {
	// 1. Write Cold (Durability)
	if err := s.cold.ApplyTierChange(ctx, req); err != nil {
		return err
	}
	// 2. Write Hot (Availability)
	_ = s.hot.ApplyTierChange(ctx, req) //nolint:errcheck // Best effort - Cold is source of truth
	return nil
}

// AddLimit implements goquota.Storage with write-through strategy.
func (s *Storage) AddLimit(
	ctx context.Context,
	userID, resource string,
	amount int,
	period goquota.Period,
	idempotencyKey string,
) error {
	// Critical financial data: Write Cold first
	if err := s.cold.AddLimit(ctx, userID, resource, amount, period, idempotencyKey); err != nil {
		return err
	}
	// Best effort update to Hot to reflect new limit immediately
	// Note: If idempotency key was used, Hot might return ErrIdempotencyKeyExists,
	// but we ignore Hot errors since Cold succeeded (the source of truth)
	//nolint:errcheck // Best effort - Cold is source of truth
	_ = s.hot.AddLimit(ctx, userID, resource, amount, period, idempotencyKey)
	return nil
}

// SubtractLimit implements goquota.Storage with write-through strategy.
func (s *Storage) SubtractLimit(
	ctx context.Context,
	userID, resource string,
	amount int,
	period goquota.Period,
	idempotencyKey string,
) error {
	// 1. Write Cold (Durability)
	if err := s.cold.SubtractLimit(ctx, userID, resource, amount, period, idempotencyKey); err != nil {
		return err
	}
	// 2. Write Hot (Availability)
	//nolint:errcheck // Best effort - Cold is source of truth
	_ = s.hot.SubtractLimit(ctx, userID, resource, amount, period, idempotencyKey)
	return nil
}

// RefundQuota implements goquota.Storage with write-through strategy.
func (s *Storage) RefundQuota(ctx context.Context, req *goquota.RefundRequest) error {
	// 1. Write Cold (Financial record - durability first)
	if err := s.cold.RefundQuota(ctx, req); err != nil {
		return err
	}
	// 2. Write Hot (Access control - availability)
	_ = s.hot.RefundQuota(ctx, req) //nolint:errcheck // Best effort - Cold is source of truth
	return nil
}

// --- Strategy: Hot-Primary / Async Audit ---
// High frequency operations optimized for latency.

// ConsumeQuota implements goquota.Storage with hot-primary/async-audit strategy.
func (s *Storage) ConsumeQuota(ctx context.Context, req *goquota.ConsumeRequest) (int, error) {
	// 1. Enforce Quota on Hot Store (Atomic, Fast)
	newUsed, err := s.hot.ConsumeQuota(ctx, req)
	if err != nil {
		return newUsed, err
	}

	// 2. Sync to Cold Store (Audit Trail)
	if s.conf.AsyncUsageSync {
		// Clone request to avoid race conditions if caller modifies it
		reqClone := *req

		// Attempt to enqueue non-blocking
		select {
		case s.syncQueue <- func() error {
			// Context background ensures completion even if request cancels
			_, err := s.cold.ConsumeQuota(context.Background(), &reqClone)
			return err
		}:
		default:
			if s.conf.AsyncErrorHandler != nil {
				s.conf.AsyncErrorHandler(errors.New("tiered storage: sync queue full, dropping cold write"))
			}
		}
	} else {
		// Synchronous fallback (safe mode)
		if _, err := s.cold.ConsumeQuota(ctx, req); err != nil {
			// Note: We return success because Hot succeeded and enforced the limit.
			// Ideally we should rollback Hot, but that's complex.
			// We log the inconsistency instead.
			if s.conf.AsyncErrorHandler != nil {
				s.conf.AsyncErrorHandler(fmt.Errorf("tiered storage: sync cold write failed: %w", err))
			}
		}
	}

	return newUsed, nil
}

// --- Strategy: Hot-Only ---
// Ephemeral data requiring extreme speed.

// CheckRateLimit implements goquota.Storage with hot-only strategy.
func (s *Storage) CheckRateLimit(
	ctx context.Context,
	req *goquota.RateLimitRequest,
) (allowed bool, remaining int, resetTime time.Time, err error) {
	return s.hot.CheckRateLimit(ctx, req)
}

// RecordRateLimitRequest implements goquota.Storage with hot-only strategy.
func (s *Storage) RecordRateLimitRequest(ctx context.Context, req *goquota.RateLimitRequest) error {
	return s.hot.RecordRateLimitRequest(ctx, req)
}

// --- TimeSource Support ---

// Now uses Hot store time for consistency (usually Redis TIME).
// Falls back to Cold if Hot doesn't support it, then local time.
func (s *Storage) Now(ctx context.Context) (time.Time, error) {
	// Prefer Hot store time (Redis) as it governs rate limits and high-freq usage
	if ts, ok := s.hot.(goquota.TimeSource); ok {
		return ts.Now(ctx)
	}
	// Fallback to Cold if Hot doesn't support it (unlikely for Redis)
	if ts, ok := s.cold.(goquota.TimeSource); ok {
		return ts.Now(ctx)
	}
	return time.Now().UTC(), nil
}
