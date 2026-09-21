package redis

import (
	"testing"

	"github.com/mihaimyh/goquota/pkg/goquota/promotiontest"
)

func TestPromotionConformance(t *testing.T) {
	promotiontest.Run(t, func(t *testing.T) promotiontest.Store {
		return newPromotionTestStorage(t)
	})
}
