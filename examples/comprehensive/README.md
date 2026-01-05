# Comprehensive Example

This example demonstrates all goquota features in a single application:

- **Tiered Storage (Redis + PostgreSQL)** - Hot/Cold storage architecture for optimal performance and durability
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
 - **Billing Integration (optional)** - RevenueCat and Stripe providers for automatic entitlement → tier sync

## Running Locally

### Prerequisites

1. **Start Redis and PostgreSQL**:
   ```bash
   docker-compose up -d redis postgres
   ```

2. **Setup PostgreSQL database**:
   ```bash
   # Connect to PostgreSQL and create database
   psql -h localhost -U postgres -c "CREATE DATABASE goquota;"
   
   # Run migrations (from project root)
   psql -h localhost -U postgres -d goquota -f storage/postgres/migrations/001_initial_schema.sql
   psql -h localhost -U postgres -d goquota -f storage/postgres/migrations/002_forever_periods.sql
   ```

3. **Run the example**:
   ```bash
   go run examples/comprehensive/main.go
   ```

3. **(Optional) Enable billing integration**:
   
   **RevenueCat:**
   ```bash
   export REVENUECAT_WEBHOOK_SECRET="yWt"
   export REVENUECAT_SECRET_API_KEY="sk_"
   # or on Windows PowerShell:
   # $env:REVENUECAT_WEBHOOK_SECRET="..."
   # $env:REVENUECAT_SECRET_API_KEY="..."
   ```
   
   **Stripe (test mode):**
   ```bash
   export STRIPE_API_KEY="sk_test_..."
   export STRIPE_WEBHOOK_SECRET="whsec_test_..."
   # or on Windows PowerShell:
   # $env:STRIPE_API_KEY="sk_test_..."
   # $env:STRIPE_WEBHOOK_SECRET="whsec_test_..."
   ```
   
   Note: Both providers can be enabled simultaneously.

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
- Wait for Redis and PostgreSQL to be healthy before starting
- Connect to Redis and PostgreSQL using Docker networking
- Expose ports 8080 (HTTP) and 9090 (Prometheus metrics)

### Exposing Webhooks for RevenueCat (Local Development)

RevenueCat webhooks require a publicly accessible URL. For local development, use ngrok:

1. **Get a free ngrok account** and auth token from [ngrok.com](https://ngrok.com)

2. **Set the ngrok auth token**:
   ```bash
   # Linux/Mac
   export NGROK_AUTHTOKEN="your_ngrok_token"
   
   # Windows PowerShell
   $env:NGROK_AUTHTOKEN="your_ngrok_token"
   ```

3. **Start ngrok tunnel**:
   ```bash
   docker-compose --profile webhook-tunnel up -d ngrok
   ```

4. **Get the public URL**:
   ```bash
   # View ngrok web interface at http://localhost:4040
   # Or get the URL via API:
   curl http://localhost:4040/api/tunnels | jq '.tunnels[0].public_url'
   ```

5. **Configure RevenueCat webhook**:
   - Go to RevenueCat Dashboard → Project Settings → Webhooks
   - Add webhook URL: `https://your-ngrok-url.ngrok.io/webhooks/revenuecat`
   - Copy the webhook secret and set `REVENUECAT_WEBHOOK_SECRET`

6. **Set RevenueCat credentials** and restart:
   ```bash
   export REVENUECAT_WEBHOOK_SECRET="your_webhook_secret"
   export REVENUECAT_SECRET_API_KEY="your_api_key"
   docker-compose restart comprehensive-example
   ```

**Note**: The ngrok URL changes each time you restart ngrok (unless you have a paid plan with static domains). Update the webhook URL in RevenueCat if needed.

**Alternative**: You can also use [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation/) (free, no account required):
```bash
cloudflared tunnel --url http://localhost:8080
```

### Exposing Webhooks for Stripe (Local Development)

Stripe webhooks can be tested locally using the Stripe CLI (recommended for testing):

1. **Install Stripe CLI**: [stripe.com/docs/stripe-cli](https://stripe.com/docs/stripe-cli)

2. **Login to Stripe CLI**:
   ```bash
   stripe login
   ```

3. **Forward webhooks to local server**:
   ```bash
   stripe listen --forward-to localhost:8080/webhooks/stripe
   ```
   
   This will output a webhook signing secret (starts with `whsec_`). Copy this value.

4. **Set the webhook secret**:
   ```bash
   export STRIPE_WEBHOOK_SECRET="whsec_..."  # From step 3
   export STRIPE_API_KEY="sk_test_..."        # Your Stripe test API key
   ```

5. **Restart the comprehensive example** (if running):
   ```bash
   docker-compose restart comprehensive-example
   ```

6. **Trigger test events**:
   ```bash
   stripe trigger customer.subscription.created
   stripe trigger invoice.payment_succeeded
   stripe trigger checkout.session.completed
   ```

**For production webhooks**, use ngrok (same setup as RevenueCat above) and configure the webhook endpoint in Stripe Dashboard → Developers → Webhooks.

**Important**: 
- Stripe requires `metadata["user_id"]` on subscriptions to map Stripe customers to your application users
- Configure tier mappings in `main.go` to map your Stripe Price IDs to goquota tiers (free, pro, enterprise)
- See `pkg/billing/stripe/CONFIGURATION.md` for detailed Stripe integration requirements

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

# (Optional) Restore purchases / sync from Stripe
curl -X POST "http://localhost:8080/api/restore-purchases-stripe?user_id=user1_free"

# (Optional) RevenueCat webhook (normally called by RevenueCat, not manually)
curl -X POST "http://localhost:8080/webhooks/revenuecat" \
  -H "Authorization: Bearer $REVENUECAT_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"event":{"id":"test","type":"TEST","app_user_id":"user1_free"}}'

# (Optional) Stripe webhook (normally called by Stripe, use Stripe CLI for testing)
# See "Exposing Webhooks for Stripe" section above
```

> Note: Billing endpoints are only registered when the respective provider is configured:
> - RevenueCat: `/webhooks/revenuecat`, `/api/restore-purchases` (requires `REVENUECAT_WEBHOOK_SECRET`)
> - Stripe: `/webhooks/stripe`, `/api/restore-purchases-stripe` (requires `STRIPE_API_KEY`)

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
- `POSTGRES_DSN` - PostgreSQL connection string (default: `postgres://postgres:postgres@localhost:5432/goquota?sslmode=disable`)
  - Use `postgres://postgres:postgres@postgres:5432/goquota?sslmode=disable` when running in Docker Compose
- **RevenueCat (optional)**:
  - `REVENUECAT_WEBHOOK_SECRET` - RevenueCat webhook secret used to verify `/webhooks/revenuecat` (optional)
  - `REVENUECAT_SECRET_API_KEY` - RevenueCat API key used by `SyncUser` for `/api/restore-purchases` (optional)
- **Stripe (optional)**:
  - `STRIPE_API_KEY` - Stripe API key (required for Stripe provider, use `sk_test_...` for test mode)
  - `STRIPE_WEBHOOK_SECRET` - Stripe webhook secret used to verify `/webhooks/stripe` (optional, use `whsec_test_...` for test mode)
  - If unset, the example still runs but the respective billing integration is disabled
  - Both RevenueCat and Stripe can be enabled simultaneously

## Files

- `main.go` - Main example application
- `Dockerfile` - Docker image definition
- `.dockerignore` - Docker build exclusions

