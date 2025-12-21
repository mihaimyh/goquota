package memory

import (
"context"
"testing"
"time"

"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestStorage_GetSetEntitlement(t *testing.T) {
storage := New()
ctx := context.Background()

// Test getting non-existent entitlement
_, err := storage.GetEntitlement(ctx, "user1")
if err != goquota.ErrEntitlementNotFound {
t.Errorf("Expected ErrEntitlementNotFound, got %v", err)
}

// Test setting entitlement
ent := &goquota.Entitlement{
UserID:                "user1",
Tier:                  "scholar",
SubscriptionStartDate: time.Now().UTC(),
UpdatedAt:             time.Now().UTC(),
}

err = storage.SetEntitlement(ctx, ent)
if err != nil {
t.Fatalf("SetEntitlement failed: %v", err)
}

// Test getting entitlement
retrieved, err := storage.GetEntitlement(ctx, "user1")
if err != nil {
t.Fatalf("GetEntitlement failed: %v", err)
}

if retrieved.UserID != ent.UserID {
t.Errorf("UserID mismatch: got %s, want %s", retrieved.UserID, ent.UserID)
}
if retrieved.Tier != ent.Tier {
t.Errorf("Tier mismatch: got %s, want %s", retrieved.Tier, ent.Tier)
}
}

func TestStorage_GetUsage_NotFound(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
if err != nil {
t.Fatalf("GetUsage failed: %v", err)
}

// Should return nil for non-existent usage
if usage != nil {
t.Errorf("Expected nil usage, got %+v", usage)
}
}

func TestStorage_ConsumeQuota_Success(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

// Consume quota
req := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "api_calls",
Amount:   5,
Tier:     "scholar",
Period:   period,
Limit:    100,
}

err := storage.ConsumeQuota(ctx, req)
if err != nil {
t.Fatalf("ConsumeQuota failed: %v", err)
}

// Verify usage
usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
if err != nil {
t.Fatalf("GetUsage failed: %v", err)
}

if usage.Used != 5 {
t.Errorf("Expected 5 used, got %d", usage.Used)
}
if usage.Limit != 100 {
t.Errorf("Expected limit 100, got %d", usage.Limit)
}
}

func TestStorage_ConsumeQuota_Exceeds(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

// Try to consume more than limit
req := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "api_calls",
Amount:   150,
Tier:     "scholar",
Period:   period,
Limit:    100,
}

err := storage.ConsumeQuota(ctx, req)
if err != goquota.ErrQuotaExceeded {
t.Errorf("Expected ErrQuotaExceeded, got %v", err)
}
}

func TestStorage_ConsumeQuota_Multiple(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

// Consume multiple times
req := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "api_calls",
Amount:   10,
Tier:     "scholar",
Period:   period,
Limit:    100,
}

for i := 0; i < 5; i++ {
err := storage.ConsumeQuota(ctx, req)
if err != nil {
t.Fatalf("ConsumeQuota iteration %d failed: %v", i, err)
}
}

// Verify total usage
usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
if err != nil {
t.Fatalf("GetUsage failed: %v", err)
}

if usage.Used != 50 {
t.Errorf("Expected 50 used (5 x 10), got %d", usage.Used)
}
}

func TestStorage_ConsumeQuota_ZeroAmount(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

req := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "api_calls",
Amount:   0,
Tier:     "scholar",
Period:   period,
Limit:    100,
}

err := storage.ConsumeQuota(ctx, req)
if err != nil {
t.Errorf("ConsumeQuota with 0 amount should succeed, got %v", err)
}
}

func TestStorage_ConsumeQuota_NegativeAmount(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

req := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "api_calls",
Amount:   -10,
Tier:     "scholar",
Period:   period,
Limit:    100,
}

err := storage.ConsumeQuota(ctx, req)
if err != goquota.ErrInvalidAmount {
t.Errorf("Expected ErrInvalidAmount, got %v", err)
}
}

func TestStorage_ApplyTierChange(t *testing.T) {
storage := New()
ctx := context.Background()

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(30 * 24 * time.Hour),
Type:  goquota.PeriodTypeMonthly,
}

// Consume some quota first
consumeReq := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "audio_seconds",
Amount:   1000,
Tier:     "scholar",
Period:   period,
Limit:    3600,
}

err := storage.ConsumeQuota(ctx, consumeReq)
if err != nil {
t.Fatalf("ConsumeQuota failed: %v", err)
}

// Apply tier change
tierChangeReq := &goquota.TierChangeRequest{
UserID:      "user1",
OldTier:     "scholar",
NewTier:     "fluent",
Period:      period,
OldLimit:    3600,
NewLimit:    10000,
CurrentUsed: 1000,
}

err = storage.ApplyTierChange(ctx, tierChangeReq)
if err != nil {
t.Fatalf("ApplyTierChange failed: %v", err)
}

// Verify new limit
usage, err := storage.GetUsage(ctx, "user1", "audio_seconds", period)
if err != nil {
t.Fatalf("GetUsage failed: %v", err)
}

if usage.Limit != 10000 {
t.Errorf("Expected new limit 10000, got %d", usage.Limit)
}
if usage.Tier != "fluent" {
t.Errorf("Expected tier fluent, got %s", usage.Tier)
}
}

func TestStorage_Clear(t *testing.T) {
storage := New()
ctx := context.Background()

// Add some data
ent := &goquota.Entitlement{
UserID:                "user1",
Tier:                  "scholar",
SubscriptionStartDate: time.Now().UTC(),
UpdatedAt:             time.Now().UTC(),
}
_ = storage.SetEntitlement(ctx, ent)

period := goquota.Period{
Start: time.Now().UTC(),
End:   time.Now().UTC().Add(24 * time.Hour),
Type:  goquota.PeriodTypeDaily,
}

req := &goquota.ConsumeRequest{
UserID:   "user1",
Resource: "api_calls",
Amount:   10,
Tier:     "scholar",
Period:   period,
Limit:    100,
}
_ = storage.ConsumeQuota(ctx, req)

// Clear storage
storage.Clear()

// Verify everything is cleared
_, err := storage.GetEntitlement(ctx, "user1")
if err != goquota.ErrEntitlementNotFound {
t.Errorf("Expected ErrEntitlementNotFound after Clear, got %v", err)
}

usage, err := storage.GetUsage(ctx, "user1", "api_calls", period)
if err != nil {
t.Fatalf("GetUsage failed: %v", err)
}
if usage != nil {
t.Errorf("Expected nil usage after Clear, got %+v", usage)
}
}