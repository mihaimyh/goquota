package revenuecat

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
)

const rcAnonymousIDPrefix = "$rcanonymousid:"

// filterAppUserIDs keeps Firebase / custom app user ids and drops
// RevenueCat anonymous aliases ($RCAnonymousID:…).
func filterAppUserIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(id), rcAnonymousIDPrefix) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

type transferIdentityPayload struct {
	Event struct {
		TransferredFrom []string `json:"transferred_from"`
		TransferredTo   []string `json:"transferred_to"`
	} `json:"event"`
}

func parseTransferIdentities(body []byte) (from, to []string) {
	var extra transferIdentityPayload
	if err := json.Unmarshal(body, &extra); err != nil {
		return nil, nil
	}
	return extra.Event.TransferredFrom, extra.Event.TransferredTo
}

func (p *Provider) processTransferEvent(ctx context.Context, payload *webhookPayload, body []byte) error {
	rawFrom, rawTo := parseTransferIdentities(body)
	fromIDs := filterAppUserIDs(rawFrom)
	toIDs := filterAppUserIDs(rawTo)
	if len(fromIDs) == 0 && len(toIDs) == 0 {
		return nil
	}

	eventTimestamp := parseEventTimestamp(payload.getEventTimestamp())
	snapshot := p.bestTransferSnapshot(ctx, fromIDs)

	for _, destID := range toIDs {
		if snapshot == nil {
			continue
		}
		if err := p.applyTransferredEntitlement(ctx, destID, snapshot, eventTimestamp, payload.Event.Type); err != nil {
			return err
		}
	}

	for _, sourceID := range fromIDs {
		if err := p.downgradeTransferredSource(ctx, sourceID, eventTimestamp, payload.Event.Type); err != nil {
			return err
		}
	}

	return nil
}

func (p *Provider) bestTransferSnapshot(ctx context.Context, fromIDs []string) *goquota.Entitlement {
	var best *goquota.Entitlement
	for _, id := range fromIDs {
		ent, err := p.manager.GetEntitlement(ctx, id)
		if err != nil || ent == nil {
			continue
		}
		if best == nil || paidTierRank(ent.Tier, p.defaultTier) > paidTierRank(best.Tier, p.defaultTier) {
			copyEnt := *ent
			best = &copyEnt
		}
	}
	return best
}

func paidTierRank(tier, defaultTier string) int {
	if tier == "" || tier == defaultTier {
		return 0
	}
	return 1
}

func (p *Provider) applyTransferredEntitlement(
	ctx context.Context,
	userID string,
	snapshot *goquota.Entitlement,
	eventTimestamp time.Time,
	eventType string,
) error {
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return err
	}
	if skipStaleTransfer(existing, eventTimestamp) {
		return nil
	}

	previousTier := p.defaultTier
	if existing != nil {
		previousTier = existing.Tier
	}

	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  snapshot.Tier,
		SubscriptionStartDate: snapshot.SubscriptionStartDate,
		ExpiresAt:             snapshot.ExpiresAt,
		Timezone:              snapshot.Timezone,
		UpdatedAt:             transferUpdatedAt(eventTimestamp, existing),
	}

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return err
	}

	return p.invokeWebhookCallback(ctx, billing.WebhookEvent{
		UserID:         userID,
		PreviousTier:   previousTier,
		NewTier:        ent.Tier,
		Provider:       providerName,
		EventType:      eventType,
		EventTimestamp: ent.UpdatedAt,
		ExpiresAt:      ent.ExpiresAt,
		Metadata:       map[string]interface{}{"event_type": eventType, "transfer_role": "to"},
	})
}

func (p *Provider) downgradeTransferredSource(
	ctx context.Context,
	userID string,
	eventTimestamp time.Time,
	eventType string,
) error {
	existing, err := p.manager.GetEntitlement(ctx, userID)
	if err != nil && err != goquota.ErrEntitlementNotFound {
		return err
	}
	if skipStaleTransfer(existing, eventTimestamp) {
		return nil
	}

	previousTier := p.defaultTier
	timezone := ""
	start := time.Time{}
	if existing != nil {
		previousTier = existing.Tier
		timezone = existing.Timezone
		start = existing.SubscriptionStartDate
	}

	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  p.defaultTier,
		SubscriptionStartDate: start,
		Timezone:              timezone,
		UpdatedAt:             transferUpdatedAt(eventTimestamp, existing),
	}

	if err := p.manager.SetEntitlement(ctx, ent); err != nil {
		return err
	}

	return p.invokeWebhookCallback(ctx, billing.WebhookEvent{
		UserID:         userID,
		PreviousTier:   previousTier,
		NewTier:        ent.Tier,
		Provider:       providerName,
		EventType:      eventType,
		EventTimestamp: ent.UpdatedAt,
		Metadata:       map[string]interface{}{"event_type": eventType, "transfer_role": "from"},
	})
}

func skipStaleTransfer(existing *goquota.Entitlement, eventTimestamp time.Time) bool {
	return existing != nil && !eventTimestamp.IsZero() && !eventTimestamp.After(existing.UpdatedAt)
}

func transferUpdatedAt(eventTimestamp time.Time, existing *goquota.Entitlement) time.Time {
	if !eventTimestamp.IsZero() {
		return eventTimestamp
	}
	if existing != nil && !existing.UpdatedAt.IsZero() {
		return existing.UpdatedAt
	}
	return time.Now().UTC()
}
