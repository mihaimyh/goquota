import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';

// Custom metrics
const quotaExceeded = new Counter('quota_exceeded');
const quotaStatusChecks = new Counter('quota_status_checks');
const responseTime = new Trend('response_time');

// Test configuration
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';

// User IDs from main.go
const FREE_USER = 'user1_free';      // Daily: 50, Monthly: 1000
const PRO_USER = 'user2_pro';        // Daily: 500, Monthly: 10000

export const options = {
  stages: [
    { duration: '10s', target: 1 },   // Warm up
    { duration: '60s', target: 2 },  // Consume quota gradually
    { duration: '10s', target: 0 },  // Cool down
  ],
  thresholds: {
    'http_req_duration': ['p(95)<1000'],
    'quota_exceeded': ['count>0'], // Expect quota to be exceeded
  },
};

function checkQuotaStatus(userID) {
  const headers = { 'X-User-ID': userID };
  const res = http.get(`${BASE_URL}/api/quota`, { headers });
  
  quotaStatusChecks.add(1);
  
  if (res.status === 200) {
    const body = JSON.parse(res.body);
    return {
      daily: body.daily,
      monthly: body.monthly,
    };
  }
  return null;
}

export default function () {
  const userType = __VU % 2;
  let userID, tierName, expectedDailyLimit, expectedMonthlyLimit;
  
  if (userType === 0) {
    userID = FREE_USER;
    tierName = 'Free';
    expectedDailyLimit = 50;
    expectedMonthlyLimit = 1000;
  } else {
    userID = PRO_USER;
    tierName = 'Pro';
    expectedDailyLimit = 500;
    expectedMonthlyLimit = 10000;
  }

  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Check quota status periodically
  if (__ITER % 10 === 0) {
    const quota = checkQuotaStatus(userID);
    if (quota) {
      console.log(`[${tierName}] Daily: ${quota.daily.used}/${quota.daily.limit} (${quota.daily.percentage.toFixed(1)}%), Monthly: ${quota.monthly.used}/${quota.monthly.limit} (${quota.monthly.percentage.toFixed(1)}%)`);
    }
  }

  // Make request to /api/data (cost: 1)
  const res = http.get(`${BASE_URL}/api/data`, { headers });
  
  const duration = res.timings.duration;
  responseTime.add(duration);

  // Check response
  const checks = check(res, {
    'status is valid': (r) => r.status === 200 || r.status === 402 || r.status === 429,
  });

  if (res.status === 402) {
    quotaExceeded.add(1);
    const body = JSON.parse(res.body);
    check(body, {
      'quota exceeded error code': (b) => b.error && b.error.code === 'QUOTA_EXCEEDED',
      'quota exceeded has details': (b) => b.error && b.error.details && b.error.details.used !== undefined,
    });
    console.log(`[${tierName}] Quota exceeded! Used: ${body.error.details.used}, Limit: ${body.error.details.limit}`);
  } else if (res.status === 429) {
    // Rate limit exceeded (different from quota)
    const retryAfter = res.headers['Retry-After'];
    check(res, {
      'has Retry-After header': () => retryAfter !== undefined,
    });
  }

  // Small sleep to avoid overwhelming
  sleep(0.5);
}

export function handleSummary(data) {
  return {
    'stdout': `
=== Quota Exhaustion Test Results ===

Metrics:
- Total Requests: ${data.metrics.http_reqs.values.count}
- Quota Exceeded (402): ${data.metrics.quota_exceeded.values.count}
- Quota Status Checks: ${data.metrics.quota_status_checks.values.count}

Response Times:
- Average: ${data.metrics.http_req_duration.values.avg.toFixed(2)}ms
- P95: ${data.metrics.http_req_duration.values['p(95)'].toFixed(2)}ms

Status Code Distribution:
- 200 OK: ${data.metrics.http_req_duration.values.count - data.metrics.quota_exceeded.values.count}
- 402 Payment Required: ${data.metrics.quota_exceeded.values.count}
- 429 Too Many Requests: ${(data.metrics.http_reqs.values.count - data.metrics.quota_exceeded.values.count) * 0.1} (estimated)
    `,
  };
}

