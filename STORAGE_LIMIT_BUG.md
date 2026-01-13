# Storage Limit Bug - Consume Uses Stored Limit Instead of Request Limit

## Problem

When a user upgrades/downgrades tiers, the storage layer (`ConsumeQuota`) uses the **stored limit from the database** instead of the **request limit** calculated by Manager. This causes:

- User upgrades to premium (limit=-1, unlimited)
- Manager correctly calculates limit=-1 and passes it to storage
- But Firestore/Postgres reads stored limit=5 from old tier
- Storage uses stored limit=5 instead of request limit=-1
- Consume fails with "quota exceeded" even though limit should be unlimited

## Root Cause

### Firestore (firestore.go:293-296)
```go
storedLimit := getInt(data, "limit")
if storedLimit > 0 {
    currentLimit = storedLimit  // ❌ Overwrites request limit!
}
```

### Postgres (postgres.go:354-370)
```go
var limitAmount int64
err = tx.QueryRow(ctx, `SELECT ... limit_amount ...`).Scan(&currentUsed, &limitAmount)
// ...
if limitAmount != -1 && newUsed > limitAmount {  // ❌ Uses stored limit, not req.Limit!
    return int(currentUsed), goquota.ErrQuotaExceeded
}
```

### Redis (redis.go:164)
✅ **OK** - Uses request limit from ARGV[2] (req.Limit)

## Fix

Storage should **always use the request limit** since Manager already calculated it correctly from the current tier. The stored limit may be stale from a previous tier.

**Firestore**: Use `req.Limit` instead of overwriting with stored limit
**Postgres**: Use `req.Limit` instead of `limitAmount` from database

## Impact

- **Critical**: Users can't consume quota after tier upgrades/downgrades
- **Affects**: Firestore and Postgres storage backends
- **Not affected**: Redis (already correct)
