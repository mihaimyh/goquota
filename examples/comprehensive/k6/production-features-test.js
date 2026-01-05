import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate, Gauge } from 'k6/metrics';

// Custom metrics for production features
const quotaPercentage = new Gauge('quota_percentage');
const quotaRemaining = new Gauge('quota_remaining');
const rateLimitHeadersValid = new Counter('rate_limit_headers_valid');
const responseTime = new Trend('response_time');
const successRate = new Rate('success_rate');
const quotaExceeded = new Counter('quota_exceeded');
const rateLimitExceeded = new Counter('rate_limit_exceeded');
const softLimitWarnings = new Counter('soft_limit_warnings'); // 80%+ usage

// Test configuration
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';
const VUS = parseInt(__ENV.K6_VUS || '10');
const DURATION = __ENV.K6_DURATION || '30s';

// User IDs from main.go
const FREE_USER = 'user1_free';      // Daily: 50, Monthly: 1000
const PRO_USER = 'user2_pro';        // Daily: 500, Monthly: 10000

export const options = {
  stages: [
    { duration: '5s', target: VUS * 0.2 },  // Ramp up to 20%
    { duration: '5s', target: VUS * 0.5 },  // Ramp up to 50%
    { duration: '5s', target: VUS },        // Ramp up to 100%
    { duration: DURATION, target: VUS },    // Sustained load
    { duration: '5s', target: 0 },         // Ramp down
  ],
  thresholds: {
    'http_req_duration': ['p(95)<1000', 'p(99)<2000'],
    'http_req_failed': ['rate<0.2'], // Less than 20% failures (allows quota/rate limit errors)
    'success_rate': ['rate>0.7'],    // At least 70% success
  },
};

function getRandomUser() {
  return Math.random() < 0.5 ? FREE_USER : PRO_USER;
}

function checkQuotaStatus(userID) {
  const headers = { 'X-User-ID': userID };
  const res = http.get(`${BASE_URL}/api/quota`, { headers });
  
  if (res.status === 200) {
    const quota = JSON.parse(res.body);
    
    // Track metrics for production monitoring
    quotaPercentage.add(quota.monthly.percentage);
    quotaRemaining.add(quota.monthly.remaining);
    
    // Track soft limit warnings (80%+ usage)
    if (quota.monthly.percentage >= 80) {
      softLimitWarnings.add(1);
    }
    
    return quota;
  }
  return null;
}

function validateRateLimitHeaders(res) {
  const headerKeys = Object.keys(res.headers);
  let hasLimit = false;
  let hasRemaining = false;
  let hasReset = false;
  
  for (const key of headerKeys) {
    const lowerKey = key.toLowerCase();
    if (lowerKey === 'x-ratelimit-limit') hasLimit = true;
    if (lowerKey === 'x-ratelimit-remaining') hasRemaining = true;
    if (lowerKey === 'x-ratelimit-reset') hasReset = true;
  }
  
  if (hasLimit && hasRemaining && hasReset) {
    rateLimitHeadersValid.add(1);
    return true;
  }
  return false;
}

export default function () {
  const userID = getRandomUser();
  
  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Check quota status every 5th request (simulating monitoring)
  let quota = null;
  if (__ITER % 5 === 0) {
    quota = checkQuotaStatus(userID);
    if (quota && quota.monthly.percentage >= 80) {
      console.log(`[${userID}] Soft limit warning: ${quota.monthly.percentage.toFixed(1)}% used (${quota.monthly.used}/${quota.monthly.limit})`);
    }
  }

  // Test different endpoints with weighted distribution
  const rand = Math.random();
  let endpoint, method, body;
  
  if (rand < 0.6) {
    // 60% - GET /api/data (cost: 1) - most common
    endpoint = '/api/data';
    method = 'GET';
    body = null;
  } else if (rand < 0.85) {
    // 25% - POST /api/write (cost: 5)
    endpoint = '/api/write';
    method = 'POST';
    body = JSON.stringify({ data: 'test' });
  } else {
    // 15% - POST /api/expensive (cost: 10) - expensive operations
    endpoint = '/api/expensive';
    method = 'POST';
    body = JSON.stringify({ data: 'expensive operation' });
  }

  // Make request
  let res;
  const startTime = Date.now();
  if (method === 'GET') {
    res = http.get(`${BASE_URL}${endpoint}`, { headers });
  } else {
    res = http.post(`${BASE_URL}${endpoint}`, body, { headers });
  }
  const duration = Date.now() - startTime;
  responseTime.add(duration);

  // Validate rate limit headers (if present)
  validateRateLimitHeaders(res);

  // Check response
  const isSuccess = res.status === 200;
  successRate.add(isSuccess);

  if (res.status === 200) {
    check(res, {
      'status is 200': (r) => r.status === 200,
      'response has body': (r) => r.body.length > 0,
      'response time acceptable': () => duration < 1000,
    });
  } else if (res.status === 402) {
    quotaExceeded.add(1);
    const body = JSON.parse(res.body);
    check(body, {
      'quota exceeded error code': (b) => b.error && b.error.code === 'QUOTA_EXCEEDED',
      'quota exceeded has details': (b) => b.error && b.error.details && b.error.details.used !== undefined,
    });
    console.log(`[${userID}] Quota exceeded: ${body.error.details.used}/${body.error.details.limit}`);
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
  const softLimitWarningsCount = data.metrics.soft_limit_warnings ? data.metrics.soft_limit_warnings.values.count : 0;
  
  const avgQuotaPercentage = data.metrics.quota_percentage && data.metrics.quota_percentage.values ? data.metrics.quota_percentage.values.avg : 0;
  const avgQuotaRemaining = data.metrics.quota_remaining && data.metrics.quota_remaining.values ? data.metrics.quota_remaining.values.avg : 0;

  return {
    'stdout': `
=== Production Features Load Test Results ===

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

Production Features:
- Rate Limit Headers Valid: ${headersValidCount} (${((headersValidCount / totalRequests) * 100).toFixed(1)}%)
- Rate Limit Exceeded: ${rateLimitExceededCount}
- Quota Exceeded: ${quotaExceededCount}
- Soft Limit Warnings (80%+): ${softLimitWarningsCount}
- Average Quota Usage: ${avgQuotaPercentage.toFixed(1)}%
- Average Quota Remaining: ${avgQuotaRemaining.toFixed(0)}

Throughput:
- Requests per second: ${(totalRequests / (parseInt(DURATION) || 30)).toFixed(2)}
    `,
  };
}

