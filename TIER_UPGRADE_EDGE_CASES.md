# Tier Upgrade Cache Bug - Edge Cases Analysis

## Summary

After reviewing the tier upgrade cache bug fix, several edge cases were identified that may not be fully covered by the current implementation.

## Current Implementation

The fix recalculates limits from the current tier with these exceptions:
1. **Forever periods**: Preserve cached limit (purchased credits)
2. **Prorated limits**: If `cached.Limit > tierConfigLimit && tierConfigLimit > 0`, preserve it (assumed to be prorated from `ApplyTierChange`)

## Identified Edge Cases

### ❌ **Edge Case 1: Tier Downgrade with Stale Cache**

**Scenario:**
- User has premium tier (limit=100) cached
- User downgrades to free tier (limit=5) via `SetEntitlement`
- Cache still has old limit=100 from premium tier
- Current logic: `100 > 5 && 5 > 0` = **true** → preserves old limit=100 ❌

**Problem:**
The condition `cached.Limit > tierConfigLimit` assumes higher limits are prorated, but after a downgrade, a higher cached limit is just stale data from the previous tier.

**Impact:**
- User downgrades but still sees old higher limit
- User can consume more than their new tier allows
- Billing/quota enforcement incorrect

**Fix Needed:**
Check if `cached.Tier != tier` first. If tiers don't match, always recalculate regardless of limit values.

---

### ⚠️ **Edge Case 2: Cached Tier Mismatch Not Checked**

**Scenario:**
- Cache has `Tier="premium"` with `Limit=100`
- Current entitlement is `Tier="free"` with config limit=5
- Code updates `cached.Tier = tier` but doesn't check if it was different before

**Problem:**
The code updates the tier field but doesn't use it to determine if the limit is stale. If `cached.Tier != tier` before the update, the limit is definitely stale.

**Impact:**
- Stale limits from previous tiers preserved incorrectly
- Similar to Edge Case 1

**Fix Needed:**
Check `cached.Tier != tier` before checking prorated limits. If tiers differ, always recalculate.

---

### ✅ **Edge Case 3: Unlimited (-1) Handling**

**Scenario:**
- Various combinations of unlimited (-1) and limited tiers

**Analysis:**
- If `tierConfigLimit = -1`: Condition `tierConfigLimit > 0` is false → correctly recalculates ✅
- If `cached.Limit = -1` and `tierConfigLimit = 5`: Condition `-1 > 5` is false → correctly recalculates ✅
- If `cached.Limit = 100` and `tierConfigLimit = -1`: Condition `100 > -1 && -1 > 0` is false → correctly recalculates ✅

**Status:** ✅ **Handled correctly**

---

### ⚠️ **Edge Case 4: Prorated Limit After Tier Change**

**Scenario:**
- User upgrades via `ApplyTierChange` which sets prorated limit (e.g., 150 when tier config is 100)
- Cache is populated with prorated limit=150 and new tier
- User then downgrades via `SetEntitlement` to a tier with limit=50
- Cache still has prorated limit=150 from previous tier

**Problem:**
The condition `150 > 50 && 50 > 0` = true, so it preserves the old prorated limit=150, which is wrong after a downgrade.

**Impact:**
- Old prorated limits preserved after tier changes
- Incorrect quota limits shown to users

**Fix Needed:**
Check tier match first. Only preserve prorated limits if tiers match.

---

### ⚠️ **Edge Case 5: Storage vs Cache Tier Mismatch**

**Scenario:**
- Storage has usage with `Tier="premium"` and `Limit=100`
- User's entitlement is updated to `Tier="free"` via `SetEntitlement`
- Cache is invalidated for entitlement but usage cache still has old data
- `GetQuota` hits usage cache with old tier/limit

**Problem:**
Usage cache is not invalidated on `SetEntitlement`, only entitlement cache is. So usage cache can have stale tier/limit data.

**Current Behavior:**
- Code does recalculate limit from current tier (good)
- But doesn't check if cached tier matches current tier

**Impact:**
- Less critical since limit is recalculated, but tier field might be stale
- Code does update `cached.Tier = tier` which fixes this

**Status:** ⚠️ **Partially handled** - limit is recalculated, tier is updated

---

### ⚠️ **Edge Case 6: Fallback Usage with Stale Tier**

**Scenario:**
- Storage fails, fallback returns usage with old tier/limit
- Same logic applies: `fallbackUsage.Limit > tierConfigLimit && tierConfigLimit > 0` preserves old limit

**Problem:**
Same issue as Edge Case 1 - doesn't check if fallback tier matches current tier.

**Impact:**
- Fallback data with stale limits preserved incorrectly

**Fix Needed:**
Same fix as Edge Case 1 - check tier match first.

---

## Recommended Fix

The core issue is that the code assumes higher limits are prorated, but doesn't verify that the cached tier matches the current tier. 

**Proposed Logic:**
```go
tierConfigLimit := m.getLimitForResource(resource, tier, periodType)
if periodType == PeriodTypeForever {
    // Forever periods: preserve cached limit
} else if cached.Tier != tier {
    // Tier mismatch - definitely stale, always recalculate
    cached.Limit = tierConfigLimit
} else if cached.Limit > tierConfigLimit && tierConfigLimit > 0 {
    // Tiers match and limit is higher - likely prorated, preserve it
} else {
    // Normal case: recalculate from tier config
    cached.Limit = tierConfigLimit
}
```

**Key Change:**
Add `cached.Tier != tier` check before checking for prorated limits. This ensures:
1. Tier downgrades always recalculate limits
2. Stale cache from previous tiers is always corrected
3. Prorated limits are only preserved when tiers match

---

## Test Cases Needed

1. **Tier Downgrade with Cached Data**
   - Cache premium tier (limit=100)
   - Downgrade to free tier (limit=5)
   - Verify limit is recalculated to 5, not preserved as 100

2. **Tier Upgrade then Downgrade**
   - Upgrade via `ApplyTierChange` (prorated limit=150)
   - Downgrade via `SetEntitlement` (limit=50)
   - Verify limit is recalculated to 50, not preserved as 150

3. **Multiple Tier Changes**
   - Free → Premium → Free
   - Verify limits are always correct after each change

4. **Fallback with Tier Mismatch**
   - Storage fails, fallback returns old tier data
   - Verify limit is recalculated from current tier

---

## Priority

- **High**: Edge Cases 1, 2, 4, 6 (tier mismatch not checked)
- **Medium**: Edge Case 5 (partially handled)
- **Low**: Edge Case 3 (already handled correctly)

---

## Related Code Locations

- `pkg/goquota/manager.go:310-328` - Cache hit path
- `pkg/goquota/manager.go:335-352` - Singleflight cache check
- `pkg/goquota/manager.go:370-386` - Fallback usage path
- `pkg/goquota/manager.go:438-450` - Storage usage path

All four locations need the same fix.
