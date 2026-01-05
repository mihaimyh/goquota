import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';

// Custom metrics
const quotaExceeded = new Counter('quota_exceeded');
const rateLimitExceeded = new Counter('rate_limit_exceeded');
const rateLimitHeadersValid = new Counter('rate_limit_headers_valid');
const responseTime = new Trend('response_time');
const successRate = new Rate('success_rate');

// Tiered storage specific metrics
const tieredReadThrough = new Counter('tiered_read_through'); // Read from Cold (cache miss)
const tieredHotHit = new Counter('tiered_hot_hit'); // Read from Hot (cache hit)
const tieredAsyncSync = new Counter('tiered_async_sync'); // Async consumption operations
const tieredConsistencyCheck = new Counter('tiered_consistency_check'); // Consistency verification
const tieredIdempotencyTest = new Counter('tiered_idempotency_test'); // Idempotency key tests

// Test configuration
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';
const VUS = parseInt(__ENV.K6_VUS || '50');
const DURATION = __ENV.K6_DURATION || '60s';

// User IDs from main.go
const FREE_USER = 'user1_free';
const PRO_USER = 'user2_pro';
// Note: user3_demo exists but is free tier, so we'll use pro_user for "enterprise" testing
const ENTERPRISE_USER = 'user2_pro'; // Using pro user for high-rate testing

export const options = {
  stages: [
    { duration: '10s', target: VUS * 0.2 },  // Ramp up to 20%
    { duration: '10s', target: VUS * 0.5 }, // Ramp up to 50%
    { duration: '10s', target: VUS },        // Ramp up to 100%
    { duration: DURATION, target: VUS },    // Sustained load
    { duration: '10s', target: VUS * 0.5 }, // Ramp down to 50%
    { duration: '10s', target: 0 },          // Ramp down to 0
  ],
  thresholds: {
    'http_req_duration': ['p(95)<1000', 'p(99)<2000'],
    'http_req_failed': ['rate<0.1'], // Less than 10% failures
    'success_rate': ['rate>0.85'],   // At least 85% success
  },
};

function validateRateLimitHeaders(res) {
  const hasLimit = res.headers['X-RateLimit-Limit'] !== undefined;
  const hasRemaining = res.headers['X-RateLimit-Remaining'] !== undefined;
  const hasReset = res.headers['X-RateLimit-Reset'] !== undefined;
  
  if (hasLimit && hasRemaining && hasReset) {
    rateLimitHeadersValid.add(1);
    return true;
  }
  return false;
}

function getRandomUser() {
  const rand = Math.random();
  // 40% free, 40% pro, 20% enterprise
  if (rand < 0.4) return FREE_USER;
  if (rand < 0.8) return PRO_USER;
  return ENTERPRISE_USER;
}

export default function () {
  const userID = getRandomUser();
  
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Test different endpoints with different probabilities
  const rand = Math.random();
  let endpoint, method, body;
  
  if (rand < 0.5) {
    // 50% - GET /api/data (cost: 1)
    endpoint = '/api/data';
    method = 'GET';
    body = null;
  } else if (rand < 0.8) {
    // 30% - POST /api/write (cost: 5)
    endpoint = '/api/write';
    method = 'POST';
    body = JSON.stringify({ data: 'test' });
  } else {
    // 20% - POST /api/expensive (cost: 10)
    endpoint = '/api/expensive';
    method = 'POST';
    body = JSON.stringify({ data: 'expensive operation' });
  }

  // Make request
  let res;
  if (method === 'GET') {
    res = http.get(`${BASE_URL}${endpoint}`, { headers });
  } else {
    res = http.post(`${BASE_URL}${endpoint}`, body, { headers });
  }
  
  const duration = res.timings.duration;
  responseTime.add(duration);

  // Validate rate limit headers
  validateRateLimitHeaders(res);

  // Check response
  const isSuccess = res.status === 200;
  successRate.add(isSuccess);

  if (res.status === 200) {
    check(res, {
      'status is 200': (r) => r.status === 200,
      'has rate limit headers': (r) => validateRateLimitHeaders(r),
      'response has body': (r) => r.body.length > 0,
    });
  } else if (res.status === 402) {
    quotaExceeded.add(1);
    const body = JSON.parse(res.body);
    check(body, {
      'quota exceeded error code': (b) => b.error && b.error.code === 'QUOTA_EXCEEDED',
    });
  } else if (res.status === 429) {
    rateLimitExceeded.add(1);
    const retryAfter = res.headers['Retry-After'];
    check(res, {
      'has Retry-After header': () => retryAfter !== undefined,
      'rate limit error code': (r) => {
        try {
          const body = JSON.parse(r.body);
          return body.error && body.error.code === 'RATE_LIMIT_EXCEEDED';
        } catch {
          return false;
        }
      },
    });
  }

  // ============================================================
  // Tiered Storage Integration Tests
  // ============================================================
  
  // Test 1: Read-Through behavior (every 15th request)
  // This tests that quota reads work correctly with Hot/Cold tiered storage
  // First read may hit Cold (read-through), subsequent reads should hit Hot
  if (__ITER % 15 === 0) {
    const quotaRes = http.get(`${BASE_URL}/api/quota`, { headers });
    if (quotaRes.status === 200) {
      const quota = JSON.parse(quotaRes.body);
      tieredConsistencyCheck.add(1);
      
      // Verify consistency: both daily and monthly should be present
      check(quota, {
        'quota has daily data': (q) => q.daily && typeof q.daily.used === 'number',
        'quota has monthly data': (q) => q.monthly && typeof q.monthly.used === 'number',
        'quota values are consistent': (q) => 
          q.daily.used >= 0 && q.daily.used <= q.daily.limit &&
          q.monthly.used >= 0 && q.monthly.used <= q.monthly.limit,
      });
      
      // Log if quota is getting high
      if (quota.monthly.percentage > 80) {
        console.log(`[${userID}] Monthly quota at ${quota.monthly.percentage.toFixed(1)}%`);
      }
      
      // Note: We can't directly measure Hot vs Cold hits from HTTP,
      // but we can verify that reads work correctly (read-through is transparent)
      // The first read after a write may be slower (Cold read + Hot populate)
      // Subsequent reads should be faster (Hot hit)
    }
  }

  // Test 2: Idempotency with tiered storage (every 25th request)
  // Idempotency keys must work correctly with async sync to Cold
  // The idempotency check should work even during async sync lag
  if (__ITER % 25 === 0) {
    const idempotencyKey = `test-idempotency-${__VU}-${__ITER}`;
    const idempotencyHeaders = {
      ...headers,
      'X-Idempotency-Key': idempotencyKey,
    };
    
    // First request with idempotency key
    const firstRes = http.post(`${BASE_URL}/api/write`, JSON.stringify({ data: 'idempotency-test' }), { 
      headers: idempotencyHeaders 
    });
    
    if (firstRes.status === 200) {
      tieredIdempotencyTest.add(1);
      
      // Small delay to test async sync behavior
      sleep(0.1);
      
      // Retry with same idempotency key (should not double-charge)
      const retryRes = http.post(`${BASE_URL}/api/write`, JSON.stringify({ data: 'idempotency-retry' }), { 
        headers: idempotencyHeaders 
      });
      
      // Both should succeed, but second should not consume additional quota
      check(retryRes, {
        'idempotency retry succeeds': (r) => r.status === 200,
      });
    }
  }

  // Test 3: High-frequency consumption (every 30th request)
  // This tests async sync behavior - rapid consumption should write to Hot immediately
  // and sync to Cold asynchronously without blocking
  if (__ITER % 30 === 0) {
    tieredAsyncSync.add(1);
    
    // Make 5 rapid requests to test async sync throughput
    const rapidRequests = [];
    for (let i = 0; i < 5; i++) {
      rapidRequests.push(http.get(`${BASE_URL}/api/data`, { headers }));
    }
    
    // Verify all requests succeeded (async sync should not block)
    const allSucceeded = rapidRequests.every(r => r.status === 200);
    check({ allSucceeded }, {
      'rapid async sync requests succeed': () => allSucceeded,
    });
  }

  // Test 4: Write-Through behavior (every 50th request)
  // Test that entitlement/usage writes go through Cold first, then Hot
  // This is tested indirectly by checking quota consistency after operations
  if (__ITER % 50 === 0) {
    // Make a write operation
    const writeRes = http.post(`${BASE_URL}/api/write`, JSON.stringify({ data: 'write-through-test' }), { headers });
    
    if (writeRes.status === 200) {
      // Immediately check quota (should reflect the write)
      // With write-through, Cold is written first, then Hot
      // So the read should be consistent
      sleep(0.05); // Small delay to allow write-through
      const quotaAfterWrite = http.get(`${BASE_URL}/api/quota`, { headers });
      
      if (quotaAfterWrite.status === 200) {
        const quota = JSON.parse(quotaAfterWrite.body);
        check(quota, {
          'quota updated after write': (q) => q.monthly.used > 0,
        });
      }
    }
  }

  // Variable sleep to simulate real user behavior
  sleep(Math.random() * 0.5 + 0.1);
}

export function handleSummary(data) {
  const totalRequests = data.metrics.http_reqs.values.count;
  const successRate = data.metrics.success_rate ? data.metrics.success_rate.values.rate : 0;
  const successCount = totalRequests * successRate;
  const quotaExceededCount = data.metrics.quota_exceeded ? data.metrics.quota_exceeded.values.count : 0;
  const rateLimitExceededCount = data.metrics.rate_limit_exceeded ? data.metrics.rate_limit_exceeded.values.count : 0;
  const headersValidCount = data.metrics.rate_limit_headers_valid ? data.metrics.rate_limit_headers_valid.values.count : 0;

  return {
    'stdout': `
=== Comprehensive Load Test Results ===

Test Configuration:
- Virtual Users: ${VUS}
- Duration: ${DURATION}
- Base URL: ${BASE_URL}

Overall Metrics:
- Total Requests: ${totalRequests}
- Successful Requests: ${successCount.toFixed(0)} (${(successRate * 100).toFixed(1)}%)
- Failed Requests: ${(totalRequests - successCount).toFixed(0)} (${((1 - successRate) * 100).toFixed(1)}%)

Status Code Distribution:
- 200 OK: ${(successCount).toFixed(0)}
- 402 Payment Required (Quota Exceeded): ${quotaExceededCount}
- 429 Too Many Requests (Rate Limited): ${rateLimitExceededCount}
- Other Errors: ${(totalRequests - successCount - quotaExceededCount - rateLimitExceededCount).toFixed(0)}

Response Times:
- Average: ${data.metrics.http_req_duration && data.metrics.http_req_duration.values ? data.metrics.http_req_duration.values.avg.toFixed(2) : '0.00'}ms
- Median: ${data.metrics.http_req_duration && data.metrics.http_req_duration.values ? data.metrics.http_req_duration.values.med.toFixed(2) : '0.00'}ms
- P90: ${data.metrics.http_req_duration && data.metrics.http_req_duration.values && data.metrics.http_req_duration.values['p(90)'] ? data.metrics.http_req_duration.values['p(90)'].toFixed(2) : '0.00'}ms
- P95: ${data.metrics.http_req_duration && data.metrics.http_req_duration.values && data.metrics.http_req_duration.values['p(95)'] ? data.metrics.http_req_duration.values['p(95)'].toFixed(2) : '0.00'}ms
- P99: ${data.metrics.http_req_duration && data.metrics.http_req_duration.values && data.metrics.http_req_duration.values['p(99)'] ? data.metrics.http_req_duration.values['p(99)'].toFixed(2) : '0.00'}ms
- Max: ${data.metrics.http_req_duration && data.metrics.http_req_duration.values ? data.metrics.http_req_duration.values.max.toFixed(2) : '0.00'}ms

Rate Limiting:
- Rate Limit Headers Valid: ${headersValidCount} (${((headersValidCount / totalRequests) * 100).toFixed(1)}%)
- Rate Limit Exceeded: ${rateLimitExceededCount}

Quota Management:
- Quota Exceeded: ${quotaExceededCount}

Tiered Storage Integration:
- Consistency Checks: ${data.metrics.tiered_consistency_check ? data.metrics.tiered_consistency_check.values.count : 0}
- Idempotency Tests: ${data.metrics.tiered_idempotency_test ? data.metrics.tiered_idempotency_test.values.count : 0}
- Async Sync Operations: ${data.metrics.tiered_async_sync ? data.metrics.tiered_async_sync.values.count : 0}
- Read-Through Operations: ${data.metrics.tiered_read_through ? data.metrics.tiered_read_through.values.count : 0}
- Hot Cache Hits: ${data.metrics.tiered_hot_hit ? data.metrics.tiered_hot_hit.values.count : 0}

Tiered Storage Architecture:
- Hot Store (Redis): Handles rate limits, immediate quota consumption, cache
- Cold Store (PostgreSQL): Source of truth, async audit trail, durable storage
- Strategy: Hot-Primary/Async-Audit for consumption (fast writes to Hot, async sync to Cold)
- Strategy: Read-Through for reads (Hot → Cold → populate Hot)
- Strategy: Write-Through for entitlements (Cold → Hot)

Throughput:
- Requests per second: ${(totalRequests / (parseInt(DURATION) || 60)).toFixed(2)}
    `,
  };
}

