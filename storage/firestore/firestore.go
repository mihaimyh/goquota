// Package firestore provides a Firestore implementation of the goquota.Storage interface.
// This implementation uses Google Cloud Firestore for production-grade quota persistence.
package firestore

import (
	"context"
	"fmt"
	"math"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Storage implements goquota.Storage using Google Cloud Firestore
type Storage struct {
	client                 *firestore.Client
	entitlementsCollection string
	usageCollection        string
	refundsCollection      string
	consumptionsCollection string
}

// Config holds Firestore storage configuration
type Config struct {
	// EntitlementsCollection is the Firestore collection for user entitlements
	// Default: "billing_entitlements"
	EntitlementsCollection string

	// UsageCollection is the Firestore collection for usage tracking
	// Default: "billing_usage"
	UsageCollection string

	// RefundsCollection is the Firestore collection for audit logs
	// Default: "billing_refunds"
	RefundsCollection string

	// ConsumptionsCollection is the Firestore collection for consumption audit logs
	// Default: "billing_consumptions"
	ConsumptionsCollection string
}

// New creates a new Firestore storage adapter
func New(client *firestore.Client, config Config) (*Storage, error) {
	if client == nil {
		return nil, fmt.Errorf("firestore client is required")
	}

	// Set defaults
	if config.EntitlementsCollection == "" {
		config.EntitlementsCollection = "billing_entitlements"
	}
	if config.UsageCollection == "" {
		config.UsageCollection = "billing_usage"
	}
	if config.RefundsCollection == "" {
		config.RefundsCollection = "billing_refunds"
	}
	if config.ConsumptionsCollection == "" {
		config.ConsumptionsCollection = "billing_consumptions"
	}

	return &Storage{
		client:                 client,
		entitlementsCollection: config.EntitlementsCollection,
		usageCollection:        config.UsageCollection,
		refundsCollection:      config.RefundsCollection,
		consumptionsCollection: config.ConsumptionsCollection,
	}, nil
}

// GetEntitlement implements goquota.Storage
func (s *Storage) GetEntitlement(ctx context.Context, userID string) (*goquota.Entitlement, error) {
	doc := s.client.Collection(s.entitlementsCollection).Doc(userID)
	snap, err := doc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, goquota.ErrEntitlementNotFound
		}
		return nil, fmt.Errorf("failed to get entitlement: %w", err)
	}

	if !snap.Exists() {
		return nil, goquota.ErrEntitlementNotFound
	}

	data := snap.Data()
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  getString(data, "tier"),
		SubscriptionStartDate: getTime(data, "subscriptionStartDate"),
		UpdatedAt:             getTime(data, "updatedAt"),
	}

	if expiresAt, ok := data["expiresAt"].(time.Time); ok && !expiresAt.IsZero() {
		ent.ExpiresAt = &expiresAt
	}

	return ent, nil
}

// SetEntitlement implements goquota.Storage
func (s *Storage) SetEntitlement(ctx context.Context, ent *goquota.Entitlement) error {
	if ent == nil || ent.UserID == "" {
		return fmt.Errorf("invalid entitlement")
	}

	doc := s.client.Collection(s.entitlementsCollection).Doc(ent.UserID)

	data := map[string]interface{}{
		"tier":                  ent.Tier,
		"subscriptionStartDate": ent.SubscriptionStartDate,
		"updatedAt":             ent.UpdatedAt,
	}

	if ent.ExpiresAt != nil {
		data["expiresAt"] = *ent.ExpiresAt
	}

	_, err := doc.Set(ctx, data, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to set entitlement: %w", err)
	}

	return nil
}

// GetUsage implements goquota.Storage
func (s *Storage) GetUsage(ctx context.Context, userID, resource string,
	period goquota.Period) (*goquota.Usage, error) {
	doc := s.usageDoc(userID, resource, period)
	snap, err := doc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil // No usage yet is not an error
		}
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	if !snap.Exists() {
		return nil, nil
	}

	data := snap.Data()
	usage := &goquota.Usage{
		UserID:    userID,
		Resource:  resource,
		Used:      getInt(data, "used"),
		Limit:     getInt(data, "limit"),
		Period:    period,
		Tier:      getString(data, "tier"),
		UpdatedAt: getTime(data, "updatedAt"),
	}

	return usage, nil
}

// ConsumeQuota implements goquota.Storage with transaction-safe consumption
func (s *Storage) ConsumeQuota(ctx context.Context, req *goquota.ConsumeRequest) (int, error) {
	if req.Amount < 0 {
		return 0, goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return 0, nil // No-op
	}

	doc := s.usageDoc(req.UserID, req.Resource, req.Period)
	var newUsed int

	err := s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		// 1. Check idempotency if key provided
		if req.IdempotencyKey != "" {
			consumptionDocRef := s.client.Collection(s.consumptionsCollection).Doc(req.IdempotencyKey)
			snap, err := tx.Get(consumptionDocRef)
			if err != nil && status.Code(err) != codes.NotFound {
				return err
			}
			if snap.Exists() {
				// Idempotency hit - return cached result
				data := snap.Data()
				newUsed = getInt(data, "newUsed")
				return nil
			}
		}

		// 2. Get current usage
		snap, err := tx.Get(doc)

		currentUsed := 0
		currentLimit := req.Limit

		if err == nil && snap.Exists() {
			data := snap.Data()
			currentUsed = getInt(data, "used")
			storedLimit := getInt(data, "limit")
			if storedLimit > 0 {
				currentLimit = storedLimit
			}
		}

		newUsed = currentUsed + req.Amount
		if newUsed > currentLimit {
			return goquota.ErrQuotaExceeded
		}

		// 3. Update usage
		now := time.Now().UTC()
		err = tx.Set(doc, map[string]interface{}{
			"used":       newUsed,
			"limit":      currentLimit,
			"cycleStart": req.Period.Start,
			"cycleEnd":   req.Period.End,
			"tier":       req.Tier,
			"resource":   req.Resource,
			"updatedAt":  now,
		}, firestore.MergeAll)
		if err != nil {
			return err
		}

		// 4. Create consumption audit record (if idempotency key provided)
		if req.IdempotencyKey != "" {
			consumptionDocRef := s.client.Collection(s.consumptionsCollection).Doc(req.IdempotencyKey)
			err = tx.Create(consumptionDocRef, map[string]interface{}{
				"consumptionId":  req.IdempotencyKey,
				"userId":         req.UserID,
				"resource":       req.Resource,
				"amount":         req.Amount,
				"periodStart":    req.Period.Start,
				"periodEnd":      req.Period.End,
				"periodType":     string(req.Period.Type),
				"timestamp":      now,
				"idempotencyKey": req.IdempotencyKey,
				"newUsed":        newUsed,
			})
			if err != nil {
				return err
			}
		}

		return nil
	})

	return newUsed, err
}

// ApplyTierChange implements goquota.Storage with prorated quota adjustment
func (s *Storage) ApplyTierChange(ctx context.Context, req *goquota.TierChangeRequest) error {
	// Calculate prorated limit (already done by Manager, just store it)
	doc := s.usageDoc(req.UserID, "audio_seconds", req.Period) // Assuming audio_seconds resource

	return s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(doc)

		currentUsed := req.CurrentUsed
		if err == nil && snap.Exists() {
			data := snap.Data()
			currentUsed = getInt(data, "used")
		}

		now := time.Now().UTC()
		return tx.Set(doc, map[string]interface{}{
			"limit":         req.NewLimit,
			"used":          currentUsed,
			"cycleStart":    req.Period.Start,
			"cycleEnd":      req.Period.End,
			"tier":          req.NewTier,
			"previousTier":  req.OldTier,
			"tierChangedAt": now,
			"resource":      "audio_seconds",
			"updatedAt":     now,
		}, firestore.MergeAll)
	})
}

// SetUsage implements goquota.Storage
func (s *Storage) SetUsage(ctx context.Context, userID, resource string,
	usage *goquota.Usage, period goquota.Period) error {
	if usage == nil {
		return fmt.Errorf("usage is required")
	}

	doc := s.usageDoc(userID, resource, period)
	data := map[string]interface{}{
		"used":       usage.Used,
		"limit":      usage.Limit,
		"cycleStart": period.Start,
		"cycleEnd":   period.End,
		"tier":       usage.Tier,
		"resource":   resource,
		"updatedAt":  usage.UpdatedAt,
	}

	_, err := doc.Set(ctx, data, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to set usage: %w", err)
	}

	return nil
}

// RefundQuota implements goquota.Storage
func (s *Storage) RefundQuota(ctx context.Context, req *goquota.RefundRequest) error {
	if req.Amount < 0 {
		return goquota.ErrInvalidAmount
	}
	if req.Amount == 0 {
		return nil // No-op
	}

	// Transaction to ensure atomicity of usage update and audit log creation
	err := s.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		// 1. Check idempotency if key provided, and get whether it already exists
		alreadyExists, err := s.checkRefundIdempotency(tx, req.IdempotencyKey)
		if err != nil {
			return err
		}
		if alreadyExists {
			// Idempotent - refund already processed, return success
			return nil
		}

		// 2. Calculate period
		period := s.calculateRefundPeriod(req)
		if period.Type == "" {
			return goquota.ErrInvalidPeriod
		}

		// 3. Get current usage and update
		if err := s.updateRefundUsage(tx, req, period); err != nil {
			return err
		}

		// 4. Create refund audit record (we know it doesn't exist from step 1)
		return s.createRefundRecord(tx, req, period)
	})

	return err
}

// checkRefundIdempotency checks if a refund with the given idempotency key already exists.
// Returns (true, nil) if the refund already exists (idempotent),
// (false, nil) if it doesn't exist, or (false, error) on error.
func (s *Storage) checkRefundIdempotency(tx *firestore.Transaction, idempotencyKey string) (bool, error) {
	if idempotencyKey == "" {
		return false, nil
	}

	refundDocRef := s.client.Collection(s.refundsCollection).Doc(idempotencyKey)
	snap, err := tx.Get(refundDocRef)
	if err != nil && status.Code(err) != codes.NotFound {
		return false, err
	}
	if snap.Exists() {
		// Idempotency hit - refund already exists
		return true, nil
	}
	return false, nil
}

// calculateRefundPeriod calculates the period for the refund request
func (s *Storage) calculateRefundPeriod(req *goquota.RefundRequest) goquota.Period {
	if !req.Period.Start.IsZero() {
		return req.Period
	}

	now := time.Now().UTC()
	switch req.PeriodType {
	case goquota.PeriodTypeMonthly:
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)
		return goquota.Period{Start: start, End: end, Type: goquota.PeriodTypeMonthly}
	case goquota.PeriodTypeDaily:
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		end := start.Add(24 * time.Hour)
		return goquota.Period{Start: start, End: end, Type: goquota.PeriodTypeDaily}
	default:
		// Return empty period - will be handled by caller
		return goquota.Period{}
	}
}

// updateRefundUsage updates the usage document with the refund amount
func (s *Storage) updateRefundUsage(
	tx *firestore.Transaction,
	req *goquota.RefundRequest,
	period goquota.Period,
) error {
	usageDoc := s.usageDoc(req.UserID, req.Resource, period)
	snap, err := tx.Get(usageDoc)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			// No usage to refund, just return nil
			return nil
		}
		return err
	}

	currentUsed := 0
	if snap.Exists() {
		currentUsed = getInt(snap.Data(), "used")
	}

	// Calculate new used amount (clamp to 0)
	newUsed := currentUsed - req.Amount
	if newUsed < 0 {
		newUsed = 0
	}

	now := time.Now().UTC()
	return tx.Set(usageDoc, map[string]interface{}{
		"used":      newUsed,
		"updatedAt": now,
	}, firestore.MergeAll)
}

// createRefundRecord creates an audit record for the refund
// Note: This should only be called after checkRefundIdempotency confirms the record doesn't exist
func (s *Storage) createRefundRecord(
	tx *firestore.Transaction,
	req *goquota.RefundRequest,
	period goquota.Period,
) error {
	if req.IdempotencyKey == "" {
		return nil
	}

	// Create the refund record using Set with merge to handle race conditions
	// If two transactions run concurrently, both will try to create, but Set will work
	// The idempotency check in checkRefundIdempotency prevents duplicate processing
	now := time.Now().UTC()
	refundDocRef := s.client.Collection(s.refundsCollection).Doc(req.IdempotencyKey)
	return tx.Set(refundDocRef, map[string]interface{}{
		"refundId":       req.IdempotencyKey,
		"userId":         req.UserID,
		"resource":       req.Resource,
		"amount":         req.Amount,
		"periodStart":    period.Start,
		"periodEnd":      period.End,
		"periodType":     string(req.PeriodType),
		"timestamp":      now,
		"idempotencyKey": req.IdempotencyKey,
		"reason":         req.Reason,
		"metadata":       req.Metadata,
	}, firestore.MergeAll)
}

// GetRefundRecord implements goquota.Storage
func (s *Storage) GetRefundRecord(ctx context.Context, idempotencyKey string) (*goquota.RefundRecord, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	doc := s.client.Collection(s.refundsCollection).Doc(idempotencyKey)
	snap, err := doc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil // Not found is not an error
		}
		return nil, fmt.Errorf("failed to get refund record: %w", err)
	}

	data := snap.Data()

	const dailyPeriodType = "daily"
	periodTypeStr := getString(data, "periodType")
	var periodType goquota.PeriodType
	if periodTypeStr == dailyPeriodType {
		periodType = goquota.PeriodTypeDaily
	} else {
		periodType = goquota.PeriodTypeMonthly
	}

	record := &goquota.RefundRecord{
		RefundID: getString(data, "refundId"),
		UserID:   getString(data, "userId"),
		Resource: getString(data, "resource"),
		Amount:   getInt(data, "amount"),
		Period: goquota.Period{
			Start: getTime(data, "periodStart"),
			End:   getTime(data, "periodEnd"),
			Type:  periodType,
		},
		Timestamp:      getTime(data, "timestamp"),
		IdempotencyKey: getString(data, "idempotencyKey"),
		Reason:         getString(data, "reason"),
	}

	// Manual metadata extraction
	if m, ok := data["metadata"].(map[string]interface{}); ok {
		metadata := make(map[string]string)
		for k, v := range m {
			if sVal, ok := v.(string); ok {
				metadata[k] = sVal
			}
		}
		record.Metadata = metadata
	}

	return record, nil
}

// GetConsumptionRecord implements goquota.Storage
func (s *Storage) GetConsumptionRecord(ctx context.Context, idempotencyKey string) (*goquota.ConsumptionRecord, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	doc := s.client.Collection(s.consumptionsCollection).Doc(idempotencyKey)
	snap, err := doc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil // Not found is not an error
		}
		return nil, fmt.Errorf("failed to get consumption record: %w", err)
	}

	data := snap.Data()

	const dailyPeriodType = "daily"
	periodTypeStr := getString(data, "periodType")
	var periodType goquota.PeriodType
	if periodTypeStr == dailyPeriodType {
		periodType = goquota.PeriodTypeDaily
	} else {
		periodType = goquota.PeriodTypeMonthly
	}

	record := &goquota.ConsumptionRecord{
		ConsumptionID: getString(data, "consumptionId"),
		UserID:        getString(data, "userId"),
		Resource:      getString(data, "resource"),
		Amount:        getInt(data, "amount"),
		Period: goquota.Period{
			Start: getTime(data, "periodStart"),
			End:   getTime(data, "periodEnd"),
			Type:  periodType,
		},
		Timestamp:      getTime(data, "timestamp"),
		IdempotencyKey: getString(data, "idempotencyKey"),
		NewUsed:        getInt(data, "newUsed"),
	}

	// Manual metadata extraction
	if m, ok := data["metadata"].(map[string]interface{}); ok {
		metadata := make(map[string]string)
		for k, v := range m {
			if sVal, ok := v.(string); ok {
				metadata[k] = sVal
			}
		}
		record.Metadata = metadata
	}

	return record, nil
}

// usageDoc returns the Firestore document reference for usage tracking
func (s *Storage) usageDoc(userID, resource string, period goquota.Period) *firestore.DocumentRef {
	// Structure: billing_usage/{userID}/periods/{periodKey}_{resource}
	periodKey := period.Key()
	docID := fmt.Sprintf("%s_%s", periodKey, resource)

	return s.client.Collection(s.usageCollection).
		Doc(userID).
		Collection("periods").
		Doc(docID)
}

// Helper functions for type conversion from Firestore data

func getString(data map[string]interface{}, key string) string {
	if v, ok := data[key].(string); ok {
		return v
	}
	return ""
}

func getInt(data map[string]interface{}, key string) int {
	switch v := data[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(math.Round(v))
	default:
		return 0
	}
}

func getTime(data map[string]interface{}, key string) time.Time {
	if v, ok := data[key].(time.Time); ok {
		return v
	}
	return time.Time{}
}
