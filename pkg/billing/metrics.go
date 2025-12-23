package billing

import "time"

// Metrics defines the interface for tracking billing provider operations.
// All methods are optional - providers should gracefully handle nil metrics.
type Metrics interface {
	// RecordWebhookEvent records a webhook event received from the billing provider.
	// eventType: The type of event (e.g., "INITIAL_PURCHASE", "RENEWAL", "TEST")
	// status: "success" or "error"
	RecordWebhookEvent(provider, eventType, status string)

	// RecordWebhookProcessingDuration records how long it took to process a webhook.
	RecordWebhookProcessingDuration(provider, eventType string, duration time.Duration)

	// RecordWebhookError records a webhook processing error.
	// errorType: The type of error (e.g., "auth_failed", "invalid_payload", "processing_error")
	RecordWebhookError(provider, errorType string)

	// RecordUserSync records a user synchronization operation.
	// status: "success" or "error"
	RecordUserSync(provider, status string)

	// RecordUserSyncDuration records how long a user sync took.
	RecordUserSyncDuration(provider string, duration time.Duration)

	// RecordTierChange records when a user's tier changes.
	RecordTierChange(provider, fromTier, toTier string)

	// RecordAPICall records an API call to the billing provider.
	// endpoint: The API endpoint called (e.g., "/subscribers/{id}")
	// status: HTTP status code as string (e.g., "200", "404", "500")
	RecordAPICall(provider, endpoint, status string)

	// RecordAPICallDuration records how long an API call took.
	RecordAPICallDuration(provider, endpoint string, duration time.Duration)
}

// NoopMetrics is a no-op implementation of the Metrics interface.
type NoopMetrics struct{}

func (n *NoopMetrics) RecordWebhookEvent(_, _, _ string)                            {}
func (n *NoopMetrics) RecordWebhookProcessingDuration(_, _ string, _ time.Duration) {}
func (n *NoopMetrics) RecordWebhookError(_, _ string)                               {}
func (n *NoopMetrics) RecordUserSync(_, _ string)                                   {}
func (n *NoopMetrics) RecordUserSyncDuration(_ string, _ time.Duration)             {}
func (n *NoopMetrics) RecordTierChange(_, _, _ string)                              {}
func (n *NoopMetrics) RecordAPICall(_, _, _ string)                                 {}
func (n *NoopMetrics) RecordAPICallDuration(_, _ string, _ time.Duration)           {}
