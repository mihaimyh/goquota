# k6 Load Testing Guide

This directory contains k6 load test scripts for testing the comprehensive-example service.

## Prerequisites

1. Start the comprehensive-example service:
   ```bash
   docker-compose up -d comprehensive-example
   ```

2. Wait for the service to be healthy (check with `docker-compose ps`)

## Available Tests

### 1. Production Features Test (New!)
Tests all production readiness features including quota monitoring, soft limits, and rate limiting.

```bash
docker-compose run --rm k6-load-test run /scripts/production-features-test.js
```

**What it tests:**
- Quota status monitoring (every 5th request)
- Soft limit warnings (80%+ usage)
- Rate limiting behavior
- Quota exhaustion
- Response times
- Different endpoint costs (1, 5, 10 units)

**Customize:**
```bash
# Lower load for testing
docker-compose run --rm -e K6_VUS=5 -e K6_DURATION=10s k6-load-test run /scripts/production-features-test.js

# Higher load
docker-compose run --rm -e K6_VUS=100 -e K6_DURATION=2m k6-load-test run /scripts/production-features-test.js
```

### 2. General Load Test
Comprehensive load test with mixed endpoints and user tiers.

```bash
docker-compose run --rm k6-load-test run /scripts/load-test.js
```

**What it tests:**
- Mixed endpoint usage (GET /api/data, POST /api/write, POST /api/expensive)
- Multiple user tiers (free, pro, enterprise)
- Rate limit headers validation
- Quota status checks
- Response time metrics

### 3. Rate Limit Test
Specifically tests rate limiting behavior with rapid burst requests.

```bash
docker-compose run --rm k6-load-test run /scripts/rate-limit-test.js
```

**What it tests:**
- Token bucket algorithm (free tier: 10 req/sec, burst 20)
- Rate limit header presence on 429 responses
- Retry-After header
- Burst behavior

### 4. Quota Exhaustion Test
Gradually consumes quota to test exhaustion behavior.

```bash
docker-compose run --rm k6-load-test run /scripts/quota-exhaustion-test.js
```

**What it tests:**
- Gradual quota consumption
- Quota exceeded responses (402)
- Quota status monitoring
- Different tier limits

### 5. Tier Comparison Test
Compares behavior across different user tiers.

```bash
docker-compose run --rm k6-load-test run /scripts/tier-comparison-test.js
```

**What it tests:**
- Free tier limits (50 daily, 1000 monthly)
- Pro tier limits (500 daily, 10000 monthly)
- Tier-specific rate limits
- Quota consumption patterns

## Environment Variables

All tests support these environment variables:

- `BASE_URL`: Service URL (default: `http://comprehensive-example:8080`)
- `K6_VUS`: Number of virtual users (default: 50)
- `K6_DURATION`: Test duration (default: 60s)

Example:
```bash
docker-compose run --rm \
  -e K6_VUS=10 \
  -e K6_DURATION=30s \
  k6-load-test run /scripts/production-features-test.js
```

## Understanding Results

### Key Metrics

- **http_req_duration**: Response time (p95, p99)
- **http_req_failed**: Failure rate (quota/rate limit errors are expected)
- **quota_exceeded**: Number of 402 responses (quota exhausted)
- **rate_limit_exceeded**: Number of 429 responses (rate limited)
- **soft_limit_warnings**: Quota usage >= 80%
- **quota_percentage**: Average quota usage percentage
- **quota_remaining**: Average remaining quota

### Expected Behaviors

- **High failure rate is normal** when testing quota/rate limits - these are intentional 402/429 responses
- **Soft limit warnings** indicate users approaching quota limits (80%+)
- **Rate limit headers** should be present on 429 responses
- **Response times** should be < 10ms for successful requests (Redis storage)

## Tips

1. **Start with lower load** to verify everything works:
   ```bash
   docker-compose run --rm -e K6_VUS=5 -e K6_DURATION=10s k6-load-test run /scripts/production-features-test.js
   ```

2. **Monitor Prometheus metrics** while tests run:
   ```bash
   # In another terminal
   curl http://localhost:9090/metrics | grep goquota
   ```

3. **Check service logs** for detailed behavior:
   ```bash
   docker-compose logs -f comprehensive-example
   ```

4. **Reset quotas** between tests (if needed):
   ```bash
   # The service resets quotas on startup, or restart the service
   docker-compose restart comprehensive-example
   ```

## Troubleshooting

**Test fails to connect:**
- Ensure comprehensive-example is running: `docker-compose ps`
- Check service health: `curl http://localhost:8080/health`

**All requests fail:**
- Check Redis is running: `docker-compose ps redis`
- Check service logs: `docker-compose logs comprehensive-example`

**High response times:**
- Check Redis connection
- Monitor Prometheus metrics for storage latency
- Reduce K6_VUS if system is overloaded

