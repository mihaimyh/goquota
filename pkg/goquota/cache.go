package goquota

import (
	"sync"
	"time"
)

// Cache defines the interface for caching entitlements and usage data
// to reduce storage backend load and improve performance.
type Cache interface {
	// GetEntitlement retrieves a cached entitlement
	// Returns the entitlement and true if found, nil and false otherwise
	GetEntitlement(userID string) (*Entitlement, bool)

	// SetEntitlement stores an entitlement in the cache with TTL
	SetEntitlement(userID string, ent *Entitlement, ttl time.Duration)

	// InvalidateEntitlement removes an entitlement from the cache
	InvalidateEntitlement(userID string)

	// GetUsage retrieves cached usage data
	// Returns the usage and true if found, nil and false otherwise
	GetUsage(key string) (*Usage, bool)

	// SetUsage stores usage data in the cache with TTL
	SetUsage(key string, usage *Usage, ttl time.Duration)

	// InvalidateUsage removes usage data from the cache
	InvalidateUsage(key string)

	// Clear removes all entries from the cache
	Clear()

	// Stats returns cache statistics
	Stats() CacheStats
}

// CacheStats holds cache performance statistics
type CacheStats struct {
	EntitlementHits   int64
	EntitlementMisses int64
	UsageHits         int64
	UsageMisses       int64
	Evictions         int64
	Size              int
}

// cacheEntry wraps a cached value with expiration time
type cacheEntry struct {
	value      interface{}
	expiration time.Time
}

func (e *cacheEntry) isExpired() bool {
	return time.Now().After(e.expiration)
}

// NoopCache is a cache implementation that does nothing
// Used when caching is disabled
type NoopCache struct{}

// NewNoopCache creates a new no-op cache
func NewNoopCache() *NoopCache {
	return &NoopCache{}
}

func (c *NoopCache) GetEntitlement(userID string) (*Entitlement, bool) {
	return nil, false
}

func (c *NoopCache) SetEntitlement(userID string, ent *Entitlement, ttl time.Duration) {}

func (c *NoopCache) InvalidateEntitlement(userID string) {}

func (c *NoopCache) GetUsage(key string) (*Usage, bool) {
	return nil, false
}

func (c *NoopCache) SetUsage(key string, usage *Usage, ttl time.Duration) {}

func (c *NoopCache) InvalidateUsage(key string) {}

func (c *NoopCache) Clear() {}

func (c *NoopCache) Stats() CacheStats {
	return CacheStats{}
}

// LRUCache implements Cache using an in-memory LRU cache with TTL support
type LRUCache struct {
	entitlements    map[string]*cacheEntry
	usage           map[string]*cacheEntry
	maxEntitlements int
	maxUsage        int
	mu              sync.RWMutex
	entitlementHits int64
	entitlementMiss int64
	usageHits       int64
	usageMisses     int64
	evictions       int64
}

// NewLRUCache creates a new LRU cache with specified maximum sizes
func NewLRUCache(maxEntitlements, maxUsage int) *LRUCache {
	if maxEntitlements <= 0 {
		maxEntitlements = 1000 // default
	}
	if maxUsage <= 0 {
		maxUsage = 10000 // default
	}

	return &LRUCache{
		entitlements:    make(map[string]*cacheEntry, maxEntitlements),
		usage:           make(map[string]*cacheEntry, maxUsage),
		maxEntitlements: maxEntitlements,
		maxUsage:        maxUsage,
	}
}

func (c *LRUCache) GetEntitlement(userID string) (*Entitlement, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entitlements[userID]
	if !exists || entry.isExpired() {
		c.entitlementMiss++
		return nil, false
	}

	c.entitlementHits++
	// Return a copy to prevent external modifications
	ent := entry.value.(*Entitlement)
	return &Entitlement{
		UserID:                ent.UserID,
		Tier:                  ent.Tier,
		SubscriptionStartDate: ent.SubscriptionStartDate,
		ExpiresAt:             ent.ExpiresAt,
		UpdatedAt:             ent.UpdatedAt,
	}, true
}

func (c *LRUCache) SetEntitlement(userID string, ent *Entitlement, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if at capacity
	if len(c.entitlements) >= c.maxEntitlements {
		if _, exists := c.entitlements[userID]; !exists {
			// Evict oldest (simple strategy: evict first found)
			for key := range c.entitlements {
				delete(c.entitlements, key)
				c.evictions++
				break
			}
		}
	}

	c.entitlements[userID] = &cacheEntry{
		value:      ent,
		expiration: time.Now().Add(ttl),
	}
}

func (c *LRUCache) InvalidateEntitlement(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entitlements, userID)
}

func (c *LRUCache) GetUsage(key string) (*Usage, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.usage[key]
	if !exists || entry.isExpired() {
		c.usageMisses++
		return nil, false
	}

	c.usageHits++
	// Return a copy to prevent external modifications
	usage := entry.value.(*Usage)
	return &Usage{
		UserID:    usage.UserID,
		Resource:  usage.Resource,
		Used:      usage.Used,
		Limit:     usage.Limit,
		Period:    usage.Period,
		Tier:      usage.Tier,
		UpdatedAt: usage.UpdatedAt,
	}, true
}

func (c *LRUCache) SetUsage(key string, usage *Usage, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if at capacity
	if len(c.usage) >= c.maxUsage {
		if _, exists := c.usage[key]; !exists {
			// Evict oldest (simple strategy: evict first found)
			for k := range c.usage {
				delete(c.usage, k)
				c.evictions++
				break
			}
		}
	}

	c.usage[key] = &cacheEntry{
		value:      usage,
		expiration: time.Now().Add(ttl),
	}
}

func (c *LRUCache) InvalidateUsage(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.usage, key)
}

func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entitlements = make(map[string]*cacheEntry, c.maxEntitlements)
	c.usage = make(map[string]*cacheEntry, c.maxUsage)
}

func (c *LRUCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		EntitlementHits:   c.entitlementHits,
		EntitlementMisses: c.entitlementMiss,
		UsageHits:         c.usageHits,
		UsageMisses:       c.usageMisses,
		Evictions:         c.evictions,
		Size:              len(c.entitlements) + len(c.usage),
	}
}
