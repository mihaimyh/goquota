# Usage API Package

The `pkg/api` package provides a standardized HTTP API for exposing user quota state. This transforms goquota from a backend library into a full-stack SaaS kit by providing ready-to-use endpoints that frontend developers can consume without understanding the underlying billing complexity.

## Features

- **Unified Quota View**: Combines monthly limits and forever credits into a single, easy-to-consume JSON response
- **Orphaned Credits Detection**: Automatically discovers and displays purchased credits even when user downgrades to a tier without that resource
- **Unlimited Quota Handling**: Properly handles unlimited (-1) quotas
- **Resource Filtering**: Optional resource filtering for performance optimization
- **Comprehensive Error Handling**: Secure error responses with appropriate HTTP status codes

## Quick Start

```go
package main

import (
    "net/http"
    
    "github.com/mihaimyh/goquota/pkg/api"
    "github.com/mihaimyh/goquota/pkg/goquota"
    "github.com/mihaimyh/goquota/storage/memory"
)

func main() {
    // 1. Create your quota manager
    storage := memory.New()
    config := &goquota.Config{
        DefaultTier: "free",
        Tiers: map[string]goquota.TierConfig{
            "free": {
                Name: "free",
                MonthlyQuotas: map[string]int{
                    "api_calls": 100,
                },
            },
            "pro": {
                Name: "pro",
                MonthlyQuotas: map[string]int{
                    "api_calls": 1000,
                },
            },
        },
    }
    manager, _ := goquota.NewManager(storage, config)
    
    // 2. Create API handler
    usageHandler, _ := api.NewHandler(api.Config{
        Manager: manager,
        GetUserID: api.FromHeader("X-User-ID"), // Extract user ID from header
        KnownResources: []string{"api_calls"},   // List all known resources
    })
    
    // 3. Register route
    http.HandleFunc("/api/v1/me/usage", usageHandler.GetUsage)
    http.ListenAndServe(":8080", nil)
}
```

## Configuration

### Required Fields

- **Manager**: The goquota Manager instance
- **GetUserID**: Function to extract user ID from HTTP request

### Optional Fields

- **KnownResources**: List of all known resources across all tiers. Used to discover orphaned credits when users downgrade.
- **ResourceFilter**: Function to filter which resources to include in the response. Applied AFTER resource discovery.
- **OnError**: Custom error handler. If nil, uses default error handling.

## User ID Extraction

The package provides helper functions for common extraction patterns:

```go
// From header
handler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: api.FromHeader("X-User-ID"),
})

// From context (e.g., JWT middleware)
type contextKey string
key := contextKey("userID")
handler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: api.FromContext(key),
})

// Custom extractor
handler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: func(r *http.Request) string {
        // Custom logic (e.g., JWT parsing)
        return extractUserIDFromJWT(r)
    },
})
```

## Response Format

The API returns a JSON response with the following structure:

```json
{
  "user_id": "user_123",
  "tier": "pro",
  "status": "active",
  "resources": {
    "api_calls": {
      "limit": 1500,
      "used": 150,
      "remaining": 1350,
      "reset_at": "2025-02-01T00:00:00Z",
      "breakdown": [
        {
          "source": "monthly",
          "limit": 1000,
          "used": 150
        },
        {
          "source": "forever",
          "balance": 500,
          "limit": 500,
          "used": 0
        }
      ]
    }
  }
}
```

### Response Fields

- **user_id**: The user's identifier
- **tier**: Current subscription tier
- **status**: One of "active", "expired", or "default"
- **resources**: Map of resource names to quota information
  - **limit**: Combined limit (monthly + forever credits, or -1 for unlimited)
  - **used**: Combined used amount (from monthly quota)
  - **remaining**: Combined remaining quota (or -1 for unlimited)
  - **reset_at**: Reset time for monthly quota (ISO 8601 format)
  - **breakdown**: Array of quota sources
    - **source**: "monthly", "forever", or "daily"
    - **limit**: Limit for this source (-1 for unlimited)
    - **used**: Used amount for this source
    - **balance**: Balance for forever credits (limit - used)

## Resource Filtering

Use `ResourceFilter` to limit which resources are returned. This is useful for performance optimization when the frontend only needs specific resources:

```go
handler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: api.FromHeader("X-User-ID"),
    KnownResources: []string{"api_calls", "gpt4", "tts_chars"},
    ResourceFilter: func(resources []string) []string {
        // Only return api_calls (e.g., from query parameter)
        filtered := make([]string, 0)
        for _, r := range resources {
            if r == "api_calls" {
                filtered = append(filtered, r)
            }
        }
        return filtered
    },
})
```

**Note**: ResourceFilter is applied AFTER resource discovery, so it doesn't affect orphaned credits detection.

## Orphaned Credits

When a user purchases credits and then downgrades to a tier that doesn't include that resource, the credits become "orphaned". The API automatically discovers and displays these credits if:

1. The resource is listed in `KnownResources`
2. The resource has forever credits (limit > 0 or used > 0)

Example scenario:
1. User has "pro" tier with "gpt4" resource
2. User purchases 1000 "gpt4" credits
3. User downgrades to "free" tier (no "gpt4" in config)
4. API still shows the 1000 credits because it's in `KnownResources`

## Unlimited Quotas

The API properly handles unlimited quotas (represented as `-1` in goquota):

- If monthly quota is unlimited (-1), the combined limit and remaining are also unlimited (-1)
- Forever credits are still shown in the breakdown, but don't affect the combined unlimited status
- Example: Monthly unlimited + 500 forever credits → Combined: unlimited

## Error Handling

The API returns appropriate HTTP status codes:

- **200 OK**: Success
- **400 Bad Request**: Invalid user ID format
- **401 Unauthorized**: Missing user ID
- **500 Internal Server Error**: Storage or internal errors

Custom error handling can be provided via `OnError`:

```go
handler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: api.FromHeader("X-User-ID"),
    OnError: func(w http.ResponseWriter, r *http.Request, err error) {
        // Custom error handling
        log.Printf("Error: %v", err)
        http.Error(w, "Something went wrong", http.StatusInternalServerError)
    },
})
```

## Integration with Existing Middleware

The Usage API can be used alongside the quota enforcement middleware:

```go
// 1. Protect endpoints with quota middleware
api.Use(goquota.Middleware(&goquota.Config{
    Manager: manager,
    GetUserID: api.FromHeader("X-User-ID"),
    GetResource: api.FixedResource("api_calls"),
    GetAmount: api.FixedAmount(1),
}))

// 2. Expose usage endpoint
usageHandler, _ := api.NewHandler(api.Config{
    Manager: manager,
    GetUserID: api.FromHeader("X-User-ID"),
    KnownResources: []string{"api_calls"},
})
api.GET("/api/v1/me/usage", usageHandler.GetUsage)
```

## Performance Considerations

- **Sequential Fetching**: The API uses sequential quota queries (not concurrent) for simplicity and maintainability. For 1-3 resources, this takes <2ms total.
- **Caching**: The API leverages Manager's built-in caching - no additional caching needed.
- **Resource Filtering**: Use `ResourceFilter` to reduce the number of quota queries when only specific resources are needed.

## Testing

The package includes comprehensive tests with 86%+ code coverage, covering:

- Happy paths (monthly only, forever only, both)
- Edge cases (unlimited quotas, orphaned credits, zero limits, exceeded quota)
- Error scenarios (missing user ID, storage errors, invalid config)
- Resource filtering
- Entitlement states (active, expired, default)

Run tests:

```bash
go test ./pkg/api -v -cover
```

## Best Practices

1. **Always provide KnownResources**: This ensures orphaned credits are discovered
2. **Use ResourceFilter for performance**: When the frontend only needs specific resources
3. **Handle errors gracefully**: Provide custom `OnError` for better error messages
4. **Cache responses on frontend**: The API doesn't cache responses - implement client-side caching
5. **Monitor usage**: Use the breakdown to show users where their quota is being consumed

## Example Frontend Integration

```javascript
// React/Vue example
async function fetchUsage() {
  const response = await fetch('/api/v1/me/usage', {
    headers: {
      'X-User-ID': userId,
    },
  });
  
  const data = await response.json();
  
  // Display quota
  data.resources.api_calls && (
    <ProgressBar
      used={data.resources.api_calls.used}
      limit={data.resources.api_calls.limit}
      remaining={data.resources.api_calls.remaining}
      resetAt={data.resources.api_calls.reset_at}
    />
  );
  
  // Show breakdown
  data.resources.api_calls.breakdown.map(source => (
    <div key={source.source}>
      {source.source}: {source.balance || source.used}/{source.limit}
    </div>
  ));
}
```

