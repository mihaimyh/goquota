import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';

// Edge case specific metrics
const edgeCasePassed = new Counter('edge_case_passed');
const edgeCaseFailed = new Counter('edge_case_failed');
const readAfterWriteConsistency = new Counter('read_after_write_consistency');
const concurrentOperationSuccess = new Counter('concurrent_operation_success');
const asyncSyncLagTest = new Counter('async_sync_lag_test');
const quotaBoundaryTest = new Counter('quota_boundary_test');
const rapidConsumptionTest = new Counter('rapid_consumption_test');

// Test configuration
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';
const VUS = parseInt(__ENV.K6_VUS || '10'); // Lower VUs for focused edge case testing
const DURATION = __ENV.K6_DURATION || '30s';

// Test users - using existing users from comprehensive example
const FREE_USER = 'user1_free';
const PRO_USER = 'user2_pro';

export const options = {
  stages: [
    { duration: '5s', target: VUS },   // Quick ramp up
    { duration: DURATION, target: VUS }, // Sustained testing
    { duration: '5s', target: 0 },     // Ramp down
  ],
  thresholds: {
    'edge_case_passed': ['count>50'], // At least 50 edge cases should pass
    'http_req_failed': ['rate<0.5'], // Allow up to 50% failures (edge cases may intentionally fail)
  },
};

// ============================================================
// Edge Case 1: Rapid Retries During Async Sync
// ============================================================
// Test: Rapid retries during async sync window
// Expected: All operations are tracked correctly, async sync handles load
function testRapidRetriesAsyncSync(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get initial quota
  const quotaBefore = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaBefore.status !== 200) {
    edgeCaseFailed.add(1);
    return false;
  }
  const quotaBeforeData = JSON.parse(quotaBefore.body);
  const initialUsed = quotaBeforeData.monthly.used;
  const remaining = quotaBeforeData.monthly.limit - initialUsed;

  if (remaining < 10) {
    // Not enough quota - skip this test (don't count as pass/fail)
    return true;
  }

  // Rapid retries (simulating network retries during async sync)
  const requests = [];
  for (let i = 0; i < 5; i++) {
    requests.push(http.get(`${BASE_URL}/api/data`, { headers })); // Each costs 1
    sleep(0.01); // 10ms delay - within async sync window
  }

  // Wait a bit for async sync
  sleep(0.2);

  // Check quota after all requests
  const quotaAfter = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaAfter.status !== 200) return false;
  const quotaAfterData = JSON.parse(quotaAfter.body);
  const finalUsed = quotaAfterData.monthly.used;

  const successCount = requests.filter(r => r.status === 200).length;
  const consumed = finalUsed - initialUsed;
  const passed = successCount === 5 && consumed === 5;
  
  if (passed) {
    asyncSyncLagTest.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ consumed, successCount, req1Status: requests[0]?.status }, {
    'rapid retries during async sync: all requests succeed': () => successCount === 5,
    'rapid retries during async sync: quota accurate': () => consumed === 5,
  });
}

// ============================================================
// Edge Case 2: Read Immediately After Write (Async Sync Window)
// ============================================================
// Test: Read quota immediately after consumption (during async sync)
// Expected: Read should reflect the write (from Hot), even if Cold sync pending
function testReadAfterWriteConsistency(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get initial quota
  const quotaBefore = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaBefore.status !== 200) return false;
  const quotaBeforeData = JSON.parse(quotaBefore.body);
  const initialUsed = quotaBeforeData.monthly.used;

  // Consume quota
  const consumeRes = http.get(`${BASE_URL}/api/data`, { headers }); // Cost: 1
  if (consumeRes.status !== 200) return false;

  // Read immediately (within async sync window)
  sleep(0.005); // 5ms delay
  const quotaAfter = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaAfter.status !== 200) return false;
  const quotaAfterData = JSON.parse(quotaAfter.body);
  const finalUsed = quotaAfterData.monthly.used;

  // Quota should reflect the consumption immediately (read from Hot)
  const consumed = finalUsed - initialUsed;
  const passed = consumed === 1;
  
  if (passed) {
    readAfterWriteConsistency.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ consumed, initialUsed, finalUsed }, {
    'read after write: quota updated immediately': () => consumed === 1,
    'read after write: quota is consistent': () => finalUsed === initialUsed + 1,
  });
}

// ============================================================
// Edge Case 3: Concurrent Operations on Same User
// ============================================================
// Test: Multiple VUs consuming quota for same user simultaneously
// Expected: All operations succeed, quota is accurate (atomic operations)
function testConcurrentOperations(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get initial quota
  const quotaBefore = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaBefore.status !== 200) return false;
  const quotaBeforeData = JSON.parse(quotaBefore.body);
  const initialUsed = quotaBeforeData.monthly.used;

  // Make 5 concurrent requests
  const requests = [];
  for (let i = 0; i < 5; i++) {
    requests.push(http.get(`${BASE_URL}/api/data`, { headers })); // Each costs 1
  }

  // Wait a bit for async sync
  sleep(0.1);

  // Check final quota
  const quotaAfter = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaAfter.status !== 200) return false;
  const quotaAfterData = JSON.parse(quotaAfter.body);
  const finalUsed = quotaAfterData.monthly.used;

  const successCount = requests.filter(r => r.status === 200).length;
  const consumed = finalUsed - initialUsed;
  const passed = successCount === 5 && consumed === 5;

  if (passed) {
    concurrentOperationSuccess.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ successCount, consumed, initialUsed, finalUsed }, {
    'concurrent operations: all succeed': () => successCount === 5,
    'concurrent operations: quota accurate': () => consumed === 5,
  });
}

// ============================================================
// Edge Case 4: Quota Boundary Conditions
// ============================================================
// Test: Consuming exactly at limit, just under, just over
// Expected: Proper enforcement at boundaries
function testQuotaBoundary(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get current quota
  const quotaRes = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaRes.status !== 200) return false;
  const quotaData = JSON.parse(quotaRes.body);
  const remaining = quotaData.monthly.limit - quotaData.monthly.used;

  if (remaining < 2) {
    // Not enough quota to test, skip
    return true;
  }

  // Test 1: Consume exactly remaining quota (should succeed)
  const consume1 = http.get(`${BASE_URL}/api/data`, { headers }); // Cost: 1
  const quotaAfter1 = http.get(`${BASE_URL}/api/quota`, { headers });
  const quotaData1 = JSON.parse(quotaAfter1.body);
  const remainingAfter1 = quotaData1.monthly.limit - quotaData1.monthly.used;

  // Test 2: Try to consume one more (should fail with 402)
  const consume2 = http.get(`${BASE_URL}/api/data`, { headers }); // Cost: 1

  const passed = consume1.status === 200 && 
                 consume2.status === 402 && 
                 remainingAfter1 === remaining - 1;

  if (passed) {
    quotaBoundaryTest.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ consume1Status: consume1.status, consume2Status: consume2.status, remainingAfter1 }, {
    'quota boundary: consume at limit succeeds': () => consume1.status === 200,
    'quota boundary: consume over limit fails': () => consume2.status === 402,
    'quota boundary: remaining quota accurate': () => remainingAfter1 === remaining - 1,
  });
}

// ============================================================
// Edge Case 5: Rapid Consumption (Async Sync Queue Stress)
// ============================================================
// Test: Rapid fire consumption to stress async sync queue
// Expected: All operations succeed, async sync handles load
function testRapidConsumption(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get initial quota
  const quotaBefore = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaBefore.status !== 200) return false;
  const quotaBeforeData = JSON.parse(quotaBefore.body);
  const initialUsed = quotaBeforeData.monthly.used;
  const remaining = quotaBeforeData.monthly.limit - initialUsed;

  if (remaining < 20) {
    // Not enough quota, skip
    return true;
  }

  // Rapid fire 20 requests
  const requests = [];
  for (let i = 0; i < 20; i++) {
    requests.push(http.get(`${BASE_URL}/api/data`, { headers })); // Each costs 1
    // No sleep - rapid fire
  }

  // Wait for async sync to complete
  sleep(0.5);

  // Check final quota
  const quotaAfter = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaAfter.status !== 200) return false;
  const quotaAfterData = JSON.parse(quotaAfter.body);
  const finalUsed = quotaAfterData.monthly.used;

  const successCount = requests.filter(r => r.status === 200).length;
  const consumed = finalUsed - initialUsed;
  const passed = successCount === 20 && consumed === 20;

  if (passed) {
    rapidConsumptionTest.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ successCount, consumed }, {
    'rapid consumption: all operations succeed': () => successCount === 20,
    'rapid consumption: quota accurate after async sync': () => consumed === 20,
  });
}

// ============================================================
// Edge Case 6: Mixed Read/Write Operations
// ============================================================
// Test: Interleaved reads and writes
// Expected: Reads always see latest writes (Hot consistency)
function testMixedOperations(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get initial quota
  const quotaBefore = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaBefore.status !== 200) return false;
  const quotaBeforeData = JSON.parse(quotaBefore.body);
  const initialUsed = quotaBeforeData.monthly.used;

  // Interleaved operations: write, read, write, read
  const write1 = http.post(`${BASE_URL}/api/write`, JSON.stringify({ data: 'test1' }), { headers }); // Cost: 5
  sleep(0.01);
  const read1 = http.get(`${BASE_URL}/api/quota`, { headers });
  const read1Data = JSON.parse(read1.body);
  
  const write2 = http.post(`${BASE_URL}/api/write`, JSON.stringify({ data: 'test2' }), { headers }); // Cost: 5
  sleep(0.01);
  const read2 = http.get(`${BASE_URL}/api/quota`, { headers });
  const read2Data = JSON.parse(read2.body);

  const consumed = read2Data.monthly.used - initialUsed;
  const passed = write1.status === 200 && 
                 write2.status === 200 &&
                 read1.status === 200 &&
                 read2.status === 200 &&
                 consumed === 10 && // 5 + 5
                 read1Data.monthly.used === initialUsed + 5 &&
                 read2Data.monthly.used === initialUsed + 10;

  if (passed) {
    readAfterWriteConsistency.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ consumed, read1Used: read1Data.monthly.used, read2Used: read2Data.monthly.used }, {
    'mixed operations: writes succeed': () => write1.status === 200 && write2.status === 200,
    'mixed operations: reads see latest writes': () => read1Data.monthly.used === initialUsed + 5 && read2Data.monthly.used === initialUsed + 10,
  });
}

// ============================================================
// Edge Case 7: Long-Running Consistency Check
// ============================================================
// Test: Read quota after many async operations have completed
// Expected: Quota is consistent (Cold sync completed)
function testLongRunningConsistency(userID) {
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Get initial quota
  const quotaBefore = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaBefore.status !== 200) {
    edgeCaseFailed.add(1);
    return false;
  }
  const quotaBeforeData = JSON.parse(quotaBefore.body);
  const initialUsed = quotaBeforeData.monthly.used;
  const remaining = quotaBeforeData.monthly.limit - initialUsed;

  if (remaining < 10) {
    // Not enough quota - skip this test (don't count as pass/fail)
    return true;
  }

  // Perform 10 operations
  let successCount = 0;
  for (let i = 0; i < 10; i++) {
    const res = http.get(`${BASE_URL}/api/data`, { headers });
    if (res.status === 200) successCount++;
    sleep(0.05); // Small delay between operations
  }

  // Wait longer for async sync to complete
  sleep(1.0);

  // Read quota (should reflect all operations)
  const quotaAfter = http.get(`${BASE_URL}/api/quota`, { headers });
  if (quotaAfter.status !== 200) return false;
  const quotaAfterData = JSON.parse(quotaAfter.body);
  const finalUsed = quotaAfterData.monthly.used;

  const consumed = finalUsed - initialUsed;
  const passed = consumed === successCount;

  if (passed) {
    asyncSyncLagTest.add(1);
    edgeCasePassed.add(1);
  } else {
    edgeCaseFailed.add(1);
  }

  return check({ consumed, successCount }, {
    'long-running consistency: quota accurate after async sync': () => consumed === successCount,
  });
}

// ============================================================
// Main Test Function
// ============================================================
export default function () {
  // Prefer user2_pro (10,000 quota) for better test coverage
  // Only use user1_free occasionally to test different tier behaviors
  const userID = (__ITER % 10 === 0) ? FREE_USER : PRO_USER;
  
  // Rotate through different edge case tests
  const testNumber = __ITER % 6;
  
  let result = false;
  
  switch (testNumber) {
    case 0:
      result = testRapidRetriesAsyncSync(userID);
      break;
    case 1:
      result = testReadAfterWriteConsistency(userID);
      break;
    case 2:
      result = testConcurrentOperations(userID);
      break;
    case 3:
      result = testQuotaBoundary(userID);
      break;
    case 4:
      result = testRapidConsumption(userID);
      break;
    case 5:
      result = testMixedOperations(userID);
      break;
    case 6:
      result = testLongRunningConsistency(userID);
      break;
  }

  // Small delay between tests
  sleep(0.1);
}

export function handleSummary(data) {
  const totalEdgeCases = (data.metrics.edge_case_passed ? data.metrics.edge_case_passed.values.count : 0) +
                         (data.metrics.edge_case_failed ? data.metrics.edge_case_failed.values.count : 0);
  const passedEdgeCases = data.metrics.edge_case_passed ? data.metrics.edge_case_passed.values.count : 0;
  const failedEdgeCases = data.metrics.edge_case_failed ? data.metrics.edge_case_failed.values.count : 0;
  const passRate = totalEdgeCases > 0 ? (passedEdgeCases / totalEdgeCases * 100).toFixed(1) : '0.0';

  return {
    'stdout': `
=== Tiered Storage Edge Case Test Results ===

Test Configuration:
- Virtual Users: ${VUS}
- Duration: ${DURATION}
- Base URL: ${BASE_URL}

Edge Case Results:
- Total Edge Cases Tested: ${totalEdgeCases}
- Passed: ${passedEdgeCases} (${passRate}%)
- Failed: ${failedEdgeCases} (${(100 - parseFloat(passRate)).toFixed(1)}%)

Edge Case Breakdown:
- Rapid Retries During Async Sync: ${data.metrics.async_sync_lag_test ? data.metrics.async_sync_lag_test.values.count : 0}
- Read After Write Consistency: ${data.metrics.read_after_write_consistency ? data.metrics.read_after_write_consistency.values.count : 0}
- Concurrent Operations: ${data.metrics.concurrent_operation_success ? data.metrics.concurrent_operation_success.values.count : 0}
- Quota Boundary Tests: ${data.metrics.quota_boundary_test ? data.metrics.quota_boundary_test.values.count : 0}
- Rapid Consumption Tests: ${data.metrics.rapid_consumption_test ? data.metrics.rapid_consumption_test.values.count : 0}
- Long-Running Consistency: ${data.metrics.async_sync_lag_test ? data.metrics.async_sync_lag_test.values.count : 0}

Test Scenarios:
1. Rapid Retries During Async Sync - Tests that rapid retries during async sync window are handled correctly
2. Read After Write Consistency - Tests that reads immediately after writes reflect the changes (Hot store consistency)
3. Concurrent Operations - Tests atomic operations when multiple VUs operate on same user
4. Quota Boundary Conditions - Tests enforcement at exact limit boundaries
5. Rapid Consumption - Tests async sync queue under high-frequency load
6. Mixed Read/Write Operations - Tests interleaved operations maintain consistency
7. Long-Running Consistency - Tests that quota is accurate after async sync completes

Overall: ${passedEdgeCases >= totalEdgeCases * 0.8 ? '✓ PASS' : '✗ FAIL'} (${passRate}% pass rate)
    `,
  };
}

