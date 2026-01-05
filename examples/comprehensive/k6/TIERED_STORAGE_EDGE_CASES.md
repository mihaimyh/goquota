# Tiered Storage Edge Case Tests

This document describes the edge case tests for tiered storage integration (Redis Hot + PostgreSQL Cold).

## Running the Tests

```bash
# Run edge case tests
docker-compose run --rm -e K6_VUS=5 -e K6_DURATION=30s k6-load-test run /scripts/tiered-storage-edge-cases.js

# Or with custom parameters
docker-compose run --rm -e K6_VUS=10 -e K6_DURATION=60s k6-load-test run /scripts/tiered-storage-edge-cases.js
```

## Test Scenarios

### 1. Rapid Retries During Async Sync
**Purpose**: Tests that rapid retries during the async sync window are handled correctly.

**What it tests**:
- Multiple rapid requests during async sync window
- Quota accuracy after async sync completes
- Async sync queue handling under load

**Expected behavior**:
- All operations succeed
- Quota is accurately tracked after async sync completes
- No double-charging or lost operations

### 2. Read After Write Consistency
**Purpose**: Tests that reads immediately after writes reflect the changes (Hot store consistency).

**What it tests**:
- Read-through behavior (Hot → Cold → populate Hot)
- Immediate consistency after write (Hot store)
- Async sync doesn't affect read consistency

**Expected behavior**:
- Reads immediately after writes show updated quota
- Hot store provides immediate consistency
- Cold sync happens asynchronously without blocking reads

### 3. Concurrent Operations
**Purpose**: Tests atomic operations when multiple VUs operate on the same user.

**What it tests**:
- Atomic quota consumption under concurrent load
- Race condition handling
- Quota accuracy with concurrent operations

**Expected behavior**:
- All concurrent operations succeed
- Quota is accurately tracked (no double-charging)
- Atomic operations prevent race conditions

### 4. Quota Boundary Conditions
**Purpose**: Tests enforcement at exact limit boundaries.

**What it tests**:
- Consuming exactly at limit (should succeed)
- Consuming just over limit (should fail with 402)
- Remaining quota accuracy at boundaries

**Expected behavior**:
- Operations at limit succeed
- Operations over limit fail with proper error code
- Remaining quota is accurately reported

### 5. Rapid Consumption
**Purpose**: Tests async sync queue under high-frequency load.

**What it tests**:
- High-frequency consumption operations
- Async sync queue capacity
- Quota accuracy after many async operations

**Expected behavior**:
- All operations succeed
- Async sync queue handles high-frequency load
- Quota is accurate after async sync completes

### 6. Mixed Read/Write Operations
**Purpose**: Tests interleaved operations maintain consistency.

**What it tests**:
- Interleaved reads and writes
- Read consistency during writes
- Hot store consistency

**Expected behavior**:
- Reads always see latest writes
- Consistency maintained during interleaved operations
- Hot store provides immediate consistency

### 7. Long-Running Consistency
**Purpose**: Tests that quota is accurate after async sync completes.

**What it tests**:
- Quota accuracy after many async operations
- Long-running consistency between Hot and Cold
- Async sync completion verification

**Expected behavior**:
- Quota is accurate after async sync completes
- Hot and Cold stores are eventually consistent
- No lost operations during async sync

## Interpreting Results

### Expected Failures

Some test failures are **expected** and indicate the system is working correctly:

1. **Quota Exhaustion**: If users have consumed their quota, tests will fail with 402 errors. This is expected behavior - the tiered storage is correctly enforcing limits.

2. **Rate Limiting**: If users hit rate limits, tests will fail with 429 errors. This is expected - rate limiting is working correctly.

### Success Criteria

- **Pass Rate**: Aim for >80% pass rate on edge cases that have sufficient quota
- **Consistency**: All quota reads should be consistent
- **No Double-Charging**: Quota should never be consumed twice for the same operation
- **Async Sync**: All operations should eventually sync to Cold store

### Metrics to Monitor

- `edge_case_passed`: Number of edge cases that passed
- `edge_case_failed`: Number of edge cases that failed
- `read_after_write_consistency`: Read consistency tests passed
- `concurrent_operation_success`: Concurrent operation tests passed
- `quota_boundary_test`: Boundary condition tests passed
- `rapid_consumption_test`: Rapid consumption tests passed
- `async_sync_lag_test`: Async sync lag tests passed

## Manual Edge Case Testing

Some edge cases require manual testing with service manipulation:

### Cold Store Failure
**Test**: Stop PostgreSQL while Redis is running
```bash
docker-compose stop postgres
# Run operations - should work (Hot store available)
# Check that operations are queued for async sync
docker-compose start postgres
# Verify operations sync to Cold store
```

**Expected behavior**:
- Operations succeed (Hot store available)
- Async sync queue fills up
- Operations sync to Cold when it comes back online

### Hot Store Failure
**Test**: Stop Redis while PostgreSQL is running
```bash
docker-compose stop redis
# Run operations - should fall back to Cold store
docker-compose start redis
# Verify Hot store is repopulated
```

**Expected behavior**:
- Operations fall back to Cold store (read-through)
- Slower performance (Cold store is slower)
- Hot store repopulated when Redis comes back

### Both Stores Down
**Test**: Stop both Redis and PostgreSQL
```bash
docker-compose stop redis postgres
# Run operations - should use fallback strategies
```

**Expected behavior**:
- Fallback to cache (if enabled)
- Fallback to secondary storage (if configured)
- Optimistic allowance (if enabled)

### Network Partition
**Test**: Simulate network issues between services
```bash
# Use network manipulation tools to simulate partition
# Test behavior during partition
```

**Expected behavior**:
- Operations continue with available store
- Async sync resumes when partition heals
- No data loss

## Troubleshooting

### Tests Failing Due to Quota Exhaustion

If tests are failing because users have exhausted their quota:

1. **Reset Quota** (if admin API available):
   ```bash
   # Reset user quota via admin endpoint
   ```

2. **Use Different Users**: The tests use `user1_free` (1,000 quota) and `user2_pro` (10,000 quota). Use `user2_pro` for more extensive testing.

3. **Wait for Reset**: Monthly quotas reset at subscription anniversary. Daily quotas reset daily.

### Tests Not Tracking Metrics

If metrics show 0 tests:

1. Check that users exist: `user1_free` and `user2_pro` should be set up by the comprehensive example
2. Check API responses: Verify `/api/quota` endpoint returns 200
3. Check quota limits: Ensure users have sufficient quota for testing

### Async Sync Not Completing

If async sync seems slow:

1. Check async sync queue size in logs
2. Monitor PostgreSQL connection pool
3. Check for errors in comprehensive-example logs
4. Verify async sync worker is running

## Best Practices

1. **Run tests with fresh quota**: Reset user quotas before running edge case tests
2. **Monitor logs**: Watch comprehensive-example logs for async sync errors
3. **Check metrics**: Monitor Prometheus metrics for tiered storage operations
4. **Test incrementally**: Start with shorter durations and fewer VUs
5. **Verify consistency**: Manually verify Hot and Cold store consistency after tests

