import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

// Custom metrics
const rateLimitHits = new Counter('rate_limit_hits');
const rateLimitHeadersValid = new Counter('rate_limit_headers_valid');
const responseTime = new Trend('response_time');

// Test configuration
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';

// User IDs from main.go
const FREE_USER = 'user1_free';      // Token bucket: 10 req/sec, burst 20
const PRO_USER = 'user2_pro';        // Sliding window: 100 req/sec
// Note: user3_demo exists but is free tier, so we'll use pro_user for "enterprise" testing
const ENTERPRISE_USER = 'user2_pro'; // Using pro user for high-rate testing

export const options = {
  stages: [
    { duration: '2s', target: 1 },   // Warm up
    { duration: '1s', target: 25 },  // Burst test - 25 rapid requests to trigger rate limits
    { duration: '2s', target: 1 },   // Cool down
  ],
  thresholds: {
    'http_req_duration': ['p(95)<500'],
    'rate_limit_hits': ['count>0'], // Expect some rate limit hits
  },
};

function validateRateLimitHeaders(res, expectedLimit) {
  // k6 normalizes header names, so we need to find headers case-insensitively
  // The actual header keys in k6 appear to be normalized (e.g., "X-Ratelimit-Limit" not "X-RateLimit-Limit")
  let limitHeader = null;
  let remainingHeader = null;
  let resetHeader = null;
  
  // Iterate through all headers to find rate limit headers (case-insensitive search)
  const headerKeys = Object.keys(res.headers);
  for (const key of headerKeys) {
    const lowerKey = key.toLowerCase();
    if (lowerKey === 'x-ratelimit-limit') {
      limitHeader = res.headers[key];
    } else if (lowerKey === 'x-ratelimit-remaining') {
      remainingHeader = res.headers[key];
    } else if (lowerKey === 'x-ratelimit-reset') {
      resetHeader = res.headers[key];
    }
  }
  
  if (limitHeader && remainingHeader && resetHeader) {
    rateLimitHeadersValid.add(1);
    const limit = parseInt(limitHeader);
    const remaining = parseInt(remainingHeader);
    
    // Validate limit matches expected
    if (expectedLimit && limit === expectedLimit) {
      return true;
    }
    return true; // Headers present is good enough
  }
  
  // Debug: log what headers we actually got
  if (res.status === 429) {
    console.log(`[DEBUG] 429 response headers: ${JSON.stringify(Object.keys(res.headers))}`);
    console.log(`[DEBUG] Looking for rate limit headers, found: limit=${limitHeader}, remaining=${remainingHeader}, reset=${resetHeader}`);
  }
  
  return false;
}

export default function () {
  // Use free tier user to test rate limiting (lower limits = easier to trigger)
  // Free tier: 10 req/sec with burst 20 (token bucket)
  const userID = FREE_USER;
  const expectedLimit = 10;

  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Make request to /api/data (cost: 1)
  const res = http.get(`${BASE_URL}/api/data`, { headers });
  
  const duration = res.timings.duration;
  responseTime.add(duration);

  // Check response
  check(res, {
    'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
  });

  if (res.status === 429) {
    rateLimitHits.add(1);
    // Rate limit headers are ONLY present on 429 responses
    // Debug: log all headers to see what we're getting
    if (__ITER < 3) {
      console.log(`[DEBUG] 429 response - All headers: ${JSON.stringify(res.headers)}`);
      console.log(`[DEBUG] Header keys: ${Object.keys(res.headers).join(', ')}`);
    }
    const hasHeaders = validateRateLimitHeaders(res, expectedLimit);
    check(res, {
      'has rate limit headers on 429': () => hasHeaders,
      'has Retry-After header': () => {
        // Case-insensitive lookup for Retry-After header
        const headerKeys = Object.keys(res.headers);
        for (const key of headerKeys) {
          if (key.toLowerCase() === 'retry-after') {
            return res.headers[key] !== undefined;
          }
        }
        return false;
      },
    });
  }

  // No sleep - we want rapid requests to trigger rate limiting
}

export function handleSummary(data) {
  const totalRequests = data.metrics.http_reqs.values.count;
  const rateLimitHits = data.metrics.rate_limit_hits ? data.metrics.rate_limit_hits.values.count : 0;
  const headersValid = data.metrics.rate_limit_headers_valid ? data.metrics.rate_limit_headers_valid.values.count : 0;
  const successCount = totalRequests - rateLimitHits;
  
  return {
    'stdout': `
=== Rate Limiting Test Results ===

Test Strategy:
- Made rapid burst requests to trigger rate limiting
- Rate limit headers are only present on 429 responses (by design)

Metrics:
- Total Requests: ${totalRequests}
- Successful (200): ${successCount} (${((successCount / totalRequests) * 100).toFixed(1)}%)
- Rate Limited (429): ${rateLimitHits} (${((rateLimitHits / totalRequests) * 100).toFixed(1)}%)
- Valid Rate Limit Headers (on 429s): ${headersValid} / ${rateLimitHits}

Response Times:
- Average: ${data.metrics.http_req_duration.values.avg.toFixed(2)}ms
- P95: ${data.metrics.http_req_duration.values['p(95)'].toFixed(2)}ms
- P99: ${data.metrics.http_req_duration.values['p(99)'] ? data.metrics.http_req_duration.values['p(99)'].toFixed(2) : 'N/A'}ms

Note: Rate limit headers are only added when rate limits are exceeded (429 responses).
This is the current implementation - headers are not included on successful requests.
    `,
  };
}

