package goquota_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestSupportsPromotions(t *testing.T) {
	assert.False(t, goquota.SupportsPromotions(nil), "nil storage has no capabilities")
	assert.True(t, goquota.SupportsPromotions(memory.New()))
	assert.False(t, goquota.SupportsPromotions(&noPromotionStorage{Storage: memory.New()}))
}

// TestSupportsPromotions_UnwrapsCircuitBreaker guards the boot-time capability
// check against a wrapper that always satisfies PromotionStore but forwards to
// an inner store that does not.
func TestSupportsPromotions_UnwrapsCircuitBreaker(t *testing.T) {
	capable := goquota.NewCircuitBreakerStorage(memory.New(),
		goquota.NewDefaultCircuitBreaker(5, time.Second, nil))
	assert.True(t, goquota.SupportsPromotions(capable))

	incapable := goquota.NewCircuitBreakerStorage(
		&noPromotionStorage{Storage: memory.New()},
		goquota.NewDefaultCircuitBreaker(5, time.Second, nil))
	assert.False(t, goquota.SupportsPromotions(incapable))
}

func TestManagerPromotionsSupported(t *testing.T) {
	manager, _ := newPromotionManager(t)
	assert.True(t, manager.PromotionsSupported())

	config := goquota.Config{
		DefaultTier: "free",
		Tiers:       map[string]goquota.TierConfig{"free": {Name: "free"}},
	}
	manager, err := goquota.NewManager(&noPromotionStorage{Storage: memory.New()}, &config)
	require.NoError(t, err)
	assert.False(t, manager.PromotionsSupported())
}
