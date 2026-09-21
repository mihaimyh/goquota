package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestHandler_GetUsage_ActivePromotion(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}))

	expires := time.Now().UTC().Add(24 * time.Hour)
	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    userID,
		Tier:      "pro",
		ExpiresAt: expires,
		Source:    "support_comp",
	})
	require.NoError(t, err)

	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{testResource},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/usage", http.NoBody)
	w := httptest.NewRecorder()
	handler.GetUsage(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp UsageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "pro", resp.Tier, "response tier must reflect the promotion")
	assert.Equal(t, statusActive, resp.Status)
	require.NotNil(t, resp.Promotion)
	assert.Equal(t, "pro", resp.Promotion.Tier)
	assert.Equal(t, "support_comp", resp.Promotion.Source)
	assert.True(t, resp.Promotion.ExpiresAt.Equal(expires))
}

func TestHandler_GetUsage_ExpiredPromotionFallsBackToBaseTier(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()
	userID := testUserID

	// An already-expired promotion written directly (GrantPromotion rejects past
	// expiries) must not be reflected as the effective tier.
	past := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
		Promotion: &goquota.TierPromotion{
			Tier:      "pro",
			GrantedAt: past.Add(-time.Hour),
			ExpiresAt: past,
		},
	}))

	handler, err := NewHandler(Config{
		Manager:        manager,
		GetUserID:      func(_ *http.Request) string { return userID },
		KnownResources: []string{testResource},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/usage", http.NoBody)
	w := httptest.NewRecorder()
	handler.GetUsage(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp UsageResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "free", resp.Tier)
	assert.Equal(t, statusActive, resp.Status)
	assert.Nil(t, resp.Promotion)
}
