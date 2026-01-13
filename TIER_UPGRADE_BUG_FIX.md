# Tier Upgrade Cache Bug Fix - User Migration Guide

## Summary

A bug was fixed where users upgrading their subscription tier would see stale quota limits from their previous tier due to cached usage data. The fix ensures quota limits are always recalculated from the current tier, even when usage data is cached.

**Bug Fixed**: When a user upgraded from free tier (limit=5) to premium tier (limit=-1, unlimited), `GetQuota` would return the cached limit=5 instead of recalculating to -1.

## Impact Assessment

### What Was Affected
- Users who upgraded tiers while having cached usage data
- The issue only affected the **limit** returned by `GetQuota`, not the actual entitlement stored in your database
- Usage counts were always correct

### What Was NOT Affected
- Entitlement data in storage (Firestore/Redis/PostgreSQL) - always correct
- Actual quota consumption - always worked correctly
- Forever period credits - preserved correctly
- Prorated limits from `ApplyTierChange` - preserved correctly

## Action Required

### ✅ **No Action Required (Recommended)**

The fix is **backward compatible** and works automatically:

1. **Automatic Correction**: The fix recalculates limits from the current tier on every `GetQuota` call, even if cached data has stale limits. This means:
   - Existing cached data will be corrected automatically on the next `GetQuota` call
   - No data migration needed
   - No manual cache clearing required

2. **Natural Cache Expiration**: Cached usage data has a TTL (default: 1 minute for usage, configurable via `CacheConfig.UsageTTL`). Stale cache entries will expire naturally.

3. **Immediate Effect**: After upgrading to the fixed version, the next `GetQuota` call for any user will return the correct limit based on their current tier.

### Optional: Force Immediate Cache Refresh

If you want to ensure all cached data is refreshed immediately after deploying the fix, you have these options:

#### Option 1: Wait for Cache TTL (Recommended)
Simply wait for the cache TTL to expire (default: 1 minute for usage cache). All cached entries will refresh automatically.

#### Option 2: Restart Your Application
Restarting your application will clear all in-memory cache. This is the simplest way to force immediate refresh if you're using the default in-memory LRU cache.

#### Option 3: Programmatic Cache Clearing (If Needed)
If you have access to the cache instance and need to clear it programmatically:

```go
// Note: This requires access to the internal cache, which is not exposed in the Manager API
// The Manager doesn't expose a ClearCache method, so this is only possible if you
// maintain a reference to the cache instance separately
```

**Note**: The Manager API doesn't expose cache clearing methods, so restarting is the most practical option if immediate refresh is needed.

## Verification Steps

After deploying the fix, verify it's working correctly:

### 1. Test Tier Upgrade Flow

```go
ctx := context.Background()
userID := "test_user"
resource := "api_calls"

// Step 1: Set free tier
manager.SetEntitlement(ctx, &goquota.Entitlement{
    UserID:                userID,
    Tier:                  "free",
    SubscriptionStartDate: time.Now().UTC(),
    UpdatedAt:             time.Now().UTC(),
})

// Step 2: Get quota (should return limit=5 for free tier)
usage1, _ := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
fmt.Printf("Free tier limit: %d\n", usage1.Limit) // Should be 5

// Step 3: Upgrade to premium
manager.SetEntitlement(ctx, &goquota.Entitlement{
    UserID:                userID,
    Tier:                  "premium", // Assuming premium has limit=-1 (unlimited)
    SubscriptionStartDate: time.Now().UTC(),
    UpdatedAt:             time.Now().UTC(),
})

// Step 4: Get quota again (should return limit=-1, not cached limit=5)
usage2, _ := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
fmt.Printf("Premium tier limit: %d\n", usage2.Limit) // Should be -1 (unlimited)
fmt.Printf("Tier: %s\n", usage2.Tier) // Should be "premium"
```

### 2. Monitor Logs

Check your application logs for any users who might have been affected. Look for:
- Users who recently upgraded tiers
- Any "quota exceeded" errors that occurred shortly after tier upgrades
- These users should now see correct limits

### 3. Check Affected Users

If you have users who reported quota issues after upgrading:
1. Verify their current tier in your database/storage
2. Call `GetQuota` for those users - it should now return the correct limit
3. If they were blocked, they should now have access

## Technical Details

### What Changed

The fix modifies `GetQuota` to always recalculate the limit from the current tier configuration, even when returning cached usage data. This ensures tier upgrades are immediately reflected.

**Before**: Cached usage with old tier limit was returned as-is if limit > 0  
**After**: Limit is always recalculated from current tier (with exceptions for forever periods and prorated limits)

### Exceptions Preserved

The fix preserves these special cases:
1. **Forever Period Credits**: Stored limits for forever periods (purchased credits) are preserved
2. **Prorated Limits**: Limits set by `ApplyTierChange` (higher than tier config) are preserved

### Cache Behavior

- **Entitlement Cache**: Already invalidated on `SetEntitlement` (unchanged)
- **Usage Cache**: Now automatically corrects limits on read (new behavior)
- **Cache TTL**: Default 1 minute for usage, configurable via `CacheConfig.UsageTTL`

## Rollback Plan

If you need to rollback (not recommended):

1. The fix is read-only for storage - it only affects what's returned from `GetQuota`
2. Rolling back will restore the bug behavior (stale cached limits)
3. No data corruption risk - storage data is unchanged

## Support

If you encounter any issues after deploying this fix:

1. Check that you're using the latest version with the fix
2. Verify tier configuration is correct
3. Test with a known user who upgraded tiers
4. Check logs for any errors

## Version Information

- **Fix Applied In**: [Version number after fix is released]
- **Related Issue**: Tier upgrade cache bug
- **Test Coverage**: `TestManager_GetQuota_TierUpgrade_CacheBug` verifies the fix

---

**Bottom Line**: Deploy the fix and continue normal operations. The fix works automatically with no data migration or manual intervention required. Cached data will refresh naturally or on the next `GetQuota` call.
