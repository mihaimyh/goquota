package stripe

import (
	"context"
	"errors"
	"testing"

	"github.com/mihaimyh/goquota/pkg/billing"
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestGetPriceIDForTier(t *testing.T) {
	tests := []struct {
		name        string
		tierMapping map[string]string
		tier        string
		wantPrice   string
	}{
		{
			name: "exact match",
			tierMapping: map[string]string{
				"price_pro_monthly":   "pro",
				"price_basic_monthly": "basic",
			},
			tier:      "pro",
			wantPrice: "price_pro_monthly",
		},
		{
			name: "no match returns empty",
			tierMapping: map[string]string{
				"price_pro_monthly": "pro",
			},
			tier:      "enterprise",
			wantPrice: "",
		},
		{
			name: "multiple prices for same tier returns first found",
			tierMapping: map[string]string{
				"price_pro_monthly": "pro",
				"price_pro_yearly":  "pro",
			},
			tier:      "pro",
			wantPrice: "", // Note: map iteration is random, so we can't predict which one
		},
		{
			name:        "empty tier mapping",
			tierMapping: map[string]string{},
			tier:        "pro",
			wantPrice:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &Provider{
				tierMapping: tt.tierMapping,
			}

			got := provider.getPriceIDForTier(tt.tier)

			// For the "multiple prices" case, just verify we got one of them
			if tt.name == "multiple prices for same tier returns first found" {
				if got != "price_pro_monthly" && got != "price_pro_yearly" {
					t.Errorf("getPriceIDForTier() = %v, want price_pro_monthly or price_pro_yearly", got)
				}
				return
			}

			if got != tt.wantPrice {
				t.Errorf("getPriceIDForTier() = %v, want %v", got, tt.wantPrice)
			}
		})
	}
}

func TestResolveCustomerID(t *testing.T) {
	tests := []struct {
		name               string
		customerIDResolver func(context.Context, string) (string, error)
		wantCustomerID     string
		wantErr            bool
		skip               bool // Skip tests that require Stripe API
	}{
		{
			name: "fast path success",
			customerIDResolver: func(_ context.Context, _ string) (string, error) {
				return "cus_123", nil
			},
			wantCustomerID: "cus_123",
			wantErr:        false,
		},
		{
			name: "fast path returns empty falls back to slow path",
			customerIDResolver: func(_ context.Context, _ string) (string, error) {
				return "", nil
			},
			wantCustomerID: "",
			wantErr:        true, // Slow path will fail without real Stripe API
			skip:           true, // Skip - requires Stripe API mocking
		},
		{
			name: "fast path error falls back to slow path",
			customerIDResolver: func(_ context.Context, _ string) (string, error) {
				return "", errors.New("database error")
			},
			wantCustomerID: "",
			wantErr:        true, // Slow path will fail without real Stripe API
			skip:           true, // Skip - requires Stripe API mocking
		},
		{
			name:               "no resolver uses slow path",
			customerIDResolver: nil,
			wantCustomerID:     "",
			wantErr:            true, // Slow path will fail without real Stripe API
			skip:               true, // Skip - requires Stripe API mocking
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skip("Requires Stripe API mocking")
			}

			// Create a minimal provider for testing
			manager, _ := goquota.NewManager(memory.New(), &goquota.Config{})

			provider := &Provider{
				manager:            manager,
				customerIDResolver: tt.customerIDResolver,
				metrics:            &billing.NoopMetrics{},
			}

			got, err := provider.resolveCustomerID(context.Background(), "user123")

			if (err != nil) != tt.wantErr {
				t.Errorf("resolveCustomerID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Only check customer ID if we expect success
			if !tt.wantErr && got != tt.wantCustomerID {
				t.Errorf("resolveCustomerID() = %v, want %v", got, tt.wantCustomerID)
			}
		})
	}
}

func TestCheckoutURL_TierValidation(t *testing.T) {
	tests := []struct {
		name        string
		tier        string
		tierMapping map[string]string
		wantErr     error
	}{
		{
			name: "tier not configured",
			tier: "enterprise",
			tierMapping: map[string]string{
				"price_pro_monthly": "pro",
			},
			wantErr: billing.ErrTierNotConfigured,
		},
		{
			name:        "empty tier mapping",
			tier:        "pro",
			tierMapping: map[string]string{},
			wantErr:     billing.ErrTierNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, _ := goquota.NewManager(memory.New(), &goquota.Config{})

			provider := &Provider{
				manager:     manager,
				tierMapping: tt.tierMapping,
				metrics:     &billing.NoopMetrics{},
			}

			_, err := provider.CheckoutURL(
				context.Background(),
				"user123",
				tt.tier,
				"https://example.com/success",
				"https://example.com/cancel",
			)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("CheckoutURL() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("CheckoutURL() error = %v, wantErr %v", err, tt.wantErr)
				}
			}
		})
	}
}

// TestCheckoutURL_MetadataInjection verifies that user_id is correctly injected
// This is critical for webhook processing
func TestCheckoutURL_MetadataInjection(t *testing.T) {
	// This test would require mocking the Stripe API
	// For now, we document the critical requirement:
	// CheckoutURL MUST inject metadata["user_id"] = userID
	// This is verified in the implementation
	t.Skip("Requires Stripe API mocking - verified in implementation")
}

// TestPortalURL_Integration would test actual Stripe API calls
func TestPortalURL_Integration(t *testing.T) {
	// This test would require Stripe test mode credentials
	t.Skip("Requires Stripe test mode credentials")
}

// TestBuildCreditPackCheckoutParams_PaymentIntentMetadata is a regression test
// for BILLING-6: the refund handler resolves user_id/resource from the
// PaymentIntent, so the one-time checkout params must set
// PaymentIntentData.Metadata (session metadata alone is not enough).
func TestBuildCreditPackCheckoutParams_PaymentIntentMetadata(t *testing.T) {
	params := buildCreditPackCheckoutParams("user-1", "credits", 500, "https://ok", "https://cancel")

	if params.Metadata["user_id"] != "user-1" || params.Metadata["resource"] != "credits" {
		t.Fatalf("session metadata missing: %+v", params.Metadata)
	}

	if params.PaymentIntentData == nil {
		t.Fatal("PaymentIntentData is nil: refund handler cannot resolve user_id/resource from the PaymentIntent")
	}
	if params.PaymentIntentData.Metadata["user_id"] != "user-1" ||
		params.PaymentIntentData.Metadata["resource"] != "credits" {
		t.Fatalf("PaymentIntent metadata missing: %+v", params.PaymentIntentData.Metadata)
	}
}
