# FINAL AUDIT REPORT — goquota

Audit window: 2026-09-20 · Auditor: Principal Systems Architect (iterative, 7-module audit)
Artifacts: `.tmp/audit/audit_state.json`, `.tmp/audit/findings/*.md`, `.tmp/audit/repros/*_test.go`

---

## 1. Executive Summary & Stack Overview

**What this is.** `github.com/mihaimyh/goquota` is a Go 1.27 library for subscription
quota management: anniversary-based billing cycles, prorated tier changes, daily/
monthly/forever credits, pluggable storage (memory/Redis/Postgres/Firestore/
tiered), rate limiting, billing-provider webhooks (Stripe, RevenueCat), HTTP
middleware for four frameworks, a standardized Usage API, and Prometheus/zerolog
observability.

**Architectural posture — good.** The layering is clean and dependency-inverted:
the `goquota.Storage` interface lives in the core package and all persistence is
injected *upward*. No lower layer imports a higher one (verified; the only
core→storage edges are in `_test.go` files). The domain model is immutable-by-copy
across the storage boundary, configuration fails fast and aggregates all errors,
and the resilience features (cache, circuit breaker, fallback) are optional,
capability-detected, and nil-safe. This is a well-structured library.

**Runtime health — mixed.** The shipped test suite is **green except for one
state-dependent Firestore test** (global run: 887 pass / 8 skip / 1 fail; Postgres
integration suite additionally passes behind `-tags integration`). `go build ./...`
passes, but **`go vet ./...` fails** repo-wide. The audit found **24 verified
defects** across the 7 modules — the serious ones cluster in the storage adapters
and billing webhooks, i.e. at the *edges* where the clean core meets real I/O.

**Headline risks (verified, reproduced against HEAD):**

1. **`ApplyTierChange` hardcodes the resource `"audio_seconds"`** in Redis and
   Firestore — tier changes for any other resource silently corrupt a phantom
   record and never update the real one. (Critical)
2. **Redis `AddLimit` credits are unspendable** — top-ups write only the `limit`
   hash field while `GetUsage` requires `data`, so purchased credits are invisible
   and `Consume(Forever)` returns `ErrQuotaExceeded`. (High)
3. **Cross-user idempotency replay** on Redis/Firestore/Postgres — reusing another
   user's idempotency key skips the charge and returns the first user's `newUsed`
   (quota bypass + cross-user data leak). (High)
4. **`X-Forwarded-For` is trusted verbatim** by the billing webhook rate limiter,
   so it is trivially bypassed and allocates one bucket per spoofed IP while its
   cleanup degrades to **O(N²)**. (High)
5. **Multiple panics on `Rate == 0`** (memory sliding-window index OOB, memory and
   Firestore token-bucket divide-by-zero) reachable through `Manager.Consume`.
   (High)
6. **`InitialForeverCredits` grants only one resource per tier** (shared
   idempotency key), and the **optimistic fallback cap is defeated by a
   concurrency race** (`allowed 5120` against a `100` cap). (High)
7. **`net/http` middleware nil-deref panic** when rendering a quota-exceeded
   response during a storage outage. (High)
8. **Four framework examples crash on startup** (missing `TierConfig.Name`) and
   `go vet` fails. (Medium)

**Ecosystem posture.** No secrets committed; credentials are env-driven. The
`examples/comprehensive` app is a genuinely strong reference (k6 harness, Grafana
dashboards, guarded metrics wiring, merge endpoints). The library’s main
architectural gap is **multi-instance correctness**: caches are process-local and
invalidated only on the writing instance, and Postgres-backed rate limits are
per-process (not shared). Correctness in a horizontally scaled deployment relies
on TTL expiry rather than deterministic invalidation (see §5, X3).

---

## 2. Subsystem & Feature Catalog

### Packages (33)

| Layer | Package | Responsibility |
|---|---|---|
| Core | `pkg/goquota` | Manager, cycles, entitlements, storage contract, admin ops, merge, cache/CB/fallback/rate-limit primitives, metrics/logger interfaces |
| Persistence | `storage/memory` | In-process maps; `Clear`; `MergeUser`; `TimeSource` |
| Persistence | `storage/redis` | Atomic Lua scripts; TTLs; `TIME` source; no `MergeUser` |
| Persistence | `storage/postgres` | SQL tx + `SELECT FOR UPDATE`; embedded memory rate limiter; cleanup worker |
| Persistence | `storage/firestore` | Transactions + sub-collections; temp-doc time source |
| Persistence | `storage/tiered` | Hot/Cold orchestration (read-through/write-through/hot-only/async-audit) |
| Transport | `middleware/http`, `gin`, `echo`, `fiber` | Quota enforcement + `X-RateLimit-*`/`Retry-After` |
| Product API | `pkg/api` | `GET` usage snapshot (`GetMeterQuota`) |
| Billing | `pkg/billing` + `stripe`, `revenuecat`, `internal`, `metrics/prometheus` | Provider abstraction, webhooks, checkout/portal, sync/transfer |
| Observability | `pkg/goquota/metrics/prometheus`, `pkg/goquota/logger/zerolog` | Prometheus collectors; zerolog adapter |
| Demos | `examples/*` (14) | Quickstarts, backends, frameworks, comprehensive k6/Grafana stack |

### Dependency graph (verified: acyclic, no inversion)

```
                         examples/*  (top; imports all adapters)
                              |
        +---------------------+----------------------+
        |            |               |                |
   middleware/*   pkg/api      pkg/billing      storage/*
        |            |          /    |    \           |
        |            |   stripe  revenuecat internal   |
        +------------+----------+----------+-----------+
                              |
                         pkg/goquota  (core; base)
                              |
                    +---------+---------+
                    |                   |
     pkg/goquota/metrics/prometheus   pkg/goquota/logger/zerolog
```

All arrows point **toward** the core; `storage/postgres → storage/memory` is a
same-layer reuse: it embeds `*memory.Storage` purely for rate limiting.

---

## 3. Cross-Module Architectural & Boundary Review

| ID | Finding | Evidence | Verdict |
|---|---|---|---|
| **X1** | **Layering is clean** — no lower-level package imports transport/UI. Only `_test.go` files under `pkg/goquota` import `storage/memory`. | `grep` over `pkg/goquota/*.go`, `storage/**`, observability: zero production upward imports | ✅ Preserve |
| **X2** | **Shared mutable global: the Prometheus default registry.** `pkg/goquota/metrics/prometheus.DefaultMetrics` and `pkg/billing/metrics/prometheus.DefaultMetrics` both `MustRegister` on `prometheus.DefaultRegisterer`. Two consumers using the same namespace panic; even one consumer calling `DefaultMetrics` twice panics (OBS-2 / BILLING-5). The comprehensive example works around this with a `metricsCreated` flag — fragile. | `prometheus.go:58-59,400-403`; `pkg/billing/metrics/prometheus/prometheus.go:32-130,187-189`; repro `TestPrometheusDefaultMetricsDuplicatePanics` | ⚠️ Fix (memoize / register-or-reuse) |
| **X3** | **Generational desynchronization across instances.** `Manager`'s entitlement/usage LRU cache is process-local and invalidated only on the instance that performs the write (`cache.InvalidateEntitlement`). A billing webhook on instance A updates storage; instance B keeps serving the stale tier until `EntitlementTTL` (default 1 min; examples use 5 min). Worse, billing idempotency compares `eventTimestamp.After(existing.UpdatedAt)` against the **cached** entitlement, so a stale entry can cause a newer webhook to be skipped. There is no cross-instance invalidation bus (Redis pub/sub etc.). | `manager.go:1452-1506`, `:1367-1409`; `storage/*` writes; `pkg/billing/*/webhook.go` idempotency checks | ⚠️ Document + consider pub/sub invalidation; short TTLs |
| **X4** | **Idempotency scoping is inconsistent at the `Storage` boundary.** `GetConsumptionRecord(ctx, key)` / `GetRefundRecord(ctx, key)` omit `userID`. Postgres stores per-user but looks up globally (with a comment saying the Manager must verify `UserID` — it does not); Redis/Firestore key globally. Result: cross-user replay (STORAGE-4). | `storage.go:36-42`; `postgres.go:653-750`; `redis.go:834-874`; `firestore.go:291,348`; `manager.go:511-520,1543-1552` | ⚠️ Fix interface or Manager check |
| **X5** | **Time-source boundary inconsistency.** Core derives periods from `m.now(ctx)` (storage `TimeSource`), but `ApplyTierChange` proration uses local `time.Now().UTC()` (`manager.go:1301`), and `MemoryRateLimiter` uses `time.Now()` regardless of TimeSource. Under clock-skew protection the proration fraction can disagree with the period it is applied to. | `manager.go:1301`; `rate_limiter_memory.go:48` | ⚠️ Fix (use `m.now`) |
| **X6** | **Global mutable state elsewhere is absent.** Package-level `var` blocks are sentinel errors only (`errors.go`, `circuit_breaker.go`, `billing/errors.go`, `internal/http_utils.go`). No global caches, no un-synchronized singletons in the library. | `grep '^var '` | ✅ Preserve |
| **X7** | **Rate-limit state is per-process for Postgres.** `storage/postgres` embeds `*memory.Storage` for rate limiting, so limit enforcement multiplies by instance count (documented in `examples/postgres/main.go:127`). Redis/tiered limits are shared. | `postgres.go:27,162` | ⚠️ Document; Redis for distributed limits |

---

## 4. Master Critical Bugs Table (Verified Only)

Every entry below except **F11** has a failing standalone reproduction in
`.tmp/audit/repros/`. F11 is code-evidence only (it requires a live Stripe
account) and is included for roadmap completeness, clearly marked. Run any repro with:

```
go test -count=1 -run '<TestName>' -v ./.tmp/audit/repros/ -timeout 1500s
```

### Verified bug index (F-numbering)

| F | Module | Severity | Defect |
|---|---|---|---|
| F1 | storage | Critical | `ApplyTierChange` hardcodes `audio_seconds` (Redis/Firestore) |
| F2 | storage | High | Redis `AddLimit` credits invisible → top-ups unspendable |
| F3 | storage | High | Cross-user idempotency replay (Redis/Firestore/Postgres) |
| F4 | storage | High | Memory sliding window locks out permanently after window |
| F5 | storage+core | High | `Rate == 0` panics (memory SW, memory TB, Firestore TB) |
| F6 | core | High | `InitialForeverCredits` applied to only one resource |
| F7 | core | High | Optimistic fallback cap defeated by concurrency race |
| F8 | middleware | High | `net/http` middleware nil-deref panic on storage error |
| F9 | billing | High | Webhook rate limiter bypass via spoofed `X-Forwarded-For` |
| F10 | billing | Med-High | RevenueCat tier resolution nondeterministic (map iteration) |
| F11 | billing | Medium | Stripe credit-pack refunds can't resolve metadata **(static — not repro'd)** |
| F12 | core | Medium | LRU cache never reclaims expired entries |
| F13 | storage | Medium | `memory.Clear` leaves `mergeRecords` |
| F14 | usage_api | Medium | Storage failures swallowed → HTTP 200 with empty data |
| F15 | usage_api | Medium | Usage-API metric status polluted by entitlement status |
| F16 | observability | Medium | `rate_limit_exceeded_total` double-counted |
| F17 | billing | Medium | Rate-limiter cleanup is O(N²) once >200 IPs |
| F18 | middleware | Medium | `Retry-After` rounds sub-second delays to `0` (all 4) |
| F19 | usage_api | Low-Med | `hasActiveQuota` is dead code (inactive resources reported) |
| F20 | observability+billing | Low-Med | `DefaultMetrics`/`NewMetrics` not idempotent → panic |
| F21 | observability | Low | `hybrid_billing_users_total` is a monotonic Gauge named `_total` |
| F22 | billing | Low | RevenueCat `SyncUser` doesn't URL-escape `userID` |
| F23 | storage | Low | Firestore test suite not isolated (fixed key/collections) |
| F24 | examples | Medium | Four framework examples crash (missing `TierConfig.Name`) |
| F25 | examples | Medium | `go vet ./...` fails on unkeyed `goquota.Field` literals |

### Critical/high-severity detail

| F | Root cause | Anchor | Failure output | Minimal fix |
|---|---|---|---|---|
| **F1** | Literal resource instead of `req.Resource` | `storage/redis/redis.go:617,622`; `storage/firestore/firestore.go:379,399` | `ApplyTierChange(resource=videos) created no videos usage (audio_seconds=…)` | `s.usageKey(req.UserID, req.Resource, …)` / `"resource": req.Resource` |
| **F2** | Lua `AddLimit` writes only `limit`; `GetUsage` requires `data` | `storage/redis/redis.go:897-927` vs `:419-426` | `credits added by AddLimit are invisible to GetUsage (top-up lost)` | Write `data` JSON in the script, or let `GetUsage` read `limit` when `data` absent |
| **F3** | Lookup keyed by idempotency key only; no user binding | `storage/{redis,firestore,postgres}` + `manager.go:511-520,1543-1552` | `user B was not charged (used=0, consume returned newUsed=1): cross-user idempotency replay` | Add `userID` to `Get*Record`, or reject replay when `record.UserID != userID` |
| **F4** | `validStart` stays `0` when all timestamps expired | `storage/memory/memory.go:432-449` | `sliding window did not reset after the window elapsed: rate limit exceeded` | `validStart := len(window.timestamps)` |
| **F5** | Missing zero-rate guards | `storage/memory/memory.go:393,444-446`; `storage/firestore/firestore.go:772,782`; `pkg/goquota/rate_limiter_memory.go:167-170` | `index out of range [0] with length 0` / `integer divide by zero` (×3) | Guard `refillRate <= 0` and `len==0`; block with `ResetTime = now+window` |
| **F6** | One idempotency key for all resources | `pkg/goquota/manager.go:1385-1401` | `resource "images": expected initial forever credit to be spendable, got quota exceeded` | `fmt.Sprintf("initial_bonus_%s_%s", ent.UserID, resource)` |
| **F7** | Check under `RLock`, absolute write under separate `Lock` | `pkg/goquota/fallback.go:193-215` | `optimistic cap violated: allowed 5120 units, cap was 100` | Single write lock; `current + amount` atomically |
| **F8** | `usage` dereferenced when `GetQuota` errored | `middleware/http/middleware.go:139-147` | `middleware panicked … invalid memory address or nil pointer dereference` | Guard `err == nil && usage != nil`; fall back to `"Quota exceeded"` |
| **F9** | `X-Forwarded-For` trusted unconditionally | `pkg/billing/internal/rate_limiter.go:107-116` | `rotating X-Forwarded-For bypassed webhook rate limiting: no 429 in 1000 requests` | Trust XFF only behind configured proxies; otherwise `RemoteAddr`; cap map |
| **F10** | `range p.tierMapping` returns first active entitlement | `pkg/billing/revenuecat/webhook.go:251-259`; `sync.go:197-247` | `tier resolution is nondeterministic … map[premium:344 pro:56]` | Sort candidates or adopt Stripe-style `TierWeights` |
| **F11** | Session metadata set, but refund reads PaymentIntent metadata | `pkg/billing/stripe/checkout.go:117-120` vs `webhook.go:744-757` | static (refund handler returns 500) | Set `PaymentIntentData.Metadata` on the session |
| **F12** | Expired entries treated as misses but not deleted | `pkg/goquota/cache.go:123-146,193-221` | `expected expired entry to be reclaimed, stats size = 1` | `delete(...)` on miss |
| **F24** | `TierConfig.Name` omitted | `examples/{gin,echo,chi,gorilla}/main.go` | `panic: … tier 'free' has mismatched name in config: ''` | Add `Name: "free"/"pro"` |
| **F25** | Unkeyed struct literal | `examples/observability/main.go:48,57,69,71` | `go vet ./...` → `Field struct literal uses unkeyed fields` | `goquota.Field{Key: …, Value: …}` |

Full root-cause narratives and diffs: `.tmp/audit/findings/<module>.md`.

---

## 5. Master Algorithmic & Scalability Bottlenecks

Host: Intel i5-14400F, Go 1.27.0, windows/amd64. All figures from `.tmp/audit/repros`.

| B | Location | Big-O | Multi-scale evidence | Recommended replacement |
|---|---|---|---|---|
| **A1** | `pkg/goquota/cycle.go:35-49` `CurrentCycleForStart` | **O(months since start)** on every `Consume`/`GetQuota` | 1 mo 501 ns · 1 yr 2.8 µs · 10 yr 21 µs · 50 yr 115 µs · **zero-value 4.78 ms** | Compute `monthsElapsed` arithmetically; one adjustment, O(1) |
| **A2** | `pkg/goquota/cache.go:156-175,231-250` LRU eviction | **O(N) per insert at capacity** | `max_1000` 13.4 µs · `max_4000` 116 µs (8.6×) · `max_16000` 448 µs (33×) | `container/list` intrusive LRU; drop expired first |
| **A3** | `pkg/goquota/rate_limiter_memory.go:38-39` | **Unbounded map retention** | 200,000 `slidingWindows`, never reclaimed after windows expire | TTL/LRU the key space |
| **A4** | `storage/firestore/firestore.go:797-860` | **Unbounded doc growth** | timestamps after window: 100→100 · 1,000→1,000 · 10,000→10,000 (158 s) | Delete expired timestamp docs; TTL policy |
| **A5** | `pkg/billing/internal/rate_limiter.go:45-54` | **O(N²)** once >200 distinct IPs | 2.93 ms → 16.0 ms → **851 ms** for N=100/1k/10k (10× N → 53× time) | Cleanup once per interval; bounded/incremental eviction |
| **A6** | `pkg/api/handler.go:267-273` | **Linear N+1** (12 storage calls/resource) | 1.55 ms / 7.25 ms / 66.4 ms; exactly 7 `GetEntitlement`+5 `GetUsage` per resource | Hoist `GetCurrentCycle` out of the loop; batch reads |
| **A7** | `storage/firestore/firestore.go:35-81` `Now()` | **3 round-trips per call**, 1–2× per op | static (Set+Get+Delete temp doc) | Cache server-time offset against local clock |
| **A8** | `storage/memory/memory.go:37-42` | **Unbounded map retention** | consumptions/slidingWindows grow 1:1; also affects Postgres (embedded) | Enforce `IdempotencyKeyTTL`; bound rate-limit maps |

Constant-time modules (no bottleneck found): `middleware` (2,247 ns/op, 18 allocs, flat
to ~788k requests) and `observability` Prometheus recording (~112–129 ns/op,
**0 allocs**, flat from 0→10,000 label series).

---

## 6. Global Phased Implementation Roadmap

### Phase 1 — High Priority (fix immediately): correctness, security, data loss

| Order | F | Action | Blast radius |
|---|---|---|---|
| 1 | F1 | Use `req.Resource` in Redis + Firestore `ApplyTierChange` | Silent data corruption for every non-`audio_seconds` tier change |
| 2 | F2 | Make Redis `AddLimit` write a readable usage record | Paid credits become unspendable |
| 3 | F3 | Bind idempotency replay to the requesting user | Quota bypass + cross-user data leak |
| 4 | F4/F5 | Fix memory sliding-window reset; guard `Rate == 0` everywhere | Permanent rate-limit lockout; request-path panics |
| 5 | F6/F7 | Per-resource bonus key; atomic optimistic check-and-act | Lost sign-up credits; unbounded outage overspend |
| 6 | F8 | Nil-guard the `net/http` quota-exceeded branch | Request handler panic during storage outages |
| 7 | F9 | Stop trusting `X-Forwarded-For`; bound the limiter map | Webhook DoS bypass + unbounded memory |
| 8 | F10/F11 | Deterministic RevenueCat tier (weights); set PaymentIntent metadata | Wrong tier on multi-entitlement users; failed refunds |
| 9 | F14/F16/F18 | 500 on total storage failure; fix metric double-count; ceil `Retry-After` | Silent outages, wrong alerts, client retry storms |
| 10 | F24/F25 | Add `TierConfig.Name`; key `goquota.Field` literals | Examples don't run; CI vet gate fails |

### Phase 2 — Medium Priority (backlog): performance, resource limits, test blind spots

- **Algorithmic:** A1 (O(months) cycle), A2 (O(N) LRU eviction), A5 (O(N²) limiter cleanup), A6 (12 calls/resource), A7 (Firestore `Now()` write amplification).
- **Resource pruning:** A3/A4/A8 — TTL/LRU for memory limiter + memory storage maps, delete Firestore timestamp docs (and configure the TTL policy), enforce `IdempotencyKeyTTL` in `memory`.
- **Correctness/consistency:** F12 (delete expired cache entries), F13 (`Clear` mergeRecords), F15 (metric status label), F19 (dead discovery / decision), F20 (idempotent metrics), F21 (gauge/counter semantics), F22/F27 (URL/query escaping, sub-second Stripe ordering).
- **Architecture (X2/X3/X4/X5):** memoize `DefaultMetrics`; add cross-instance cache invalidation (Redis pub/sub or a version stamp) or shorten TTLs and document; bind idempotency keys to users at the interface; use `m.now(ctx)` for proration.
- **Test-suite blind spots:** metrics tests assert only “was it recorded?” (no values/types) — add value assertions; add storage-failure paths to `pkg/api` and `pkg/middleware/http`; fix Firestore test isolation (F23) so `go test ./...` is deterministic; add zero-rate and multi-resource-bonus cases to the core suite.

---

## 7. What NOT to Change (preserve)

1. **The `Storage` interface + optional-capability pattern.** `TimeSource`, `AuditLogger`, `RemainingDrainer`, `UserMerger` are detected by type assertion, with `ErrUnsupportedOperation` for backends that can't comply. It keeps the core interface small while allowing Redis/tiered to legitimately refuse non-atomic operations.
2. **Copy-on-read at the storage/cache boundary.** `memory.GetEntitlement/GetUsage` and `LRUCache` return shallow copies; callers cannot mutate cached/persisted state. Keep this even though it allocates — it prevents class-wide aliasing bugs.
3. **Anniversary cycle math.** `addMonthsSafeWithDay` correctly implements Jan-31 snap-back (no date drift across month lengths). This is subtle, well-tested, and exactly right; only the surrounding loop (A1) needs work.
4. **`singleflight` + double-checked cache + fail-fast validation.** Stampede control around `GetEntitlement`/`GetUsage` and the aggregated `Config.Validate` errors are the right shapes. Extend them; don't replace them.
5. **Tiered storage's strategy split and its refusal to implement `MergeUser`.** Read-through / write-through / hot-only / hot-primary-async-audit is a pragmatic, well-documented design, and declining a non-ACID two-store merge is the correct call. Likewise keep **timing-safe webhook verification** (`subtle.ConstantTimeCompare`/`hmac.Equal`, `stripe.ConstructEvent`) and **body-size limits**, and the metrics hygiene of **never labelling by `userID`**.

---

## 8. Global Verification Snapshot

| Command | Result |
|---|---|
| `go build ./...` | ✅ pass |
| `go vet ./...` | ❌ fail — 5 unkeyed `goquota.Field` literals in `examples/observability` (F25) |
| `go test -count=1 -json ./...` | **887 pass · 8 skip · 1 fail** · Σ package elapsed ≈ **33.2 s** |
| ↳ sole failure | `storage/firestore TestMergeUser_Emulator` (`first merge replayed`) — state-dependent, F23; passes after emulator reset |
| `go test -tags integration -count=1 ./storage/postgres/` | ✅ ok (6.78 s) |
| `go test -race -count=1 ./storage/memory/ ./storage/tiered/` | ✅ ok (no races) |

Repro suite: 12 files in `.tmp/audit/repros/`; the failing tests above are the
intended, verified failures against HEAD.

---

## 9. Teardown

The deliverable of this audit is this file plus `.tmp/audit/findings/*.md`. When you
are ready to remove the scratch tree, run one of:

```powershell
# Windows / PowerShell (from the repo root)
Remove-Item -Recurse -Force .tmp\audit
```

```bash
# macOS / Linux
rm -rf .tmp/audit
```

If the audit databases are still running, stop them too:

```bash
docker compose -p goquota-audit-20260920 down -v
# or, if they were started manually:
docker rm -f goquota-audit-20260920-postgres-1 goquota-audit-20260920-redis-1 goquota-audit-20260920-firestore-1
```

`FINAL_AUDIT_REPORT.md` is written to the repository root and is **not** removed by
the commands above.

---

## 10. Post-Remediation Verification (executed)

**Date:** 2026-09-21 · **HEAD:** `0050184` · All remediation commits are local on
`main` (nothing pushed).

### Global gates
| Command | Result |
|---|---|
| `go build ./...` | ✅ pass |
| `go vet ./...` | ✅ pass (was failing on `examples/observability` → EX-2) |
| `go test -race -count=1 ./...` | ✅ pass — 16 packages |

### Fixed findings (29) — commit map
| Defect | Severity | Commit |
|---|---|---|
| STORAGE-1-REDIS | Critical | `c82ff18` |
| STORAGE-1-FIRESTORE | Critical | `c82ff18` |
| STORAGE-5 | High | `034bf6a` |
| STORAGE-4 | High | `153b331` |
| STORAGE-2 | High | `86390a3` |
| STORAGE-3-MEMORY | High | `b294cdd` |
| STORAGE-3-FIRESTORE | High | `dd6d13a` |
| BUG-2 | High | `4db4858` |
| BUG-1 | High | `2dd730a` |
| BUG-3 | High | `368ac82` |
| MIDDLEWARE-1 | High | `0018c44` |
| BILLING-1 | High | `e615ee9` |
| BILLING-3 | High | `3253fad` |
| CORE-RACE-1 | High | `e43f01e` |
| BILLING-6 | Medium | `2bba743` |
| BUG-4 | Medium | `b8ea1b0` |
| STORAGE-6 | Medium | `41e7df7` |
| API-2 | Medium | `61bc452` |
| API-1 | Medium | `ea63a76` |
| OBS-1 | Medium | `3137a7f` |
| MIDDLEWARE-2 | Medium | `6d3ec80` |
| OBS-2 (+BILLING-5) | Medium | `2fc71f5` |
| EX-1 | Medium | `d3a583f` |
| EX-2 | Medium | `ce3afdf` |
| PG-ISOLATION-1 | Medium | `8ce4b9a` |
| REDIS-FLAKE-1 | Medium | `862ae5b` |
| CB-FLAKE-1 | Medium | `b3f8a19` |
| FIRESTORE-FLAKE-1 | Medium | `1bb2ef7` |
| TEST-RACE-1 | Low | `6815771` |

### Backlog clearance (9 additional fixes, executed after §10)
| Defect | Kind | Commit | Evidence |
|---|---|---|---|
| API-3 | Correctness | `b962266` | `TestUsageAPI_ExcludesInactiveResources`; discovery now propagates storage errors so API-2's 500 path still fires |
| BILLING-4 | Security | `274468c` | `TestSyncUserEscapesUserID` — subscriber id URL-escaped |
| OBS-3 | Observability | `82f5a99` | `TestPrometheusMetrics_HybridBillingIsCounter` — gauge → counter |
| CYCLE-O1 | Performance | `d033342` | `CurrentCycleForStart` O(months) → O(1); zero-value 4.78ms → ~182ns |
| LRU-O1 | Performance | `07634ad` | `LRUCache` eviction O(N) → O(1); ~228ns flat at N=1k/16k (was 13µs/448µs) |
| BILLING-2 | Performance | `d7a1bd0` | Webhook limiter no longer sweeps the full map per request; N=10k 851ms → 37ms |
| FIRESTORE-TTL | Resource | `9b3a8e2` | `TestStorage_CheckRateLimit_SlidingWindow_CleansExpiredTimestamps` |
| RL-EVICT | Resource | `f03aee9` | `TestMemoryRateLimiter_EvictsIdleKeys` |
| MEM-EVICT | Resource | `0050184` | `TestStorage_CheckRateLimit_EvictsIdleKeys` |

### Audit reproduction suite after remediation
Re-running `.tmp/audit/repros/`: the three previously-excluded Low findings
(`API-3`, `BILLING-4`, `OBS-3`) now pass. The remaining failures are stale repros,
not product regressions:
- **2 stale repros:**
  - `TestFrameworkExampleConfigsFailValidation` hard-codes the *old* broken example
    config; the examples themselves now start (verified by running all four).
  - `TestRevenueCatMultiEntitlementTierResolutionNondeterministic` now fails at
    request 100 with `429` because it relied on rotating `X-Forwarded-For`, which
    the BILLING-1 security fix intentionally ignores. The durable regression
    `TestProvider_MultiEntitlement_TierResolutionDeterministic` (unique `RemoteAddr`)
    passes.

### Remaining (intentional / by design)
- The memory storage maps for idempotency records (`consumptions`, `refunds`,
  `topUps`, `mergeRecords`) remain unbounded by design: they are needed for
  idempotency and the adapter is intended for tests/dev. Production backends
  (Redis/Postgres/Firestore) expire them. Rate-limit state maps are now bounded
  via idle eviction (RL-EVICT / MEM-EVICT).
- No other verified High/Medium findings are open. Nothing has been pushed; all
  commits are local on `main`.

