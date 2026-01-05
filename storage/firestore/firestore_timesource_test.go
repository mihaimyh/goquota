//go:build integration
// +build integration

package firestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage_Now(t *testing.T) {
	client := setupFirestoreClient(t)
	defer client.Close()

	ctx := context.Background()

	config := Config{
		EntitlementsCollection: "test_entitlements",
		UsageCollection:        "test_usage",
		RefundsCollection:      "test_refunds",
		ConsumptionsCollection: "test_consumptions",
	}

	storage, err := New(client, config)
	require.NoError(t, err)

	t.Run("get server time from Firestore", func(t *testing.T) {
		serverTime, err := storage.Now(ctx)
		require.NoError(t, err)

		// Should be within a few seconds of local time
		localTime := time.Now().UTC()
		diff := serverTime.Sub(localTime)
		if diff < 0 {
			diff = -diff
		}
		assert.Less(t, diff, 10*time.Second, "Server time should be close to local time")
	})

	t.Run("server time is UTC", func(t *testing.T) {
		serverTime, err := storage.Now(ctx)
		require.NoError(t, err)
		assert.Equal(t, time.UTC, serverTime.Location())
	})

	t.Run("multiple calls return consistent time", func(t *testing.T) {
		time1, err := storage.Now(ctx)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		time2, err := storage.Now(ctx)
		require.NoError(t, err)

		// time2 should be after or equal to time1
		assert.True(t, time2.After(time1) || time2.Equal(time1))
	})
}
