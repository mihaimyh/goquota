# Comprehensive Example

This example demonstrates all goquota features in a single application:

- **Redis Storage** - Production-ready storage backend
- **Multiple Tiers** - free, pro, enterprise with daily and monthly quotas
- **Rate Limiting** - Token bucket and sliding window algorithms
- **Caching** - Enabled with TTL configuration
- **Fallback Strategies** - Cache fallback, optimistic allowance, secondary storage
- **Observability** - Prometheus metrics and structured logging (zerolog)
- **Idempotency Keys** - Prevent double-charging
- **Refunds** - Handle failed operations
- **Tier Changes & Proration** - Mid-cycle upgrades/downgrades
- **Soft Limits & Warnings** - Threshold-based callbacks
- **HTTP Middleware** - Gin framework with dynamic cost calculation
 - **Billing Integration (optional)** - RevenueCat provider for automatic entitlement → tier sync

## Running Locally

### Prerequisites

1. **Start Redis**:
   ```bash
   docker-compose up -d redis
   ```

2. **Run the example**:
   ```bash
   go run examples/comprehensive/main.go
   ```

3. **(Optional) Enable RevenueCat billing integration**:
   ```bash
   export REVENUECAT_WEBHOOK_SECRET="yWt"
   export REVENUECAT_SECRET_API_KEY="sk_"
   # or on Windows PowerShell:
   # $env:REVENUECAT_WEBHOOK_SECRET="..."
   # $env:REVENUECAT_SECRET_API_KEY="..."
   ```

The example will:
- Run programmatic demonstrations of all features
- Start an HTTP server on `http://localhost:8080`
- Expose Prometheus metrics on `http://localhost:9090/metrics`

## Running with Docker Compose

The comprehensive example can be run entirely with Docker Compose:

```bash
# Start all services (Redis + Comprehensive Example)
docker-compose up -d comprehensive-example

# View logs
docker-compose logs -f comprehensive-example

# Stop services
docker-compose stop comprehensive-example
```

The example will automatically:
- Wait for Redis to be healthy before starting
- Connect to Redis using Docker networking
- Expose ports 8080 (HTTP) and 9090 (Prometheus metrics)

## Testing the API

Once running, test the endpoints:

```bash
# Health check
curl http://localhost:8080/health

# Check quota status
curl -H "X-User-ID: user1_free" http://localhost:8080/api/quota

# Consume quota (GET request, cost: 1)
curl -H "X-User-ID: user1_free" http://localhost:8080/api/data

# Consume quota (POST request, cost: 5)
curl -X POST -H "X-User-ID: user1_free" http://localhost:8080/api/write

# Expensive operation (cost: 10)
curl -X POST -H "X-User-ID: user1_free" http://localhost:8080/api/expensive

# View Prometheus metrics
curl http://localhost:9090/metrics

# (Optional) Restore purchases / sync from RevenueCat
curl -X POST "http://localhost:8080/api/restore-purchases?user_id=user1_free"

# (Optional) RevenueCat webhook (normally called by RevenueCat, not manually)
curl -X POST "http://localhost:8080/webhooks/revenuecat" \
  -H "Authorization: Bearer $REVENUECAT_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"event":{"id":"test","type":"TEST","app_user_id":"user1_free"}}'
```

> Note: Billing endpoints (`/webhooks/revenuecat`, `/api/restore-purchases`) are only
> registered when `REVENUECAT_SECRET` is set and the provider initializes successfully.

## What the Example Demonstrates

### Programmatic Demos

1. **Basic Quota Operations** - Daily and monthly quota consumption
2. **Rate Limiting** - Token bucket (free tier) and sliding window (pro tier)
3. **Idempotency Keys** - Prevent double-charging on retries
4. **Refunds** - Handle failed operations gracefully
5. **Tier Changes & Proration** - Mid-cycle upgrades preserve usage percentage
6. **Soft Limits & Warnings** - Callbacks triggered at thresholds (50%, 80%, 90%)
7. **Billing Cycles** - Anniversary-based billing cycle demonstration
8. **Fallback Strategies** - Configuration and behavior documentation

### HTTP Server Features

- Dynamic cost calculation (GET=1, POST=5, expensive endpoints=10)
- Rate limit headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After`)
- Custom error responses for quota exceeded and rate limited
- User ID extraction from `X-User-ID` header
- Prometheus metrics endpoint

## Environment Variables

- `REDIS_HOST` - Redis connection string (default: `localhost:6379`)
  - Use `redis:6379` when running in Docker Compose
 - `REVENUECAT_WEBHOOK_SECRET` - RevenueCat webhook secret used to verify `/webhooks/revenuecat` (optional)
 - `REVENUECAT_SECRET_API_KEY` - RevenueCat API key used by `SyncUser` for `/api/restore-purchases` (optional)
   - If either is unset, the example still runs but billing integration is disabled

## Files

- `main.go` - Main example application
- `Dockerfile` - Docker image definition
- `.dockerignore` - Docker build exclusions

