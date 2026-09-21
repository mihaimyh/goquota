package tiered

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota/promotiontest"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestPromotionConformance(t *testing.T) {
	promotiontest.Run(t, func(t *testing.T) promotiontest.Store {
		storage, err := New(Config{Hot: memory.New(), Cold: memory.New()})
		require.NoError(t, err)
		t.Cleanup(func() { _ = storage.Close() })
		return storage
	})
}
