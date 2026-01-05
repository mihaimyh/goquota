package redis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage_Now(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := New(client, DefaultConfig())
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("get server time", func(t *testing.T) {
		serverTime, err := storage.Now(ctx)
		require.NoError(t, err)

		// Should be within a few seconds of local time
		localTime := time.Now().UTC()
		diff := serverTime.Sub(localTime)
		if diff < 0 {
			diff = -diff
		}
		assert.Less(t, diff, 5*time.Second, "Server time should be close to local time")
	})

	t.Run("server time is UTC", func(t *testing.T) {
		serverTime, err := storage.Now(ctx)
		require.NoError(t, err)
		// Verify it's in UTC zone (after conversion)
		assert.Equal(t, time.UTC, serverTime.Location())
	})

	t.Run("multiple calls return consistent time", func(t *testing.T) {
		time1, err := storage.Now(ctx)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		time2, err := storage.Now(ctx)
		require.NoError(t, err)

		// time2 should be after time1
		assert.True(t, time2.After(time1) || time2.Equal(time1))
	})
}
