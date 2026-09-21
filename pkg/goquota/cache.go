package goquota

import (
	"container/list"
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

// cacheEntry is the value stored in the intrusive LRU list.
type cacheEntry struct {
	key        string
	value      interface{}
	expiration time.Time
}

// NoopCache is a cache implementation that does nothing
// Used when caching is disabled
type NoopCache struct{}

// NewNoopCache creates a new no-op cache
func NewNoopCache() *NoopCache {
	return &NoopCache{}
}

func (c *NoopCache) GetEntitlement(_ string) (*Entitlement, bool) {
	return nil, false
}

func (c *NoopCache) SetEntitlement(_ string, _ *Entitlement, _ time.Duration) {}

func (c *NoopCache) InvalidateEntitlement(_ string) {}

func (c *NoopCache) GetUsage(_ string) (*Usage, bool) {
	return nil, false
}

func (c *NoopCache) SetUsage(_ string, _ *Usage, _ time.Duration) {}

func (c *NoopCache) InvalidateUsage(_ string) {}

func (c *NoopCache) Clear() {}

func (c *NoopCache) Stats() CacheStats {
	return CacheStats{}
}

// LRUCache implements Cache using an in-memory LRU cache with TTL support.
// Eviction is O(1): a doubly-linked list (container/list) tracks recency and
// the maps point at list elements.
type LRUCache struct {
	entitlements    map[string]*list.Element
	usage           map[string]*list.Element
	entitlementList *list.List
	usageList       *list.List
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
		entitlements:    make(map[string]*list.Element, maxEntitlements),
		usage:           make(map[string]*list.Element, maxUsage),
		entitlementList: list.New(),
		usageList:       list.New(),
		maxEntitlements: maxEntitlements,
		maxUsage:        maxUsage,
	}
}

func (c *LRUCache) GetEntitlement(userID string) (*Entitlement, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, exists := c.entitlements[userID]
	if !exists {
		c.entitlementMiss++
		return nil, false
	}
	entry, ok := el.Value.(*cacheEntry)
	if !ok {
		c.entitlementList.Remove(el)
		delete(c.entitlements, userID)
		c.entitlementMiss++
		return nil, false
	}
	if time.Now().After(entry.expiration) {
		// Reclaim the expired entry so TTL bounds memory as well as freshness.
		c.entitlementList.Remove(el)
		delete(c.entitlements, userID)
		c.entitlementMiss++
		return nil, false
	}

	c.entitlementList.MoveToFront(el)
	c.entitlementHits++

	// Return a shallow copy so callers cannot mutate the cached entry.
	// Copy the full struct; omitting fields (e.g. Timezone) makes cache hits
	// silently fall back to UTC daily boundaries.
	ent, ok := entry.value.(*Entitlement)
	if !ok {
		return nil, false
	}
	cp := *ent
	return &cp, true
}

func (c *LRUCache) SetEntitlement(userID string, ent *Entitlement, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, exists := c.entitlements[userID]; exists {
		if entry, ok := el.Value.(*cacheEntry); ok {
			entry.value = ent
			entry.expiration = time.Now().Add(ttl)
			c.entitlementList.MoveToFront(el)
			return
		}
		// Corrupt element: drop it and fall through to a fresh insert.
		c.entitlementList.Remove(el)
		delete(c.entitlements, userID)
	}

	if c.entitlementList.Len() >= c.maxEntitlements {
		if back := c.entitlementList.Back(); back != nil {
			if be, ok := back.Value.(*cacheEntry); ok {
				delete(c.entitlements, be.key)
			}
			c.entitlementList.Remove(back)
			c.evictions++
		}
	}

	entry := &cacheEntry{key: userID, value: ent, expiration: time.Now().Add(ttl)}
	c.entitlements[userID] = c.entitlementList.PushFront(entry)
}

func (c *LRUCache) InvalidateEntitlement(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, exists := c.entitlements[userID]; exists {
		c.entitlementList.Remove(el)
		delete(c.entitlements, userID)
	}
}

func (c *LRUCache) GetUsage(key string) (*Usage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, exists := c.usage[key]
	if !exists {
		c.usageMisses++
		return nil, false
	}
	entry, ok := el.Value.(*cacheEntry)
	if !ok {
		c.usageList.Remove(el)
		delete(c.usage, key)
		c.usageMisses++
		return nil, false
	}
	if time.Now().After(entry.expiration) {
		// Reclaim the expired entry so TTL bounds memory as well as freshness.
		c.usageList.Remove(el)
		delete(c.usage, key)
		c.usageMisses++
		return nil, false
	}

	c.usageList.MoveToFront(el)
	c.usageHits++

	// Return a copy to prevent external modifications
	usage, ok := entry.value.(*Usage)
	if !ok {
		return nil, false
	}
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

	if el, exists := c.usage[key]; exists {
		if entry, ok := el.Value.(*cacheEntry); ok {
			entry.value = usage
			entry.expiration = time.Now().Add(ttl)
			c.usageList.MoveToFront(el)
			return
		}
		// Corrupt element: drop it and fall through to a fresh insert.
		c.usageList.Remove(el)
		delete(c.usage, key)
	}

	if c.usageList.Len() >= c.maxUsage {
		if back := c.usageList.Back(); back != nil {
			if be, ok := back.Value.(*cacheEntry); ok {
				delete(c.usage, be.key)
			}
			c.usageList.Remove(back)
			c.evictions++
		}
	}

	entry := &cacheEntry{key: key, value: usage, expiration: time.Now().Add(ttl)}
	c.usage[key] = c.usageList.PushFront(entry)
}

func (c *LRUCache) InvalidateUsage(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, exists := c.usage[key]; exists {
		c.usageList.Remove(el)
		delete(c.usage, key)
	}
}

func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entitlements = make(map[string]*list.Element, c.maxEntitlements)
	c.usage = make(map[string]*list.Element, c.maxUsage)
	c.entitlementList = list.New()
	c.usageList = list.New()
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
