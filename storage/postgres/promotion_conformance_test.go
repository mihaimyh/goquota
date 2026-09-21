//go:build integration
// +build integration

package postgres

import (
	"testing"

	"github.com/mihaimyh/goquota/pkg/goquota/promotiontest"
)

func TestPromotionConformance(t *testing.T) {
	promotiontest.Run(t, func(t *testing.T) promotiontest.Store {
		storage := setupTestStorage(t)
		t.Cleanup(storage.Close)
		return storage
	})
}
