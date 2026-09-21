# Tier Promotions (Temporary Tier Grants)

Status: Implemented
Audience: Application developers integrating `goquota`, storage adapter maintainers

## Motivation

Applications frequently need to give a user a **tier they did not pay for**, for a
bounded period of time:

- Support comps ("premium for 1 month after an incident")
- Marketing/promo codes ("unlock pro for 14 days")
- Partner/sandbox access
- Retention offers and win-back campaigns

`goquota` already exposes `Manager.SetEntitlement`, and `Entitlement` already has an
`ExpiresAt` field, so a naive implementation is tempting:

```go
manager.SetEntitlement(ctx, &goquota.Entitlement{
    UserID: "u1", Tier: "premium",
    ExpiresAt: ptr(time.Now().AddDate(0, 1, 0)),
})
```

This is incorrect in the current library for several reasons.

## Why the naive approach fails

1. **`ExpiresAt` is not enforced by the core.** Both `Consume` and `GetQuota`
   read `ent.Tier` directly. `ExpiresAt` is only inspected by `pkg/api` (for a
   display status) and by the RevenueCat webhook (once, at delivery time). A
   manually written `Entitlement{Tier: "premium", ExpiresAt: ...}` grants premium
   **forever** unless an external job downgrades it.
2. **It resets the billing anniversary.** `GetCurrentCycle` derives the monthly
   period from `SubscriptionStartDate`. Writing a fresh entitlement shifts the
   user's monthly boundary and rewrites their cycle.
3. **It breaks provider webhook idempotency.** RevenueCat and Stripe skip events
   that are not newer than `Entitlement.UpdatedAt`. A manual write stamped `now`
   can cause a legitimate, in-flight payment webhook to be dropped.
4. **Providers clobber it.** Providers construct a fresh `Entitlement{}` and call
   `SetEntitlement`, which overwrites any mutable field not included in that
   struct.
5. **There is no automatic reversion.** Restoring the previous tier would require
   a distributed scheduler, which the library deliberately does not have.

## Design

A promotion is a **time-boxed overlay on top of the provider-owned base
entitlement**, resolved at read time.

```
Entitlement
├── Tier                  (base, provider-owned)          ──┐
├── SubscriptionStartDate (base, provider-owned)            │ provider / billing owns
├── ExpiresAt             (base subscription expiry)       ──┘
├── UpdatedAt             (base, used for webhook idempotency)
└── Promotion             (overlay, managed by GrantPromotion/RevokePromotion)
    ├── Tier
    ├── GrantedAt
    ├── ExpiresAt
    ├── Source
    ├── Reason
    └── IdempotencyKey
```

While `now < Promotion.ExpiresAt` the promotion tier drives every quota, rate
limit, warning and consumption-order decision. When it expires, the base tier
resumes with **no background job**, because the effective tier is computed on
every read against the current time. A stale cache therefore self-corrects: the
cached record still contains the promotion, and the read-time check observes that
it has expired.

### Guarantees

| Guarantee | Mechanism |
| --- | --- |
| Promotion expires automatically | `ResolveEffectiveTier` checks `now` at every read |
| Billing anniversary is never touched | Promotion writes do not modify `SubscriptionStartDate` |
| Provider webhook idempotency is preserved | Promotion writes do not modify `UpdatedAt` |
| Provider writes cannot erase a promotion | `Storage.SetEntitlement` preserves an existing promotion when the incoming `Promotion` is nil |
| Repeat grants do not silently extend | `PromotionRequest.IdempotencyKey` short-circuits an active duplicate |
| Only configured tiers can be granted | `GrantPromotion` validates against `Config.Tiers` |
| Storage stays pluggable | Optional `PromotionStore` capability; unsupported backends return `ErrUnsupportedOperation` |

### Effective tier resolution

Resolution is centralised in one exported function:

```go
func ResolveEffectiveTier(ent *Entitlement, defaultTier string, now time.Time) string

// ergonomic wrappers for callers that already hold an entitlement
func (e *Entitlement) EffectiveTier(defaultTier string, now time.Time) string
func (e *Entitlement) ActivePromotion(now time.Time) *TierPromotion
```

The `Manager` uses it for every consumer of a user's tier (`Consume`,
`ConsumeWithResult`, `GetQuota`, `GetEffectiveQuota`, `GetMeterQuota`,
`TryConsume`, `SetUsage`). `ApplyTierChange` is unaffected because it takes
explicit tier names.

Because the effective tier is resolved on every read, read results already expose
it, so a caller never needs to resolve by hand:

| Result | Effective tier | Active overlay |
| --- | --- | --- |
| `*Usage` (from `GetQuota`) | `Usage.Tier` | `Usage.Promotion` |
| `*EffectiveQuota` | `.Tier` | `.Promotion` |
| `*MeterQuota` | `.Tier` | `.Promotion` |
| `*ConsumeResult` | `.EffectiveTier` | `.Promotion` |

> `Usage.Tier`, `EffectiveQuota.Tier` and `MeterQuota.Tier` are the **effective**
> tier, not `Entitlement.Tier`. `Entitlement.Tier` is the provider-owned base
> tier.

### Integrating with an existing tier-resolution layer

The most common integration bug is not in `goquota` — it is an application that
already has a `tier` string somewhere (a DTO, a cache key, a JWT claim, a Dart or
TypeScript enum) populated from `Entitlement.Tier`. That field now means "base
tier", so the quota engine grants `premium` while the app keeps showing `free`.

Checklist:

1. **Never read `Entitlement.Tier` for a decision or for display.** Replace it
   with `ent.EffectiveTier(defaultTier, now)` or `goquota.ResolveEffectiveTier`.
2. **Prefer a read result that already carries the effective tier.** A call to
   `GetQuota` / `GetEffectiveQuota` / `GetMeterQuota` / `ConsumeWithResult`
   returns the effective tier *and* the active overlay, so a second call (and a
   second resolution path) is unnecessary.
3. **Mirror `Promotion` next to `Tier` in any DTO that mirrors an entitlement.**
   If your DTO drops `Promotion`, it silently drops the effective tier too.
4. **Check the capability once at startup:**
   ```go
   if !manager.PromotionsSupported() {
       log.Fatal("storage does not implement PromotionStore")
   }
   ```
   `PromotionsSupported` (and the free function `SupportsPromotions(storage)`)
   unwraps wrappers such as `CircuitBreakerStorage`, so it reports the real
   backend capability. Without it you only find out at the first
   `GrantPromotion` (`ErrUnsupportedOperation`).
5. **Keep the base and effective domains separate in your own types.** A
   promotion-aware `EntitlementInfo` should expose both, e.g.
   `{BaseTier, EffectiveTier, Promotion}`, and the billing/badge UI should read
   `EffectiveTier` + `Promotion`.
6. **React to promotion lifecycle out of band.** `GrantPromotion` /
   `RevokePromotion` write audit entries but do not fire `WebhookCallback` (which
   is provider-only), and lazy expiry fires no event. Use the audit log or your
   own notification path.

`pkg/api` is a worked example: the usage handler calls
`goquota.ResolveEffectiveTier` / `ent.ActivePromotion` rather than inspecting
`Promotion.IsActive` itself.


### API

```go
type TierPromotion struct {
    Tier           string
    GrantedAt      time.Time
    ExpiresAt      time.Time
    Source         string
    Reason         string
    IdempotencyKey string
}

func (p *TierPromotion) IsActive(now time.Time) bool
func (e *Entitlement) ActivePromotion(now time.Time) *TierPromotion

// Canonical tier resolution (base tier overridden by an active promotion).
func ResolveEffectiveTier(ent *Entitlement, defaultTier string, now time.Time) string
func (e *Entitlement) EffectiveTier(defaultTier string, now time.Time) string

type PromotionRequest struct {
    UserID    string
    Tier      string        // must exist in Config.Tiers
    ExpiresAt time.Time     // required unless Duration is set
    Duration  time.Duration // convenience; ExpiresAt wins when both are set
    Source    string
    Reason    string
    IdempotencyKey string
}

func (m *Manager) GrantPromotion(ctx context.Context, req *PromotionRequest) (*Entitlement, error)
func (m *Manager) RevokePromotion(ctx context.Context, userID string) error
func (m *Manager) GetEffectiveTier(ctx context.Context, userID string) (string, error)

// Startup capability check.
func SupportsPromotions(storage Storage) bool
func (m *Manager) PromotionsSupported() bool
```

`GrantPromotion`:

1. Validates the request (user, tier exists, expiry is in the future).
2. Verifies the storage implements `PromotionStore`.
3. Ensures a base entitlement exists; creates one at the default tier if not
   (so the user falls back to `DefaultTier` when the promotion expires).
4. Returns the current entitlement unchanged when the same `IdempotencyKey` is
   already active (safe webhook/admin retries).
5. Writes only the promotion via `SetPromotion`, invalidates the entitlement
   cache, and writes an audit entry (`grant_promotion`).

`RevokePromotion` clears the overlay (idempotent) and audits `revoke_promotion`.

### Duration semantics

`Duration` is a fixed `time.Duration` (30 days is not one calendar month). For a
calendar-month promotion, pass an explicit expiry:

```go
now := time.Now().UTC()
manager.GrantPromotion(ctx, &goquota.PromotionRequest{
    UserID:    "u1",
    Tier:      "premium",
    ExpiresAt: now.AddDate(0, 1, 0), // calendar month, matches provider defaults
    Source:    "support_comp",
    Reason:    "incident_2026_09",
})
```

### Expiry and provider interaction

- A provider webhook that arrives **during** a promotion updates the base tier
  (`Tier`, `ExpiresAt`, `SubscriptionStartDate`, `UpdatedAt`) but never the
  overlay. When the promotion ends the store looks up the base tier again.
- A provider `SyncUser` (Restore Purchases / nightly reconciliation) cannot erase
  a promotion, because `SetEntitlement` preserves an existing `Promotion`.
- The `WebhookCallback` only fires for provider events; it does **not** fire for
  manual grants. Use the audit log / your own notification path for promotion
  lifecycle events.

### Storage contract

`SetEntitlement` preserves a stored promotion when the incoming
`Entitlement.Promotion` is nil. To clear a promotion, use `RevokePromotion` /
`ClearPromotion`. This makes provider writes safe by construction and keeps the
existing `Storage` interface unchanged for backends that do not implement
`PromotionStore`.

```go
// PromotionStore is an optional Storage capability.
type PromotionStore interface {
    SetPromotion(ctx context.Context, userID string, promo *TierPromotion) error
    ClearPromotion(ctx context.Context, userID string) error
}
```

Backends:

| Backend | Promotion storage | Notes |
| --- | --- | --- |
| memory | field on in-memory entitlement | trivially atomic under the store lock |
| Postgres | `entitlements.promotion JSONB` column | `COALESCE(EXCLUDED.promotion, <table>.promotion)` preserves on base writes |
| Redis | field inside the entitlement JSON blob | read-modify-write inside the adapter |
| Firestore | `promotion` map field | `MergeAll` omits the key on base writes, preserving it |
| Tiered (hot/cold) | delegates to cold, then hot | write-through, cold is source of truth |
| Circuit-breaker wrapper | delegates to inner storage | returns `ErrUnsupportedOperation` when the inner store has no capability |

Postgres deployments must apply the column migration (the adapter also adds it
lazily with `ADD COLUMN IF NOT EXISTS`):

```bash
psql -d goquota -f storage/postgres/migrations/004_tier_promotions.sql
```

### Usage API

`GET /api/v1/me/usage` (or any route wired to `api.Handler`) reports the
effective tier and, when a promotion is active, a `promotion` block:

```json
{
  "user_id": "u1",
  "tier": "premium",
  "status": "active",
  "promotion": { "tier": "premium", "expires_at": "2026-10-21T00:00:00Z", "source": "support_comp" },
  "resources": { }
}
```

### Explicit non-goals

- **MergeUser does not move promotions.** Merging identities transfers usage and
  forever credits; promotions are intentionally out of scope so that identity
  migration never grants access the target did not have. Grant explicitly after a
  merge if required.
- **No tier ranking.** Tier names are application-defined, so the library cannot
  decide that `premium > pro`. A promotion always overrides the base while
  active; whether granting it makes sense is the caller's policy.
- **No scheduling.** Expiry is lazy and read-time; there is no worker to operate.

### Security notes

- `GrantPromotion` is a privileged operation. Do not expose it directly to
  untrusted clients; gate it behind admin/support authorization.
- Tier names are validated against `Config.Tiers`, preventing promotions to
  undefined tiers that would silently fall back to default limits.
- `Source`/`Reason`/`IdempotencyKey` are stored and audited for attribution.
