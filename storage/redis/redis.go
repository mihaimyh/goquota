// Package redis provides a Redis implementation of the goquota.Storage interface.
// This implementation uses atomic operations via Lua scripts for transaction safety.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mihaimyh/goquota/pkg/goquota"
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
		local usageKey = KEYS[1]
		local consumptionKey = KEYS[2]
		local amount = tonumber(ARGV[1])
		local limit = tonumber(ARGV[2])
		local data = ARGV[3]
		local ttl = tonumber(ARGV[4])
		local consumptionData = ARGV[5]
		local consumptionTTL = tonumber(ARGV[6])
		
		-- Check idempotency
		if consumptionKey ~= "" then
			local exists = redis.call('EXISTS', consumptionKey)
			if exists == 1 then
				-- Idempotency hit - return cached result
				local record = redis.call('GET', consumptionKey)
				if record then
					-- Parse JSON to get newUsed (cjson is available in Redis Lua)
					local cjson = cjson or require('cjson')
					local ok, recordData = pcall(cjson.decode, record)
					if ok and recordData and recordData.newUsed then
						return {tonumber(recordData.newUsed), 'ok'}
					end
					-- Fallback: if JSON parsing fails, still return idempotent (don't consume again)
					-- Get current usage as fallback
					local current = redis.call('HGET', usageKey, 'used')
					local currentUsed = 0
					if current then
						currentUsed = tonumber(current)
					end
					return {currentUsed, 'ok'}
				end
			end
		end
		
		local current = redis.call('HGET', usageKey, 'used')
		local currentUsed = 0
		if current then
			currentUsed = tonumber(current)
		end
		
		local newUsed = currentUsed + amount
		if newUsed > limit then
			return {currentUsed, 'quota_exceeded'}
		end
		
		redis.call('HSET', usageKey, 'used', newUsed)
		redis.call('HSET', usageKey, 'data', data)
		
		if ttl > 0 then
			redis.call('EXPIRE', usageKey, ttl)
		end
		
		-- Record consumption for idempotency
		if consumptionKey ~= "" and consumptionData ~= "" then
			redis.call('SET', consumptionKey, consumptionData)
			if consumptionTTL > 0 then
				redis.call('EXPIRE', consumptionKey, consumptionTTL)
			end
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
		local usageTTL = tonumber(ARGV[3])
		local refundTTL = tonumber(ARGV[4])
		
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
				if refundTTL > 0 then
					redis.call('EXPIRE', refundKey, refundTTL)
				end
			end
			return 'ok'
		end
		
		local currentUsed = tonumber(current)
		local newUsed = currentUsed - amount
		if newUsed < 0 then
			newUsed = 0
		end
		
		redis.call('HSET', usageKey, 'used', newUsed)
		if usageTTL > 0 then
			redis.call('EXPIRE', usageKey, usageTTL)
		end
		
		-- Record refund
		if refundKey ~= "" then
			redis.call('SET', refundKey, refundData)
			if refundTTL > 0 then
				redis.call('EXPIRE', refundKey, refundTTL)
			end
		end
		
		return 'ok'
	`)

	// Token bucket rate limiting
	s.scripts["tokenBucket"] = redis.NewScript(`
		local key = KEYS[1]
		local now = tonumber(ARGV[1])
		local rate = tonumber(ARGV[2])
		local window = tonumber(ARGV[3])
		local burst = tonumber(ARGV[4])
		local ttl = tonumber(ARGV[5])
		
		-- Get current state
		local data = redis.call('HMGET', key, 'tokens', 'lastRefill')
		local tokens = burst
		local lastRefill = now
		
		if data[1] and data[2] then
			tokens = tonumber(data[1]) or burst
			lastRefill = tonumber(data[2]) or now
		end
		
		-- Refill tokens based on elapsed time
		local elapsed = now - lastRefill
		if elapsed > 0 then
			local tokensToAdd = math.floor(rate * elapsed / window)
			if tokensToAdd > 0 then
				tokens = math.min(tokens + tokensToAdd, burst)
				lastRefill = now
			end
		end
		
		-- Check if we have tokens
		local allowed = 1
		local remaining = tokens
		local resetTime = now + window
		
		if tokens <= 0 then
			allowed = 0
			remaining = 0
			-- Calculate when next token will be available
			resetTime = lastRefill + math.ceil(window / rate)
		else
			-- Consume a token
			tokens = tokens - 1
			remaining = tokens
			
			-- Calculate reset time (when bucket will be full)
			if tokens < burst then
				local tokensNeeded = burst - tokens
				resetTime = now + math.ceil(tokensNeeded * window / rate)
			end
		end
		
		-- Update state
		redis.call('HMSET', key, 'tokens', tokens, 'lastRefill', lastRefill)
		if ttl > 0 then
			redis.call('EXPIRE', key, ttl)
		end
		
		return {allowed, remaining, resetTime}
	`)

	// Sliding window rate limiting
	s.scripts["slidingWindow"] = redis.NewScript(`
		local key = KEYS[1]
		local now = tonumber(ARGV[1])
		local limit = tonumber(ARGV[2])
		local window = tonumber(ARGV[3])
		local ttl = tonumber(ARGV[4])
		
		-- Remove timestamps outside the window
		local cutoff = now - window
		redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
		
		-- Count current requests in window
		local count = redis.call('ZCARD', key)
		
		local allowed = 1
		local remaining = limit - count
		local resetTime = now + window
		
		if count >= limit then
			allowed = 0
			remaining = 0
			-- Get oldest timestamp still in window
			local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
			if oldest and #oldest >= 2 then
				local oldestTime = tonumber(oldest[2])
				if oldestTime then
					resetTime = oldestTime + window
				end
			end
		else
			-- Add current timestamp
			redis.call('ZADD', key, now, now)
			remaining = limit - count - 1
			
			-- Get oldest timestamp for reset time
			local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
			if oldest and #oldest >= 2 then
				local oldestTime = tonumber(oldest[2])
				if oldestTime then
					resetTime = oldestTime + window
				end
			end
		end
		
		if ttl > 0 then
			redis.call('EXPIRE', key, ttl)
		end
		
		return {allowed, remaining, resetTime}
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
func (s *Storage) GetUsage(ctx context.Context, userID, resource string,
	period goquota.Period) (*goquota.Usage, error) {
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
			if _, err := fmt.Sscanf(usedStr, "%d", &used); err != nil {
				return nil, fmt.Errorf("failed to parse usage: %w", err)
			}
			usage.Used = used
		}
	}

	return &usage, nil
}

// prepareConsumptionRecord prepares the consumption record data for idempotency
func (s *Storage) prepareConsumptionRecord(req *goquota.ConsumeRequest) (string, error) {
	if req.IdempotencyKey == "" {
		return "", nil
	}
	record := &goquota.ConsumptionRecord{
		ConsumptionID:  req.IdempotencyKey,
		UserID:         req.UserID,
		Resource:       req.Resource,
		Amount:         req.Amount,
		Period:         req.Period,
		Timestamp:      time.Now().UTC(),
		IdempotencyKey: req.IdempotencyKey,
		NewUsed:        0, // Will be updated by script
	}
	recordData, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("failed to marshal consumption record: %w", err)
	}
	return string(recordData), nil
}

// parseConsumeResult parses the result from the consume Lua script
func parseConsumeResult(result interface{}) (newUsed int, status string, err error) {
	resultSlice, ok := result.([]interface{})
	if !ok || len(resultSlice) != 2 {
		err = fmt.Errorf("unexpected script result format")
		return
	}

	newUsedInt64, ok := resultSlice[0].(int64)
	if !ok {
		err = fmt.Errorf("failed to parse used amount")
		return
	}
	newUsed = int(newUsedInt64)

	status, ok = resultSlice[1].(string)
	if !ok {
		err = fmt.Errorf("failed to parse status")
		return
	}

	return
}

// updateConsumptionRecord updates the consumption record with the actual newUsed value
func (s *Storage) updateConsumptionRecord(
	ctx context.Context,
	req *goquota.ConsumeRequest,
	consumptionKey string,
	newUsed int,
) {
	if req.IdempotencyKey == "" || consumptionKey == "" {
		return
	}

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
	recordData, err := json.Marshal(record)
	if err != nil {
		return
	}

	// Use TTL from request, default to 24 hours if not set
	consumptionTTL := int64(24 * 60 * 60) // Default 24 hours
	if req.IdempotencyKeyTTL > 0 {
		consumptionTTL = int64(req.IdempotencyKeyTTL.Seconds())
	}
	ttl := time.Duration(consumptionTTL) * time.Second
	if err := s.client.Set(ctx, consumptionKey, string(recordData), ttl).Err(); err != nil {
		// Log but don't fail - consumption already succeeded
		_ = err
	}
}

// ConsumeQuota implements goquota.Storage with atomic consumption via Lua script
func (s *Storage) ConsumeQuota(ctx context.Context, req *goquota.ConsumeRequest) (int, error) {
	if req.Amount < 0 {
		return 0, goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return 0, nil // No-op
	}

	usageKey := s.usageKey(req.UserID, req.Resource, req.Period)
	consumptionKey := ""
	if req.IdempotencyKey != "" {
		consumptionKey = s.consumptionKey(req.IdempotencyKey)
	}

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
	// For forever periods, never set TTL (no expiration)
	if req.Period.Type != goquota.PeriodTypeForever && s.config.UsageTTL > 0 {
		ttl = int64(s.config.UsageTTL.Seconds())
	}

	consumptionData, err := s.prepareConsumptionRecord(req)
	if err != nil {
		return 0, err
	}

	// Use TTL from request, default to 24 hours if not set
	consumptionTTL := int64(24 * 60 * 60) // Default 24 hours
	if req.IdempotencyKeyTTL > 0 {
		consumptionTTL = int64(req.IdempotencyKeyTTL.Seconds())
	}

	// Execute Lua script for atomic consumption
	result, err := s.scripts["consume"].Run(
		ctx,
		s.client,
		[]string{usageKey, consumptionKey},
		req.Amount,
		req.Limit,
		string(usageData),
		ttl,
		consumptionData,
		consumptionTTL,
	).Result()

	if err != nil {
		return 0, fmt.Errorf("failed to execute consume script: %w", err)
	}

	newUsed, status, err := parseConsumeResult(result)
	if err != nil {
		return 0, err
	}

	if status == "quota_exceeded" {
		return newUsed, goquota.ErrQuotaExceeded
	}

	s.updateConsumptionRecord(ctx, req, consumptionKey, newUsed)

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
func (s *Storage) SetUsage(ctx context.Context, userID, resource string,
	usage *goquota.Usage, period goquota.Period) error {
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

	// For forever periods, never set TTL (no expiration)
	if period.Type != goquota.PeriodTypeForever && s.config.UsageTTL > 0 {
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

	// UsageKey calculation logic below handles the period, so we don't need to assign it here early
	// usageKey := s.usageKey(req.UserID, req.Resource, goquota.Period{Type: req.PeriodType})
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
	// If `RefundRequest` lacks the specific period dates, Storage can't easily
	// reconstruction the key unless it calculates current cycle.
	// Which Manager did. But Manager didn't pass it in `RefundRequest` struct because struct doesn't have it?
	// Wait, `types.go` modification (Step 80) isn't fully visible here.
	// I should check `types.go`. If `RefundRequest` doesn't support Period dates,
	// I need to recalculate them in Storage like Memory did.
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

	usageKey := s.usageKey(req.UserID, req.Resource, period)

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

	usageTTL := int64(0)
	if s.config.UsageTTL > 0 {
		usageTTL = int64(s.config.UsageTTL.Seconds())
	}

	// Use TTL from request, default to 24 hours if not set
	refundTTL := int64(24 * 60 * 60) // Default 24 hours
	if req.IdempotencyKeyTTL > 0 {
		refundTTL = int64(req.IdempotencyKeyTTL.Seconds())
	}

	// Execute Lua script
	res, err := s.scripts["refund"].Run(
		ctx,
		s.client,
		[]string{usageKey, refundKey},
		req.Amount,
		string(refundData),
		usageTTL,
		refundTTL,
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

// GetConsumptionRecord implements goquota.Storage
func (s *Storage) GetConsumptionRecord(ctx context.Context, idempotencyKey string) (*goquota.ConsumptionRecord, error) {
	if idempotencyKey == "" {
		return nil, nil
	}

	key := s.consumptionKey(idempotencyKey)
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get consumption record: %w", err)
	}

	var record goquota.ConsumptionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal consumption record: %w", err)
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

// consumptionKey generates the Redis key for consumption records
func (s *Storage) consumptionKey(idempotencyKey string) string {
	return fmt.Sprintf("%sconsumption:%s", s.config.KeyPrefix, idempotencyKey)
}

// rateLimitKey generates the Redis key for rate limiting
func (s *Storage) rateLimitKey(userID, resource string) string {
	return fmt.Sprintf("%sratelimit:%s:%s", s.config.KeyPrefix, userID, resource)
}

// topUpKey generates the Redis key for top-up idempotency records
func (s *Storage) topUpKey(idempotencyKey string) string {
	return fmt.Sprintf("%stopup:%s", s.config.KeyPrefix, idempotencyKey)
}

// AddLimit implements goquota.Storage
func (s *Storage) AddLimit(
	ctx context.Context, userID, resource string, amount int, period goquota.Period, idempotencyKey string,
) error {
	usageKey := s.usageKey(userID, resource, period)
	topUpKey := ""
	if idempotencyKey != "" {
		topUpKey = s.topUpKey(idempotencyKey)
	}

	// Lua script: Check idempotency, then increment limit atomically
	script := `
		-- 1. Check idempotency (if key provided)
		if #ARGV[1] > 0 then
			local exists = redis.call('EXISTS', ARGV[1])
			if exists == 1 then
				return {0, 'idempotent'} -- Already processed
			end
		end
		
		-- 2. Increment limit atomically
		local newLimit = redis.call('HINCRBY', KEYS[1], 'limit', ARGV[2])
		
		-- 3. Record idempotency key (if provided)
		if #ARGV[1] > 0 then
			redis.call('SET', ARGV[1], '1', 'EX', 86400) -- 24 hour TTL
		end
		
		-- 4. Set TTL for usage key (if not forever period)
		if tonumber(ARGV[3]) > 0 then
			redis.call('EXPIRE', KEYS[1], ARGV[3])
		end
		
		return {1, newLimit}
	`

	ttl := int64(0)
	if period.Type != goquota.PeriodTypeForever && s.config.UsageTTL > 0 {
		ttl = int64(s.config.UsageTTL.Seconds())
	}

	result, err := s.client.Eval(ctx, script, []string{usageKey}, topUpKey, amount, ttl).Result()
	if err != nil {
		return fmt.Errorf("failed to execute add limit script: %w", err)
	}

	res, ok := result.([]interface{})
	if !ok || len(res) < 1 {
		return fmt.Errorf("unexpected result type from add limit script")
	}
	status, ok := res[0].(int64)
	if !ok {
		return fmt.Errorf("unexpected status type from add limit script")
	}
	if status == 0 {
		return goquota.ErrIdempotencyKeyExists
	}

	return nil
}

// SubtractLimit implements goquota.Storage
func (s *Storage) SubtractLimit(
	ctx context.Context, userID, resource string, amount int, period goquota.Period, idempotencyKey string,
) error {
	usageKey := s.usageKey(userID, resource, period)
	refundKey := ""
	if idempotencyKey != "" {
		refundKey = s.refundKey(idempotencyKey)
	}

	// Lua script: Check idempotency, then decrement limit atomically
	script := `
		-- 1. Check idempotency (if key provided)
		if #ARGV[1] > 0 then
			local exists = redis.call('EXISTS', ARGV[1])
			if exists == 1 then
				return {0, 'idempotent'} -- Already processed
			end
		end
		
		-- 2. Decrement limit atomically with clamp to 0
		local current = redis.call('HGET', KEYS[1], 'limit') or 0
		local newLimit = math.max(0, tonumber(current) - tonumber(ARGV[2]))
		redis.call('HSET', KEYS[1], 'limit', newLimit)
		
		-- 3. Record idempotency key (if provided)
		if #ARGV[1] > 0 then
			redis.call('SET', ARGV[1], '1', 'EX', 86400) -- 24 hour TTL
		end
		
		return {1, newLimit}
	`

	result, err := s.client.Eval(ctx, script, []string{usageKey}, refundKey, amount).Result()
	if err != nil {
		return fmt.Errorf("failed to execute subtract limit script: %w", err)
	}

	res, ok := result.([]interface{})
	if !ok || len(res) < 1 {
		return fmt.Errorf("unexpected result type from subtract limit script")
	}
	status, ok := res[0].(int64)
	if !ok {
		return fmt.Errorf("unexpected status type from subtract limit script")
	}
	if status == 0 {
		return goquota.ErrIdempotencyKeyExists
	}

	return nil
}

// CheckRateLimit implements goquota.Storage
//
//nolint:gocritic // Named return values would reduce readability here
func (s *Storage) CheckRateLimit(ctx context.Context, req *goquota.RateLimitRequest) (bool, int, time.Time, error) {
	if req == nil {
		return false, 0, time.Time{}, fmt.Errorf("rate limit request is required")
	}

	key := s.rateLimitKey(req.UserID, req.Resource)
	nowUnix := req.Now.Unix()
	ttl := int64(req.Window.Seconds() * 2) // Store for 2x window to allow cleanup

	var result interface{}
	var err error

	switch req.Algorithm {
	case "token_bucket":
		burst := req.Burst
		if burst <= 0 {
			burst = req.Rate
		}
		result, err = s.scripts["tokenBucket"].Run(
			ctx,
			s.client,
			[]string{key},
			nowUnix,
			req.Rate,
			int64(req.Window.Seconds()),
			burst,
			ttl,
		).Result()
	case "sliding_window":
		result, err = s.scripts["slidingWindow"].Run(
			ctx,
			s.client,
			[]string{key},
			nowUnix,
			req.Rate,
			int64(req.Window.Seconds()),
			ttl,
		).Result()
	default:
		return false, 0, time.Time{}, fmt.Errorf("unknown rate limit algorithm: %s", req.Algorithm)
	}

	if err != nil {
		return false, 0, time.Time{}, fmt.Errorf("failed to execute rate limit script: %w", err)
	}

	// Type assert result to []interface{}
	resultSlice, ok := result.([]interface{})
	if !ok {
		return false, 0, time.Time{}, fmt.Errorf("unexpected result type from rate limit script: %T", result)
	}

	// Parse result: {allowed, remaining, resetTime}
	if len(resultSlice) != 3 {
		return false, 0, time.Time{}, fmt.Errorf("unexpected result format from rate limit script")
	}

	allowedInt, ok := resultSlice[0].(int64)
	if !ok {
		return false, 0, time.Time{}, fmt.Errorf("invalid allowed value")
	}
	allowed := allowedInt == 1

	remainingInt, ok := resultSlice[1].(int64)
	if !ok {
		return false, 0, time.Time{}, fmt.Errorf("invalid remaining value")
	}
	remaining := int(remainingInt)

	resetTimeInt, ok := resultSlice[2].(int64)
	if !ok {
		return false, 0, time.Time{}, fmt.Errorf("invalid reset time value")
	}
	resetTime := time.Unix(resetTimeInt, 0).UTC()

	return allowed, remaining, resetTime, nil
}

// RecordRateLimitRequest implements goquota.Storage
func (s *Storage) RecordRateLimitRequest(_ context.Context, _ *goquota.RateLimitRequest) error {
	// For sliding window, the timestamp is already recorded in CheckRateLimit
	// This method is a no-op for Redis since we use sorted sets which handle it atomically
	// For other algorithms, this is also a no-op
	return nil
}

// Close closes the Redis client connection
func (s *Storage) Close() error {
	return s.client.Close()
}

// Ping checks the Redis connection
func (s *Storage) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}
