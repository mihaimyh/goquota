package goquota_test

import (
	"context"
	"testing"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A refund replay has to be distinguishable from an applied refund.
//
// Refund returns only an error, so a caller that logs "refunded" cannot tell that
// an earlier identical refund had already been applied and nothing moved - the
// same opacity that hid a "the quota never moves" report on the consume side.
func TestRefundWithResult_ReportsReplay(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 5)
	const userID = "refund-replay-user"

	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	_, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily,
		goquota.WithIdempotencyKey("consume-for-refund"))
	require.NoError(t, err)

	request := func() *goquota.RefundRequest {
		return &goquota.RefundRequest{
			UserID:         userID,
			Resource:       "receipt_scan",
			Amount:         1,
			PeriodType:     goquota.PeriodTypeDaily,
			IdempotencyKey: "refund-once",
			Reason:         "scan_failed",
		}
	}

	first, err := manager.RefundWithResult(ctx, request())
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.False(t, first.Replayed, "the first refund is applied")
	assert.Equal(t, 1, first.Amount)
	assert.Equal(t, goquota.PeriodTypeDaily, first.Period)

	replay, err := manager.RefundWithResult(ctx, request())
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.True(t, replay.Replayed, "a second refund under the same key is a replay")
	assert.Equal(t, 0, replay.Amount, "a replay moves nothing")

	usage, err := manager.GetQuota(ctx, userID, "receipt_scan", goquota.PeriodTypeDaily)
	require.NoError(t, err)
	assert.Equal(t, 0, usage.Used, "a replayed refund must not credit twice")
}

// Refund keeps its signature and behaviour; the result is simply discarded.
func TestRefund_StillReturnsOnlyAnError(t *testing.T) {
	ctx := context.Background()
	manager := newOverflowTestManager(t, 5)
	const userID = "refund-legacy-user"

	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: userID,
		Tier:   "free",
	}))
	_, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily)
	require.NoError(t, err)

	var refundErr error = manager.Refund(ctx, &goquota.RefundRequest{
		UserID:         userID,
		Resource:       "receipt_scan",
		Amount:         1,
		PeriodType:     goquota.PeriodTypeDaily,
		IdempotencyKey: "refund-legacy",
	})
	require.NoError(t, refundErr)
}
