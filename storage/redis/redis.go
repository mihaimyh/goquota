// Package redis provides a Redis implementation of the goquota.Storage interface.
// This implementation uses atomic operations via Lua scripts for transaction safety.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/redis/go-redis/v9"
)

// Storage implements goquota.Storage using Redis
type Storage struct {
	client  redis.UniversalClient
	config  Config
	scripts map[string]*redis.Script
}

// Config holds Redis storage configuration
type Config struct {
	// KeyPrefix is prepended to all Redis keys (default: "goquota:")
	KeyPrefix string

	// EntitlementTTL is the TTL for entitlement keys (0 = no expiration)
	EntitlementTTL time.Duration

	// UsageTTL is the TTL for usage keys (0 = no expiration)
	UsageTTL time.Duration

	// MaxRetries is the maximum number of retry attempts (default: 3)
	MaxRetries int
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
	return Config{
		KeyPrefix:      "goquota:",
		EntitlementTTL: 24 * time.Hour,
		UsageTTL:       0, // Usage doesn't expire
		MaxRetries:     3,
	}
}

// New creates a new Redis storage adapter
// The client can be *redis.Client, *redis.ClusterClient, or *redis.Ring
func New(client redis.UniversalClient, config Config) (*Storage, error) {
	if client == nil {
		return nil, fmt.Errorf("redis client is required")
	}

	// Set defaults
	if config.KeyPrefix == "" {
		config.KeyPrefix = "goquota:"
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	s := &Storage{
		client:  client,
		config:  config,
		scripts: make(map[string]*redis.Script),
	}

	// Load Lua scripts
	s.loadScripts()

	return s, nil
}

// loadScripts loads and compiles Lua scripts for atomic operations
func (s *Storage) loadScripts() {
	// Consume quota atomically
	s.scripts["consume"] = redis.NewScript(`
		local key = KEYS[1]
		local amount = tonumber(ARGV[1])
		local limit = tonumber(ARGV[2])
		local data = ARGV[3]
		
		local current = redis.call('HGET', key, 'used')
		local currentUsed = 0
		if current then
			currentUsed = tonumber(current)
		end
		
		local newUsed = currentUsed + amount
		if newUsed > limit then
			return {currentUsed, 'quota_exceeded'}
		end
		
		redis.call('HSET', key, 'used', newUsed)
		redis.call('HSET', key, 'data', data)
		
		if tonumber(ARGV[4]) > 0 then
			redis.call('EXPIRE', key, tonumber(ARGV[4]))
		end
		
		return {newUsed, 'ok'}
	`)

	// Apply tier change atomically
	s.scripts["tierChange"] = redis.NewScript(`
		local key = KEYS[1]
		local newLimit = tonumber(ARGV[1])
		local data = ARGV[2]
		
		redis.call('HSET', key, 'limit', newLimit)
		redis.call('HSET', key, 'data', data)
		
		if tonumber(ARGV[3]) > 0 then
			redis.call('EXPIRE', key, tonumber(ARGV[3]))
		end
		
		return 'ok'
	`)

	// Refund quota atomically
	s.scripts["refund"] = redis.NewScript(`
		local usageKey = KEYS[1]
		local refundKey = KEYS[2]
		local amount = tonumber(ARGV[1])
		local refundData = ARGV[2]
		local ttl = tonumber(ARGV[3])
		
		-- Check idempotency
		if refundKey ~= "" then
			local exists = redis.call('EXISTS', refundKey)
			if exists == 1 then
				return 'idempotent'
			end
		end
		
		-- Get current usage
		local current = redis.call('HGET', usageKey, 'used')
		if not current then
			-- No usage to refund, but we should record the refund if key provided
			if refundKey ~= "" then
				redis.call('SET', refundKey, refundData)
				-- Audit logs usually don't expire, or have long TTL
			end
			return 'ok'
		end
		
		local currentUsed = tonumber(current)
		local newUsed = currentUsed - amount
		if newUsed < 0 then
			newUsed = 0
		end
		
		redis.call('HSET', usageKey, 'used', newUsed)
		
		-- Record refund
		if refundKey ~= "" then
			redis.call('SET', refundKey, refundData)
		end
		
		return 'ok'
	`)
}

// GetEntitlement implements goquota.Storage
func (s *Storage) GetEntitlement(ctx context.Context, userID string) (*goquota.Entitlement, error) {
	key := s.entitlementKey(userID)

	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, goquota.ErrEntitlementNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get entitlement: %w", err)
	}

	var ent goquota.Entitlement
	if err := json.Unmarshal(data, &ent); err != nil {
		return nil, fmt.Errorf("failed to unmarshal entitlement: %w", err)
	}

	return &ent, nil
}

// SetEntitlement implements goquota.Storage
func (s *Storage) SetEntitlement(ctx context.Context, ent *goquota.Entitlement) error {
	if ent == nil || ent.UserID == "" {
		return fmt.Errorf("invalid entitlement")
	}

	key := s.entitlementKey(ent.UserID)

	data, err := json.Marshal(ent)
	if err != nil {
		return fmt.Errorf("failed to marshal entitlement: %w", err)
	}

	if s.config.EntitlementTTL > 0 {
		err = s.client.Set(ctx, key, data, s.config.EntitlementTTL).Err()
	} else {
		err = s.client.Set(ctx, key, data, 0).Err()
	}

	if err != nil {
		return fmt.Errorf("failed to set entitlement: %w", err)
	}

	return nil
}

// GetUsage implements goquota.Storage
func (s *Storage) GetUsage(ctx context.Context, userID, resource string, period goquota.Period) (*goquota.Usage, error) {
	key := s.usageKey(userID, resource, period)

	// Get both data and current used amount
	results, err := s.client.HMGet(ctx, key, "data", "used").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	if len(results) != 2 || results[0] == nil {
		return nil, nil // No usage yet
	}

	dataStr, ok := results[0].(string)
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}

	var usage goquota.Usage
	if err := json.Unmarshal([]byte(dataStr), &usage); err != nil {
		return nil, fmt.Errorf("failed to unmarshal usage: %w", err)
	}

	// Update used amount from Redis counter if present
	if results[1] != nil {
		if usedStr, ok := results[1].(string); ok {
			var used int
			fmt.Sscanf(usedStr, "%d", &used)
			usage.Used = used
		}
	}

	return &usage, nil
}

// ConsumeQuota implements goquota.Storage with atomic consumption via Lua script
func (s *Storage) ConsumeQuota(ctx context.Context, req *goquota.ConsumeRequest) (int, error) {
	if req.Amount < 0 {
		return 0, goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return 0, nil // No-op
	}

	key := s.usageKey(req.UserID, req.Resource, req.Period)

	// Create usage object
	usage := &goquota.Usage{
		UserID:    req.UserID,
		Resource:  req.Resource,
		Used:      0, // Will be set by Lua script
		Limit:     req.Limit,
		Period:    req.Period,
		Tier:      req.Tier,
		UpdatedAt: time.Now().UTC(),
	}

	usageData, err := json.Marshal(usage)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal usage: %w", err)
	}

	ttl := int64(0)
	if s.config.UsageTTL > 0 {
		ttl = int64(s.config.UsageTTL.Seconds())
	}

	// Execute Lua script for atomic consumption
	result, err := s.scripts["consume"].Run(
		ctx,
		s.client,
		[]string{key},
		req.Amount,
		req.Limit,
		string(usageData),
		ttl,
	).Result()

	if err != nil {
		return 0, fmt.Errorf("failed to execute consume script: %w", err)
	}

	resultSlice, ok := result.([]interface{})
	if !ok || len(resultSlice) != 2 {
		return 0, fmt.Errorf("unexpected script result format")
	}

	// Convert to int64 first, then to int
	newUsedInt64, ok := resultSlice[0].(int64)
	if !ok {
		return 0, fmt.Errorf("failed to parse used amount")
	}
	newUsed := int(newUsedInt64)

	status, ok := resultSlice[1].(string)
	if !ok {
		return 0, fmt.Errorf("failed to parse status")
	}

	if status == "quota_exceeded" {
		return newUsed, goquota.ErrQuotaExceeded
	}

	return newUsed, nil
}

// ApplyTierChange implements goquota.Storage
func (s *Storage) ApplyTierChange(ctx context.Context, req *goquota.TierChangeRequest) error {
	key := s.usageKey(req.UserID, "audio_seconds", req.Period)

	// Create updated usage object
	usage := &goquota.Usage{
		UserID:    req.UserID,
		Resource:  "audio_seconds",
		Used:      req.CurrentUsed,
		Limit:     req.NewLimit,
		Period:    req.Period,
		Tier:      req.NewTier,
		UpdatedAt: time.Now().UTC(),
	}

	usageData, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("failed to marshal usage: %w", err)
	}

	ttl := int64(0)
	if s.config.UsageTTL > 0 {
		ttl = int64(s.config.UsageTTL.Seconds())
	}

	// Execute Lua script for atomic tier change
	_, err = s.scripts["tierChange"].Run(
		ctx,
		s.client,
		[]string{key},
		req.NewLimit,
		string(usageData),
		ttl,
	).Result()

	if err != nil {
		return fmt.Errorf("failed to execute tier change script: %w", err)
	}

	return nil
}

// SetUsage implements goquota.Storage
func (s *Storage) SetUsage(ctx context.Context, userID, resource string, usage *goquota.Usage, period goquota.Period) error {
	if usage == nil {
		return fmt.Errorf("usage is required")
	}

	key := s.usageKey(userID, resource, period)

	usageData, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("failed to marshal usage: %w", err)
	}

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key, "used", usage.Used)
	pipe.HSet(ctx, key, "data", string(usageData))

	if s.config.UsageTTL > 0 {
		pipe.Expire(ctx, key, s.config.UsageTTL)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set usage: %w", err)
	}

	return nil
}

// RefundQuota implements goquota.Storage
func (s *Storage) RefundQuota(ctx context.Context, req *goquota.RefundRequest) error {
	if req.Amount < 0 {
		return goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return nil // No-op
	}

	usageKey := s.usageKey(req.UserID, req.Resource, goquota.Period{Type: req.PeriodType}) // Note: Period start/end needed for key?
	// The UsageKey method assumes specific period start/end.
	// We need to reconstruct the period to match what ConsumeQuota used.
	// But Period is not fully in Request for RefundQuota from interface (it passed PeriodType).
	// Wait, req has PeriodType but not full period in standard struct unless we put it there?
	// In the Manager implementation (step 83), we constructed a refund request but the period logic was inside Manager.
	// Ideally the Request struct passed to Storage should have the Period if needed for key generation.
	// Looking at manager.go changes:
	// The RefundRequest struct in types.go doesn't have Period field, just PeriodType.
	// BUT, Manager logic (Step 83) does:
	// period = ...
	// req := &RefundRequest{ ... } -> this is creating a NEW struct instance?
	// The `types.RefundRequest` struct definition:
	// type RefundRequest struct {
	//     UserID         string
	//     Resource       string
	//     Amount         int
	//     PeriodType     PeriodType
	//     IdempotencyKey string
	//     Reason         string
	//     Metadata       map[string]string
	// }
	// It relies on Storage implementation to know how to locate usage based on PeriodType?
	// But `usageKey` in Redis usually contains specific dates (e.g. `monthly:2023-10-01`).
	// If `RefundRequest` lacks the specific period dates, Storage can't easily reconstruction the key unless it calculates current cycle.
	// Which Manager did. But Manager didn't pass it in `RefundRequest` struct because struct doesn't have it?
	// Wait, `types.go` modification (Step 80) isn't fully visible here.
	// I should check `types.go`. If `RefundRequest` doesn't support Period dates, I need to recalculate them in Storage like Memory did.
	// Memory storage did recalculate period. Firestore did too.
	// So I will duplicate that logic here.

	// Calculate period
	var period goquota.Period
	now := time.Now().UTC()
	if !req.Period.Start.IsZero() {
		period = req.Period
	} else {
		switch req.PeriodType {
		case goquota.PeriodTypeMonthly:
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

	usageKey = s.usageKey(req.UserID, req.Resource, period)

	refundKey := ""
	if req.IdempotencyKey != "" {
		refundKey = s.refundKey(req.IdempotencyKey)
	}

	// Prepare refund record data
	record := &goquota.RefundRecord{
		RefundID:       req.IdempotencyKey,
		UserID:         req.UserID,
		Resource:       req.Resource,
		Amount:         req.Amount,
		Period:         period,
		Timestamp:      now,
		IdempotencyKey: req.IdempotencyKey,
		Reason:         req.Reason,
		Metadata:       req.Metadata,
	}

	refundData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal refund record: %w", err)
	}

	ttl := int64(0)
	if s.config.UsageTTL > 0 {
		ttl = int64(s.config.UsageTTL.Seconds())
	}

	// Execute Lua script
	res, err := s.scripts["refund"].Run(
		ctx,
		s.client,
		[]string{usageKey, refundKey},
		req.Amount,
		string(refundData),
		ttl,
	).Result()

	if err != nil {
		return fmt.Errorf("failed to execute refund script: %w", err)
	}

	if res == "idempotent" {
		// Already processed
		return nil
	}

	return nil
}

// GetRefundRecord implements goquota.Storage
func (s *Storage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*goquota.RefundRecord, error) {
	if idempotencyKey == "" {
		return nil, nil
	}

	key := s.refundKey(idempotencyKey)
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get refund record: %w", err)
	}

	var record goquota.RefundRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal refund record: %w", err)
	}

	return &record, nil
}

// entitlementKey generates the Redis key for an entitlement
func (s *Storage) entitlementKey(userID string) string {
	return fmt.Sprintf("%sentitlement:%s", s.config.KeyPrefix, userID)
}

// usageKey generates the Redis key for usage tracking
func (s *Storage) usageKey(userID, resource string, period goquota.Period) string {
	return fmt.Sprintf("%susage:%s:%s:%s", s.config.KeyPrefix, userID, resource, period.Key())
}

// refundKey generates the Redis key for refund records
func (s *Storage) refundKey(idempotencyKey string) string {
	return fmt.Sprintf("%srefund:%s", s.config.KeyPrefix, idempotencyKey)
}

// Close closes the Redis client connection
func (s *Storage) Close() error {
	return s.client.Close()
}

// Ping checks the Redis connection
func (s *Storage) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}
