import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';

// Custom metrics per tier
const freeTierSuccess = new Rate('free_tier_success');
const proTierSuccess = new Rate('pro_tier_success');
const enterpriseTierSuccess = new Rate('enterprise_tier_success');

const freeTierResponseTime = new Trend('free_tier_response_time');
const proTierResponseTime = new Trend('pro_tier_response_time');
const enterpriseTierResponseTime = new Trend('enterprise_tier_response_time');

// Test configuration
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';

// User IDs from main.go
const FREE_USER = 'user1_free';      // Token bucket: 10 req/sec, burst 20
const PRO_USER = 'user2_pro';        // Sliding window: 100 req/sec
// Note: user3_demo exists but is free tier, so we'll use pro_user for "enterprise" testing
const ENTERPRISE_USER = 'user2_pro'; // Using pro user for high-rate testing

export const options = {
  stages: [
    { duration: '10s', target: 30 },  // Ramp up to 30 VUs (10 per tier)
    { duration: '60s', target: 30 }, // Sustained load
    { duration: '10s', target: 0 },  // Ramp down
  ],
  thresholds: {
    'free_tier_success': ['rate>0.8'],      // At least 80% success for free tier
    'pro_tier_success': ['rate>0.9'],       // At least 90% success for pro tier
    'enterprise_tier_success': ['rate>0.95'], // At least 95% success for enterprise
    'http_req_duration': ['p(95)<500'],
  },
};

export default function () {
  // Assign VU to tier (10 VUs per tier)
  const tier = __VU % 3;
  let userID, tierName, successRate, responseTime;
  
  if (tier === 0) {
    userID = FREE_USER;
    tierName = 'Free';
    successRate = freeTierSuccess;
    responseTime = freeTierResponseTime;
  } else if (tier === 1) {
    userID = PRO_USER;
    tierName = 'Pro';
    successRate = proTierSuccess;
    responseTime = proTierResponseTime;
  } else {
    userID = ENTERPRISE_USER;
    tierName = 'Enterprise';
    successRate = enterpriseTierSuccess;
    responseTime = enterpriseTierResponseTime;
  }

  const headers = {
    'X-User-ID': userID,
    'Content-Type': 'application/json',
  };

  // Test different endpoints
  const endpoints = [
    { path: '/api/data', method: 'GET', cost: 1 },
    { path: '/api/write', method: 'POST', cost: 5 },
    { path: '/api/expensive', method: 'POST', cost: 10 },
  ];
  
  const endpoint = endpoints[__ITER % endpoints.length];
  
  let res;
  if (endpoint.method === 'GET') {
    res = http.get(`${BASE_URL}${endpoint.path}`, { headers });
  } else {
    res = http.post(`${BASE_URL}${endpoint.path}`, JSON.stringify({}), { headers });
  }
  
  const duration = res.timings.duration;
  responseTime.add(duration);

  // Check response
  const isSuccess = res.status === 200;
  successRate.add(isSuccess);

  check(res, {
    [`[${tierName}] status is 200 or 429`]: (r) => r.status === 200 || r.status === 429,
    [`[${tierName}] has rate limit headers`]: (r) => {
      return r.headers['X-RateLimit-Limit'] !== undefined &&
             r.headers['X-RateLimit-Remaining'] !== undefined;
    },
  });

  // Small sleep
  sleep(0.1);
}

export function handleSummary(data) {
  const freeSuccess = (data.metrics.free_tier_success.values.rate * 100).toFixed(1);
  const proSuccess = (data.metrics.pro_tier_success.values.rate * 100).toFixed(1);
  const enterpriseSuccess = (data.metrics.enterprise_tier_success.values.rate * 100).toFixed(1);

  return {
    'stdout': `
=== Tier Comparison Test Results ===

Success Rates by Tier:
- Free Tier: ${freeSuccess}% (Expected: ~80-90% due to lower rate limits)
- Pro Tier: ${proSuccess}% (Expected: ~90-95%)
- Enterprise Tier: ${enterpriseSuccess}% (Expected: ~95-99%)

Response Times by Tier:
- Free Tier:
  * Average: ${data.metrics.free_tier_response_time.values.avg.toFixed(2)}ms
  * P95: ${data.metrics.free_tier_response_time.values['p(95)'].toFixed(2)}ms
- Pro Tier:
  * Average: ${data.metrics.pro_tier_response_time.values.avg.toFixed(2)}ms
  * P95: ${data.metrics.pro_tier_response_time.values['p(95)'].toFixed(2)}ms
- Enterprise Tier:
  * Average: ${data.metrics.enterprise_tier_response_time.values.avg.toFixed(2)}ms
  * P95: ${data.metrics.enterprise_tier_response_time.values['p(95)'].toFixed(2)}ms

Total Requests: ${data.metrics.http_reqs.values.count}
Failed Requests: ${(data.metrics.http_req_failed.values.rate * 100).toFixed(1)}%
    `,
  };
}

