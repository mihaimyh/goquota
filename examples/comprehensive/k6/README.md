# k6 Load Testing for goquota Comprehensive Example

This directory contains k6 load testing scripts to validate all goquota features including rate limiting, quota management, tier differences, and concurrent user scenarios.

## Prerequisites

- Docker and Docker Compose installed
- The comprehensive example service running (via docker-compose)

**Note**: By default, `main.go` sets up `user1_free` (free tier) and `user2_pro` (pro tier). For full enterprise tier testing, you may need to add an enterprise user to `main.go` or the tests will handle missing users gracefully.

## Test Scripts

### 1. `load-test.js` - Comprehensive Load Test

**Purpose**: Full integration test with mixed workloads across all tiers and endpoints.

**Features**:
- Multiple user tiers (free, pro, enterprise) with weighted distribution
- All endpoints (`/api/data`, `/api/write`, `/api/expensive`) with different costs
- Ramp-up and ramp-down phases
- Quota status monitoring
- Rate limit header validation

**Usage**:
```bash
docker-compose run --rm k6-load-test run /scripts/load-test.js
```

**Custom Configuration**:
```bash
docker-compose run --rm -e K6_VUS=100 -e K6_DURATION=120s k6-load-test run /scripts/load-test.js
```

### 2. `rate-limit-test.js` - Rate Limiting Tests

**Purpose**: Validate rate limiting algorithms (token bucket vs sliding window).

**Features**:
- Free tier: Token bucket (10 req/sec, burst 20)
- Pro tier: Sliding window (100 req/sec)
- Enterprise tier: Sliding window (500 req/sec)
- Validates `X-RateLimit-*` headers and `Retry-After`

**Usage**:
```bash
docker-compose run --rm k6-load-test run /scripts/rate-limit-test.js
```

**Expected Results**:
- Free tier: ~20 requests allowed in burst, then rate limited
- Pro tier: ~100 requests allowed per second
- Enterprise tier: ~500 requests allowed per second

### 3. `quota-exhaustion-test.js` - Quota Exhaustion Scenarios

**Purpose**: Test quota limits and exhaustion behavior.

**Features**:
- Daily quota exhaustion (free: 50, pro: 500)
- Monthly quota exhaustion (free: 1000, pro: 10000)
- Validates 402 Payment Required responses
- Quota status endpoint monitoring

**Usage**:
```bash
docker-compose run --rm k6-load-test run /scripts/quota-exhaustion-test.js
```

**Expected Results**:
- Quota status shows increasing usage
- 402 responses when quota exceeded
- Error messages include quota details

### 4. `tier-comparison-test.js` - Tier Comparison

**Purpose**: Compare performance and success rates across different tiers.

**Features**:
- 10 concurrent users per tier
- All endpoints tested
- Success rate comparison
- Response time comparison

**Usage**:
```bash
docker-compose run --rm k6-load-test run /scripts/tier-comparison-test.js
```

**Expected Results**:
- Free tier: Lower success rate due to stricter limits
- Pro tier: Higher success rate
- Enterprise tier: Highest success rate

## Running Tests

### Start All Services

```bash
docker-compose up -d
```

Wait for services to be healthy (check with `docker-compose ps`).

### Run Individual Tests

```bash
# Comprehensive load test
docker-compose run --rm k6-load-test run /scripts/load-test.js

# Rate limiting test
docker-compose run --rm k6-load-test run /scripts/rate-limit-test.js

# Quota exhaustion test
docker-compose run --rm k6-load-test run /scripts/quota-exhaustion-test.js

# Tier comparison test
docker-compose run --rm k6-load-test run /scripts/tier-comparison-test.js
```

### Run with Custom Parameters

```bash
# Custom virtual users and duration
docker-compose run --rm \
  -e K6_VUS=100 \
  -e K6_DURATION=120s \
  k6-load-test run /scripts/load-test.js

# Custom base URL (for testing external deployments)
docker-compose run --rm \
  -e BASE_URL=http://localhost:8080 \
  k6-load-test run /scripts/load-test.js
```

### Run All Tests Sequentially

```bash
docker-compose run --rm k6-load-test run /scripts/rate-limit-test.js
docker-compose run --rm k6-load-test run /scripts/quota-exhaustion-test.js
docker-compose run --rm k6-load-test run /scripts/tier-comparison-test.js
docker-compose run --rm k6-load-test run /scripts/load-test.js
```

## Interpreting Results

### Key Metrics

1. **Success Rate**: Percentage of requests that returned 200 OK
   - Should be >85% for comprehensive tests
   - Lower for free tier due to stricter limits

2. **Rate Limit Hits**: Count of 429 Too Many Requests responses
   - Expected for free tier burst tests
   - Should match configured rate limits

3. **Quota Exceeded**: Count of 402 Payment Required responses
   - Expected when quota is exhausted
   - Validates quota enforcement

4. **Response Times**:
   - P95 < 1000ms is good
   - P99 < 2000ms is acceptable
   - Higher percentiles may spike during rate limiting

5. **Rate Limit Headers**:
   - `X-RateLimit-Limit`: Maximum requests per window
   - `X-RateLimit-Remaining`: Remaining requests in window
   - `X-RateLimit-Reset`: Unix timestamp when limit resets
   - `Retry-After`: Seconds to wait (on 429 responses)

### Expected Behavior

**Free Tier (Token Bucket)**:
- Burst: ~20 requests allowed immediately
- Sustained: ~10 requests per second
- Headers should reflect token bucket behavior

**Pro Tier (Sliding Window)**:
- Sustained: ~100 requests per second
- More consistent than token bucket
- Headers should reflect sliding window behavior

**Enterprise Tier (Sliding Window)**:
- Sustained: ~500 requests per second
- Highest throughput
- Minimal rate limiting

**Quota Exhaustion**:
- Free tier: 50 daily, 1000 monthly
- Pro tier: 500 daily, 10000 monthly
- 402 responses when quota exceeded
- Error messages include usage details

## Troubleshooting

### Service Not Ready

If k6 starts before the comprehensive-example is ready:

```bash
# Check service health
docker-compose ps

# Wait for comprehensive-example to be healthy
docker-compose up -d
# Wait a few seconds, then check logs
docker-compose logs comprehensive-example
```

### Connection Refused

If you see connection errors:

1. Verify services are running: `docker-compose ps`
2. Check network connectivity: `docker-compose exec k6-load-test ping comprehensive-example`
3. Verify BASE_URL: Should be `http://comprehensive-example:8080` in Docker network

### High Failure Rates

If failure rates are unexpectedly high:

1. Check Redis connection: `docker-compose logs redis`
2. Check application logs: `docker-compose logs comprehensive-example`
3. Verify user entitlements are set (check main.go setup)
4. Reduce VUs or increase duration for gradual load

### Rate Limit Headers Missing

If rate limit headers are not present:

1. Verify middleware is configured correctly
2. Check that requests are hitting protected endpoints
3. Verify user IDs match those in main.go (`user1_free`, `user2_pro`, `user3_enterprise`)

## Advanced Usage

### Export Results to InfluxDB

```bash
docker-compose run --rm \
  -e K6_INFLUXDB_URL=http://influxdb:8086/k6 \
  k6-load-test run /scripts/load-test.js
```

### Custom Test Scenarios

Create your own test script in this directory:

```javascript
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  vus: 10,
  duration: '30s',
};

export default function () {
  const res = http.get('http://comprehensive-example:8080/api/data', {
    headers: { 'X-User-ID': 'user1_free' },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}
```

Then run:
```bash
docker-compose run --rm k6-load-test run /scripts/your-test.js
```

## Integration with CI/CD

Example GitHub Actions workflow:

```yaml
name: Load Tests

on: [push, pull_request]

jobs:
  load-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Start services
        run: docker-compose up -d
      - name: Wait for services
        run: sleep 30
      - name: Run load tests
        run: |
          docker-compose run --rm k6-load-test run /scripts/load-test.js
          docker-compose run --rm k6-load-test run /scripts/rate-limit-test.js
```

## References

- [k6 Documentation](https://k6.io/docs/)
- [k6 JavaScript API](https://k6.io/docs/javascript-api/)
- [goquota Comprehensive Example](../README.md)

