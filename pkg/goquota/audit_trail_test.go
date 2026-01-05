package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// MockAuditLogger implements AuditLogger for testing
type MockAuditLogger struct {
	entries []*goquota.AuditLogEntry
}

func (m *MockAuditLogger) LogAuditEntry(_ context.Context, entry *goquota.AuditLogEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *MockAuditLogger) GetAuditLogs(
	_ context.Context,
	filter goquota.AuditLogFilter,
) ([]*goquota.AuditLogEntry, error) {
	var results []*goquota.AuditLogEntry
	for _, entry := range m.entries {
		if filter.UserID != "" && entry.UserID != filter.UserID {
			continue
		}
		if filter.Resource != "" && entry.Resource != filter.Resource {
			continue
		}
		if filter.Action != "" && entry.Action != filter.Action {
			continue
		}
		if filter.StartTime != nil && entry.Timestamp.Before(*filter.StartTime) {
			continue
		}
		if filter.EndTime != nil && entry.Timestamp.After(*filter.EndTime) {
			continue
		}
		results = append(results, entry)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	return results, nil
}

func TestManager_AuditLogging(t *testing.T) {
	mockLogger := &MockAuditLogger{
		entries: make([]*goquota.AuditLogEntry, 0),
	}

	// Create a storage that implements AuditLogger
	storage := &AuditLoggerStorage{
		Storage:     memory.New(),
		auditLogger: mockLogger,
	}

	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	const (
		testUserID           = "user1"
		testResourceAPICalls = "api_calls"
	)

	ctx := context.Background()
	userID := testUserID
	resource := testResourceAPICalls

	t.Run("SetUsage logs audit entry", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 50)
		require.NoError(t, err)

		// Verify audit log was created
		assert.Greater(t, len(mockLogger.entries), 0)
		lastEntry := mockLogger.entries[len(mockLogger.entries)-1]
		assert.Equal(t, userID, lastEntry.UserID)
		assert.Equal(t, resource, lastEntry.Resource)
		assert.Equal(t, "admin_set", lastEntry.Action)
		assert.Equal(t, 50, lastEntry.Amount)
	})

	t.Run("GrantOneTimeCredit logs audit entry", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		require.NoError(t, err)

		// Verify audit log was created
		assert.Greater(t, len(mockLogger.entries), 1)
		lastEntry := mockLogger.entries[len(mockLogger.entries)-1]
		assert.Equal(t, "user2", lastEntry.UserID)
		assert.Equal(t, resource, lastEntry.Resource)
		assert.Equal(t, "admin_grant_credit", lastEntry.Action)
		assert.Equal(t, 25, lastEntry.Amount)
	})

	t.Run("GetAuditLogs filters by userID", func(t *testing.T) {
		filter := goquota.AuditLogFilter{
			UserID: "user1",
			Limit:  100,
		}

		logs, err := manager.GetAuditLogs(ctx, filter)
		require.NoError(t, err)
		assert.Greater(t, len(logs), 0)

		// All entries should be for user1
		for _, entry := range logs {
			assert.Equal(t, "user1", entry.UserID)
		}
	})

	t.Run("GetAuditLogs filters by action", func(t *testing.T) {
		filter := goquota.AuditLogFilter{
			Action: "admin_set",
			Limit:  100,
		}

		logs, err := manager.GetAuditLogs(ctx, filter)
		require.NoError(t, err)
		assert.Greater(t, len(logs), 0)

		// All entries should be admin_set
		for _, entry := range logs {
			assert.Equal(t, "admin_set", entry.Action)
		}
	})

	t.Run("GetAuditLogs with time filter", func(t *testing.T) {
		startTime := time.Now().UTC().Add(-1 * time.Hour)
		endTime := time.Now().UTC().Add(1 * time.Hour)

		filter := goquota.AuditLogFilter{
			StartTime: &startTime,
			EndTime:   &endTime,
			Limit:     100,
		}

		logs, err := manager.GetAuditLogs(ctx, filter)
		require.NoError(t, err)

		// All entries should be within time range
		for _, entry := range logs {
			assert.True(t, entry.Timestamp.After(startTime) || entry.Timestamp.Equal(startTime))
			assert.True(t, entry.Timestamp.Before(endTime) || entry.Timestamp.Equal(endTime))
		}
	})

	t.Run("GetAuditLogs returns error when storage doesn't implement AuditLogger", func(t *testing.T) {
		// Create manager with storage that doesn't implement AuditLogger
		memoryStorage := memory.New()
		manager2, err := goquota.NewManager(memoryStorage, &config)
		require.NoError(t, err)

		filter := goquota.AuditLogFilter{}
		_, err = manager2.GetAuditLogs(ctx, filter)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not implement AuditLogger")
	})
}

// AuditLoggerStorage wraps a Storage and implements AuditLogger
type AuditLoggerStorage struct {
	goquota.Storage
	auditLogger *MockAuditLogger
}

func (s *AuditLoggerStorage) LogAuditEntry(ctx context.Context, entry *goquota.AuditLogEntry) error {
	return s.auditLogger.LogAuditEntry(ctx, entry)
}

func (s *AuditLoggerStorage) GetAuditLogs(
	ctx context.Context,
	filter goquota.AuditLogFilter,
) ([]*goquota.AuditLogEntry, error) {
	return s.auditLogger.GetAuditLogs(ctx, filter)
}
