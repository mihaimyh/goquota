# goquota Feature Roadmap

This document outlines planned enhancements to make goquota a comprehensive, production-ready quota management library for Go.

## Current State

**Version**: 0.x (Pre-1.0)  
**Test Coverage**: 80.9%+  
**Status**: Production-ready core features with room for enhancement

### Existing Features ✅

- Anniversary-based billing cycles
- Prorated quota adjustments for tier changes
- Daily, monthly, and forever (pre-paid credit) quota periods
- Pluggable storage (Redis, PostgreSQL, Firestore, in-memory, tiered Hot/Cold)
- Transaction-safe quota consumption
- HTTP middleware integration
- Multi-framework middleware (standard `net/http`, Gin, Echo, Fiber)
- Tier promotions (time-boxed temporary tier grants with automatic expiry)

---

## Priority 1: Critical Production Features

These features are essential for production deployments at scale.

### 1.1 Caching Layer

**Status**: ✅ Implemented  
**Priority**: Critical  
**Effort**: Medium

Built-in caching to reduce storage backend load and improve performance.

**Features**:

- ✅ In-memory LRU cache for entitlements
- ✅ Configurable TTL per cache type
- ✅ Cache invalidation on updates
- ❌ Optional Redis-backed distributed cache
- ✅ Cache hit/miss metrics

**Implementation**:

- `pkg/goquota/cache.go` - LRU cache with TTL support
- `examples/caching/` - Working example

**Benefits**:

- 10-100x reduction in storage reads
- Sub-millisecond quota checks
- Reduced cloud costs

### 1.2 Metrics & Observability

**Status**: ✅ Implemented  
**Priority**: Critical  
**Effort**: Medium

Comprehensive instrumentation for monitoring and debugging.

**Features**:

- ✅ Prometheus metrics integration
- ❌ OpenTelemetry support
- ✅ Key metrics:
  - Quota consumption rate
  - Rejection rate by tier/resource
  - Storage operation latency
  - Cache hit/miss rates
- ✅ Structured logging (zerolog)
- ❌ Distributed tracing support

**Implementation**:

- `pkg/goquota/metrics.go` - Metrics interface
- `pkg/goquota/metrics/prometheus/` - Prometheus integration
- `pkg/goquota/logger.go` - Logger interface
- `pkg/goquota/logger/zerolog/` - Zerolog integration
- `examples/observability/` - Working example

**Benefits**:

- Production visibility
- Performance optimization insights
- Proactive issue detection

### 1.3 Soft Limits & Warnings

**Status**: ✅ Implemented  
**Priority**: High  
**Effort**: Low

Warning thresholds before hard quota limits.

**Features**:

- ✅ Configurable warning thresholds (e.g., 80%, 90%)
- ✅ Warning callbacks via WarningHandler interface
- ✅ Context-based warning handler override
- ❌ Webhooks (only in-process callbacks)
- ❌ Grace period configuration
- ✅ Per-tier threshold customization

**Implementation**:

- `pkg/goquota/types.go` - WarningHandler interface
- `pkg/goquota/warning_test.go` - Comprehensive tests
- `pkg/goquota/manager.go` - Warning handler integration

**Benefits**:

- Better user experience
- Proactive notifications
- Reduced support burden

### 1.4 Redis Storage Adapter

**Status**: ✅ Implemented  
**Priority**: High  
**Effort**: Medium

High-performance storage backend for latency-sensitive applications.

**Features**:

- Full Storage interface implementation
- Atomic operations using Lua scripts
- Cluster support
- TTL-based automatic cleanup
- Fallback to secondary storage

**Benefits**:

- <1ms quota operations
- Horizontal scalability
- Cost-effective for high-volume workloads

### 1.5 Quota Refunds

**Status**: ✅ Implemented  
**Priority**: High  
**Effort**: Low

Handle failed operations gracefully by returning consumed quota.

**Features**:

- `Refund()` API method
- Idempotency key support
- Partial refunds
- Audit trail for refunds
- Maximum refund limits

**Benefits**:

- Fair billing for failed operations
- Better error handling
- Improved user trust

---

## Priority 2: Advanced Quota Management

Features that enhance quota flexibility and control.

### 2.1 Rate Limiting

**Status**: ✅ Implemented  
**Priority**: Medium  
**Effort**: Medium

Time-based rate limiting in addition to quota limits.

**Features**:

- ✅ Requests per second/minute/hour limits
- ✅ Token bucket algorithm
- ✅ Sliding window rate limiting
- ✅ Per-resource rate limits
- ✅ Burst allowance
- ✅ Storage-backed distributed rate limiting (Redis, Firestore, Memory)
- ✅ In-memory fallback for single-instance deployments
- ✅ HTTP middleware integration with rate limit headers
- ✅ Metrics integration

**Implementation**:

- `pkg/goquota/rate_limiter.go` - Rate limiter interface and factory
- `pkg/goquota/rate_limiter_storage.go` - Storage-backed rate limiter
- `pkg/goquota/rate_limiter_memory.go` - In-memory rate limiter fallback
- `storage/redis/redis.go` - Redis rate limiting (Lua scripts)
- `storage/firestore/firestore.go` - Firestore rate limiting (transactions)
- `storage/memory/memory.go` - Memory storage rate limiting
- `pkg/goquota/manager.go` - Rate limiting integration in Manager
- `middleware/http/middleware.go` - HTTP middleware rate limit support
- `examples/rate-limiting/` - Working example

**Benefits**:

- Prevents API abuse and protects backend services
- Distributed rate limiting across multiple instances
- Configurable per-tier and per-resource
- Graceful degradation when storage unavailable

### 2.2 Quota Reservations

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Medium

Pre-reserve quota for long-running operations.

**Features**:

- Reserve/commit/rollback pattern
- Timeout-based auto-rollback
- Reservation tracking
- Concurrent reservation limits

### 2.3 Multi-Resource Bundling

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: High

Define composite quota rules.

**Features**:

- OR conditions (100 calls OR 1000 tokens)
- AND conditions (requires both)
- Weighted consumption
- Dynamic resource mapping

### 2.4 Quota Rollover

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Carry forward unused quota to next period.

**Features**:

- Configurable rollover percentage
- Maximum rollover caps
- Per-tier rollover rules
- Expiration tracking

### 2.5 Burst/Overflow Allowance

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Temporary quota bursting for spiky workloads.

**Features**:

- Configurable burst percentage
- Burst repayment tracking
- Time-limited bursts
- Per-tier burst policies

---

## Priority 3: Enterprise Features

Features for large-scale deployments and organizations.

### 3.1 Multi-Tenancy Support

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: High

Organization and team-level quota management.

**Features**:

- Organization/team entities
- Hierarchical quota inheritance
- Team member quota pooling
- Cross-tenant isolation

### 3.2 Hierarchical Quotas

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: High

Parent-child quota relationships.

**Features**:

- Organization → Team → User hierarchy
- Quota distribution strategies
- Overflow handling
- Rebalancing algorithms

### 3.3 Quota Pools

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Shared quota pools for teams.

**Features**:

- Pool creation and management
- Fair-share allocation
- Pool exhaustion policies
- Usage attribution

### 3.4 Audit Trail

**Status**: 🟡 Partially Implemented  
**Priority**: Medium  
**Effort**: Medium

Comprehensive audit logging for compliance.

**Features**:

- ✅ AuditLogEntry and AuditLogger interface defined
- ✅ Basic audit logging integration in admin methods
- ✅ GetAuditLogs query API
- ❌ Storage backend implementations (Redis, Firestore, Postgres)
- ❌ Immutable audit logs
- ❌ Retention policies
- ❌ Compliance reporting exports

### 3.5 RBAC for Quota Management

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Role-based access control for admin operations.

**Features**:

- Admin roles and permissions
- Quota adjustment authorization
- API key management
- Action approval workflows

---

## Priority 4: Storage & Performance

Additional storage backends and performance optimizations.

### 4.1 Additional Storage Adapters

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Medium per adapter

**Planned Adapters**:

- ✅ Firestore (implemented)
- ✅ In-Memory (implemented)
- ✅ Redis (implemented)
- 🔴 PostgreSQL
- 🔴 MySQL
- 🔴 DynamoDB
- 🔴 MongoDB
- 🔴 SQLite (for embedded use)

### 4.2 Batch Operations

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Bulk quota operations for efficiency.

**Features**:

- Batch consumption
- Batch quota checks
- Batch refunds
- Transaction support

### 4.3 Read Replicas

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: High

Support for read-heavy workloads.

**Features**:

- Read/write splitting
- Eventual consistency handling
- Replica lag monitoring
- Automatic failover

### 4.4 Schema Versioning & Migration

**Status**: 🟡 Design Documented  
**Priority**: Medium  
**Effort**: High

Migration strategy for quota period schema changes without data loss.

**Problem**: Changing a user's quota period from `MONTHLY` to `WEEKLY` while they have active usage causes the key structure to change, effectively "resetting" their quota instantly.

**Current State**:
- Usage keys are based on period type and date (see `Period.Key()` in `pkg/goquota/types.go`)
- Changing period type or tier config changes the key structure
- No migration path for existing data

**Design Options**:

1. **Versioned Keys**: Include schema version in key (e.g., `user:123:reqs:v2:2024-01`)
   - Pros: Clean separation, easy to query
   - Cons: Requires migration of all existing keys

2. **Migration Tooling**: CLI to migrate existing usage records
   - Pros: Flexible, can handle complex migrations
   - Cons: Requires manual execution, potential downtime

3. **Graceful Degradation**: Support both old and new key formats during transition
   - Pros: Zero-downtime migration
   - Cons: More complex code, temporary storage overhead

**Recommendation**: Implement graceful degradation with migration tooling for initial setup. Document migration procedures in a dedicated migration guide.

**Implementation**:
- Add version field to Period struct (optional, defaults to v1)
- Update Period.Key() to include version when > v1
- Add migration utilities package
- Document migration procedures

---

## Priority 5: Developer Experience

Tools and features to improve developer productivity.

### 5.1 Admin API

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Medium

RESTful API for quota management.

**Features**:

- User quota CRUD operations
- Usage analytics endpoints
- Tier management
- Bulk operations
- OpenAPI specification

### 5.2 CLI Tool

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Low

Command-line interface for quota management.

**Features**:

- User quota inspection
- Manual quota adjustments
- Usage reports
- Migration utilities
- Configuration validation

### 5.3 Admin Dashboard

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: High

Web-based admin interface.

**Features**:

- Real-time usage monitoring
- User search and management
- Quota adjustment UI
- Analytics and reporting
- Alert configuration

### 5.4 Testing Utilities

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Low

Tools for testing quota scenarios.

**Features**:

- Mock storage with scenarios
- Time manipulation helpers
- Quota simulation tools
- Test data generators
- Assertion helpers

### 5.5 Migration Tools

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Utilities for storage backend migrations.

**Features**:

- Storage-to-storage migration
- Data validation
- Incremental migration
- Rollback support
- Migration monitoring

---

## Priority 6: Time Period Enhancements

Flexible quota period management.

### 6.1 Custom Period Types

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Medium

Support for additional period types beyond daily/monthly.

**Features**:

- Weekly quotas
- Quarterly quotas
- Yearly quotas
- Custom duration periods
- Fiscal period support

### 6.2 Multiple Concurrent Periods

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: High

Track multiple period types simultaneously.

**Features**:

- Daily AND monthly limits
- Composite period rules
- Period priority handling
- Efficient storage schema

### 6.3 Sliding Window Quotas

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: High

Rolling time window quotas.

**Features**:

- Last N days/hours tracking
- Efficient sliding window algorithm
- Configurable window size
- Historical data cleanup

---

## Priority 7: User Experience Features

Features that improve end-user experience.

### 7.1 Usage Analytics

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Medium

Built-in usage reporting and analytics.

**Features**:

- Historical usage queries
- Trend analysis
- Peak usage detection
- Resource breakdown
- Export capabilities

### 7.2 Quota Forecasting

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: High

Predict when users will exhaust quota.

**Features**:

- Usage trend analysis
- Depletion time estimation
- Proactive notifications
- Recommendation engine

### 7.3 Webhooks & Events

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Medium

Event system for external integrations.

**Features**:

- Quota exceeded webhooks
- Warning threshold webhooks
- Tier change events
- Reset notifications
- Custom event handlers

### 7.4 Quota Gifting/Sharing

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Medium

Transfer quota between users.

**Features**:

- User-to-user transfers
- Gift quota allocation
- Transfer limits
- Audit trail
- Expiration handling

---

## Priority 8: Reliability & Resilience

Features for production reliability.

### 8.1 Circuit Breaker

**Status**: ✅ Implemented  
**Priority**: High  
**Effort**: Low

Protection against storage failures.

**Features**:

- Automatic failure detection
- Configurable thresholds
- Half-open state testing
- Metrics integration

### 8.2 Fallback Strategies

**Status**: ✅ Implemented  
**Priority**: High  
**Effort**: Medium

Degraded mode operation.

**Features**:

- ✅ Fallback to cached data
- ✅ Optimistic quota allowance
- ✅ Secondary storage fallback
- ❌ Manual override mode (reserved for future use)
- ✅ Configurable staleness validation
- ✅ Composite fallback strategy (combines multiple strategies)
- ✅ Comprehensive error classification
- ✅ Panic recovery in composite strategy

**Implementation**:

- `pkg/goquota/types.go` - FallbackConfig struct and FallbackStrategy interface
- `pkg/goquota/fallback.go` - All fallback strategy implementations
- `pkg/goquota/errors.go` - Fallback error types
- `pkg/goquota/metrics.go` - Fallback metrics interface
- `pkg/goquota/metrics/prometheus/prometheus.go` - Prometheus fallback metrics
- `pkg/goquota/manager.go` - Fallback integration in Manager
- `pkg/goquota/fallback_test.go` - Comprehensive test coverage (30+ test cases)
- `examples/fallback/` - Working example

**Benefits**:

- Graceful degradation when storage is unavailable
- Reduced service disruption during outages
- Configurable fallback strategies per deployment needs
- Works with any Storage implementation for secondary storage

### 8.3 Retry Logic

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Low

Automatic retry for transient failures.

**Features**:

- Exponential backoff
- Configurable retry policies
- Idempotent operation detection
- Retry metrics

### 8.4 Idempotency Keys

**Status**: ✅ Implemented  
**Priority**: High  
**Effort**: Medium

Prevent double-charging on retries.

**Features**:

- ✅ Client-provided idempotency keys
- ✅ Automatic deduplication
- ✅ Configurable TTL (default: 24 hours)
- ✅ Key storage management
- ✅ Support for all storage backends (Memory, Firestore, Redis)
- ✅ Functional options pattern for API extensibility

**Implementation**:

- `pkg/goquota/types.go` - ConsumptionRecord type, ConsumeOptions
- `pkg/goquota/storage.go` - GetConsumptionRecord interface method
- `pkg/goquota/manager.go` - Idempotency checking in Consume()
- `storage/memory/memory.go` - In-memory idempotency support
- `storage/firestore/firestore.go` - Transaction-safe idempotency
- `storage/redis/redis.go` - Atomic idempotency via Lua scripts

---

## Priority 9: API Enhancements

Improvements to the core API.

### 9.1 Bulk Quota Check

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Low

Check multiple resources in one call.

**Features**:

- Multi-resource queries
- Batch response format
- Efficient storage queries

### 9.2 Conditional Consumption

**Status**: ✅ Implemented  
**Priority**: Medium  
**Effort**: Low

Try to consume without throwing errors.

**Features**:

- ✅ `TryConsume()` method
- ✅ Boolean success return
- ✅ Remaining quota info
- ✅ No exception overhead
- ✅ Support for idempotency keys
- ✅ Integration with warnings, metrics, and caching

**Implementation**:

- `pkg/goquota/types.go` - TryConsumeResult type
- `pkg/goquota/manager.go` - TryConsume() method implementation
- `pkg/goquota/manager_test.go` - Comprehensive test coverage (15 test cases)

**Benefits**:

- Non-throwing API for performance-critical paths
- Clear boolean success indicator
- Remaining quota information without additional calls
- Maintains all existing features (idempotency, warnings, metrics)

### 9.3 Partial Consumption

**Status**: 🔴 Not Started  
**Priority**: Low  
**Effort**: Low

Consume what's available.

**Features**:

- `ConsumeUpTo(max)` method
- Return actual consumed amount
- Partial success handling

---

## Priority 10: Documentation

Comprehensive documentation improvements.

### 10.1 Migration Guides

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Low

Help users migrate from other systems.

**Features**:

- Migration from custom solutions
- Migration from competitors
- Version upgrade guides
- Breaking change documentation

### 10.2 Best Practices

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Low

Document common patterns and anti-patterns.

**Features**:

- Architecture patterns
- Performance optimization
- Security considerations
- Scaling strategies

### 10.3 Troubleshooting Guide

**Status**: 🔴 Not Started  
**Priority**: Medium  
**Effort**: Low

Help users debug common issues.

**Features**:

- Common error scenarios
- Debug logging guide
- Performance profiling
- FAQ section

### 10.4 API Reference

**Status**: 🟡 Partial  
**Priority**: High  
**Effort**: Low

Complete API documentation.

**Features**:

- Godoc improvements
- Usage examples for all methods
- Error handling patterns
- Configuration reference

---

## Release Planning

### v0.x (Current)

- Core quota management
- Anniversary billing
- Prorated tier changes
- Firestore + in-memory storage
- HTTP middleware

### v1.0 (Target: Q2 2025)

**Focus**: Production readiness

- ✅ Caching layer (completed)
- ✅ Metrics & observability (completed)
- ✅ Soft limits & warnings (completed)
- ✅ Redis storage adapter (completed)
- ✅ Quota refunds (completed)
- ✅ Circuit breaker (completed)
- ✅ Idempotency keys (completed)
- ✅ Fallback strategies (completed)
- 🟡 Comprehensive documentation (partial)

### v1.1 (Target: Q3 2025)

**Focus**: Advanced features

- ✅ Conditional Consumption (TryConsume) (completed)
- ✅ Rate limiting (completed)
- Quota reservations
- Webhooks & events
- Admin API
- Testing utilities

### v1.2 (Target: Q4 2025)

**Focus**: Enterprise features

- Multi-tenancy support
- Hierarchical quotas
- Audit trail
- Additional storage adapters (PostgreSQL, MySQL)

### v2.0 (Target: 2026)

**Focus**: Advanced capabilities

- Sliding window quotas
- Quota forecasting
- Admin dashboard
- Multi-resource bundling
- Quota pools

---

## Contributing

We welcome contributions! Priority areas for community contributions:

1. **Storage Adapters** - PostgreSQL, MySQL, MongoDB, DynamoDB
2. **Framework Integrations** - Echo, Fiber, additional middleware
3. **Documentation** - Examples, tutorials, translations
4. **Testing** - Additional test coverage, integration tests
5. **Performance** - Benchmarks, optimizations

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

---

## Feedback

Have suggestions or want to prioritize a feature?

- 📧 Open an issue on GitHub
- 💬 Join discussions
- 🗳️ Vote on feature requests

---

**Last Updated**: 2025-01-22  
**Maintainer**: @mihaimyh
