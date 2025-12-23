import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';

// Custom metrics
const quotaExceeded = new Counter('quota_exceeded');
const rateLimitExceeded = new Counter('rate_limit_exceeded');
const rateLimitHeadersValid = new Counter('rate_limit_headers_valid');
const responseTime = new Trend('response_time');
const successRate = new Rate('success_rate');

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

  // Periodically check quota status (every 20th request)
  if (__ITER % 20 === 0) {
    const quotaRes = http.get(`${BASE_URL}/api/quota`, { headers });
    if (quotaRes.status === 200) {
      const quota = JSON.parse(quotaRes.body);
      // Log if quota is getting high
      if (quota.monthly.percentage > 80) {
        console.log(`[${userID}] Monthly quota at ${quota.monthly.percentage.toFixed(1)}%`);
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
- Average: ${data.metrics.http_req_duration.values.avg.toFixed(2)}ms
- Median: ${data.metrics.http_req_duration.values.med.toFixed(2)}ms
- P90: ${data.metrics.http_req_duration.values['p(90)'].toFixed(2)}ms
- P95: ${data.metrics.http_req_duration.values['p(95)'].toFixed(2)}ms
- P99: ${data.metrics.http_req_duration.values['p(99)'].toFixed(2)}ms
- Max: ${data.metrics.http_req_duration.values.max.toFixed(2)}ms

Rate Limiting:
- Rate Limit Headers Valid: ${headersValidCount} (${((headersValidCount / totalRequests) * 100).toFixed(1)}%)
- Rate Limit Exceeded: ${rateLimitExceededCount}

Quota Management:
- Quota Exceeded: ${quotaExceededCount}

Throughput:
- Requests per second: ${(totalRequests / (parseInt(DURATION) || 60)).toFixed(2)}
    `,
  };
}

