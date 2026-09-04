package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"

	"github.com/mihaimyh/goquota/pkg/goquota"
	firestoreStorage "github.com/mihaimyh/goquota/storage/firestore"
)

const (
	resourceReceiptScan = "receipt_scan"
	resourceReceiptChat = "receipt_chat"
)

type mergeAPI struct {
	manager *goquota.Manager
	backend string
}

func openMergeStorage(ctx context.Context, postgres goquota.Storage) (goquota.Storage, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("MERGE_STORAGE")))
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if backend == "" && host != "" {
		backend = "firestore"
	}
	if backend != "firestore" {
		return postgres, "postgres", nil
	}
	if host == "" {
		return nil, "", fmt.Errorf("MERGE_STORAGE=firestore requires FIRESTORE_EMULATOR_HOST")
	}
	projectID := os.Getenv("FIRESTORE_PROJECT_ID")
	if projectID == "" {
		projectID = "play-console-api-access-452212"
	}
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, "", fmt.Errorf("firestore emulator client: %w", err)
	}
	store, err := firestoreStorage.New(client, firestoreStorage.Config{
		EntitlementsCollection: "goquota_k6_entitlements",
		UsageCollection:        "goquota_k6_usage",
		RefundsCollection:      "goquota_k6_refunds",
		ConsumptionsCollection: "goquota_k6_consumptions",
		MergeRecordsCollection: "goquota_k6_merge_records",
		TimeQueryCollection:    "goquota_k6_temp",
	})
	if err != nil {
		_ = client.Close()
		return nil, "", err
	}
	return store, "firestore", nil
}

func newFlickAIMergeManager(_ context.Context, storage goquota.Storage) (*goquota.Manager, error) {
	cfg := goquota.Config{
		DefaultTier: "explorer",
		Tiers: map[string]goquota.TierConfig{
			"explorer": {
				Name: "explorer",
				DailyQuotas: map[string]int{
					resourceReceiptScan: 5,
					resourceReceiptChat: 5,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeDaily,
					goquota.PeriodTypeForever,
				},
			},
		},
		CacheConfig: &goquota.CacheConfig{Enabled: false},
	}
	return goquota.NewManager(storage, &cfg)
}

func registerMergeRoutes(r *gin.Engine, manager *goquota.Manager, backend string) {
	api := &mergeAPI{manager: manager, backend: backend}
	g := r.Group("/merge")
	g.GET("/backend", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"backend": backend})
	})
	g.POST("/ensure-user", api.ensureUser)
	g.POST("/consume", api.consume)
	g.POST("/topup", api.topup)
	g.POST("/drain", api.drain)
	g.POST("/merge", api.merge)
	g.GET("/quota", api.quota)
	g.GET("/snapshot", api.snapshot)
	g.GET("/entitlement", api.entitlement)
}

type ensureUserRequest struct {
	UserID string `json:"userId"`
	Tier   string `json:"tier"`
}

func (a *mergeAPI) ensureUser(c *gin.Context) {
	var req ensureUserRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId required"})
		return
	}
	tier := req.Tier
	if tier == "" {
		tier = "explorer"
	}
	existing, err := a.manager.GetEntitlement(c.Request.Context(), req.UserID)
	if err == nil && existing != nil {
		c.JSON(http.StatusOK, gin.H{"userId": existing.UserID, "tier": existing.Tier, "sealed": existing.IsSealed(time.Now().UTC())})
		return
	}
	if err != nil && !errors.Is(err, goquota.ErrEntitlementNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ent := &goquota.Entitlement{
		UserID: req.UserID,
		Tier:   tier,
	}
	if err := a.manager.SetEntitlement(c.Request.Context(), ent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"userId": req.UserID, "tier": tier, "sealed": false})
}

type consumeRequest struct {
	UserID   string `json:"userId"`
	Resource string `json:"resource"`
	Amount   int    `json:"amount"`
	Period   string `json:"period"`
}

func (a *mergeAPI) consume(c *gin.Context) {
	var req consumeRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == "" || req.Resource == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId, resource, amount required"})
		return
	}
	if req.Amount <= 0 {
		req.Amount = 1
	}
	period, err := parsePeriod(req.Period)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	newUsed, err := a.manager.Consume(c.Request.Context(), req.UserID, req.Resource, req.Amount, period)
	if err != nil {
		writeMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"userId": req.UserID, "resource": req.Resource, "newUsed": newUsed})
}

type topupRequest struct {
	UserID         string `json:"userId"`
	Resource       string `json:"resource"`
	Amount         int    `json:"amount"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func (a *mergeAPI) topup(c *gin.Context) {
	var req topupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == "" || req.Resource == "" || req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId, resource, amount required"})
		return
	}
	var opts []goquota.TopUpOption
	if req.IdempotencyKey != "" {
		opts = append(opts, goquota.WithTopUpIdempotencyKey(req.IdempotencyKey))
	}
	if err := a.manager.TopUpLimit(c.Request.Context(), req.UserID, req.Resource, req.Amount, opts...); err != nil {
		writeMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type drainRequest struct {
	UserID   string `json:"userId"`
	Resource string `json:"resource"`
	Period   string `json:"period"`
}

func (a *mergeAPI) drain(c *gin.Context) {
	var req drainRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == "" || req.Resource == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId, resource required"})
		return
	}
	period, err := parsePeriod(req.Period)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if period == goquota.PeriodTypeAuto {
		period = goquota.PeriodTypeForever
	}
	if err := a.manager.DrainRemaining(c.Request.Context(), req.UserID, req.Resource, period); err != nil {
		writeMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type mergeRequest struct {
	SourceUserID   string   `json:"sourceUserId"`
	TargetUserID   string   `json:"targetUserId"`
	Resources      []string `json:"resources"`
	Periods        []string `json:"periods"`
	IdempotencyKey string   `json:"idempotencyKey"`
	SealSource     bool     `json:"sealSource"`
}

func (a *mergeAPI) merge(c *gin.Context) {
	var req mergeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if len(req.Resources) == 0 {
		req.Resources = []string{resourceReceiptScan, resourceReceiptChat}
	}
	periods := make([]goquota.PeriodType, 0, 2)
	if len(req.Periods) == 0 {
		periods = []goquota.PeriodType{goquota.PeriodTypeDaily, goquota.PeriodTypeForever}
	} else {
		for _, p := range req.Periods {
			pt, err := parsePeriod(p)
			if err != nil || pt == goquota.PeriodTypeAuto {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period"})
				return
			}
			periods = append(periods, pt)
		}
	}
	result, err := a.manager.MergeUser(c.Request.Context(), &goquota.MergeUserRequest{
		SourceUserID:   req.SourceUserID,
		TargetUserID:   req.TargetUserID,
		Resources:      req.Resources,
		Periods:        periods,
		IdempotencyKey: req.IdempotencyKey,
		SealSource:     req.SealSource,
	})
	if err != nil {
		writeMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (a *mergeAPI) quota(c *gin.Context) {
	userID := c.Query("user")
	resource := c.Query("resource")
	periodStr := c.DefaultQuery("period", "daily")
	if userID == "" || resource == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user and resource required"})
		return
	}
	period, err := parsePeriod(periodStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	usage, err := a.manager.GetQuota(c.Request.Context(), userID, resource, period)
	if err != nil {
		writeMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, usageOrZero(userID, resource, usage))
}

func (a *mergeAPI) snapshot(c *gin.Context) {
	source := c.Query("source")
	target := c.Query("target")
	if source == "" || target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and target required"})
		return
	}
	resources := []string{resourceReceiptScan, resourceReceiptChat}
	if raw := c.Query("resources"); raw != "" {
		resources = splitCSV(raw)
	}
	ctx := c.Request.Context()
	out := gin.H{
		"source": a.userSnapshot(ctx, source, resources),
		"target": a.userSnapshot(ctx, target, resources),
	}
	if ent, err := a.manager.GetEntitlement(ctx, source); err == nil {
		out["sourceSealed"] = ent.IsSealed(time.Now().UTC())
		out["sourceMigratedTo"] = ent.MigratedTo
	} else {
		out["sourceSealed"] = false
	}
	c.JSON(http.StatusOK, out)
}

func (a *mergeAPI) entitlement(c *gin.Context) {
	userID := c.Query("user")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user required"})
		return
	}
	ent, err := a.manager.GetEntitlement(c.Request.Context(), userID)
	if err != nil {
		writeMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"userId":     ent.UserID,
		"tier":       ent.Tier,
		"sealed":     ent.IsSealed(time.Now().UTC()),
		"migratedTo": ent.MigratedTo,
		"expireAt":   ent.ExpireAt,
	})
}

func (a *mergeAPI) userSnapshot(ctx context.Context, userID string, resources []string) gin.H {
	out := gin.H{"userId": userID}
	for _, resource := range resources {
		daily, _ := a.manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
		forever, _ := a.manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeForever)
		out[resource] = gin.H{
			"daily":   usageOrZero(userID, resource, daily),
			"forever": usageOrZero(userID, resource, forever),
		}
	}
	return out
}

func usageOrZero(userID, resource string, usage *goquota.Usage) gin.H {
	if usage == nil {
		return gin.H{"userId": userID, "resource": resource, "used": 0, "limit": 0}
	}
	return gin.H{"userId": usage.UserID, "resource": usage.Resource, "used": usage.Used, "limit": usage.Limit}
}

func parsePeriod(raw string) (goquota.PeriodType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "daily":
		return goquota.PeriodTypeDaily, nil
	case "monthly":
		return goquota.PeriodTypeMonthly, nil
	case "forever":
		return goquota.PeriodTypeForever, nil
	case "auto":
		return goquota.PeriodTypeAuto, nil
	default:
		return "", goquota.ErrInvalidPeriod
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeMergeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, goquota.ErrUserSealed):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "user_sealed"})
	case errors.Is(err, goquota.ErrQuotaExceeded):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": err.Error(), "code": "quota_exceeded"})
	case errors.Is(err, goquota.ErrUnsupportedOperation):
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error(), "code": "unsupported"})
	case errors.Is(err, goquota.ErrSameUser), errors.Is(err, goquota.ErrInvalidMergeRequest), errors.Is(err, goquota.ErrInvalidPeriod), errors.Is(err, goquota.ErrInvalidAmount):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, goquota.ErrEntitlementNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
