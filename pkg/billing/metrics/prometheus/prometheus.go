package prommetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/mihaimyh/goquota/pkg/billing"
)

// Metrics implements billing.Metrics using Prometheus.
type Metrics struct {
	webhookEventsTotal        *prometheus.CounterVec
	webhookProcessingDuration *prometheus.HistogramVec
	webhookErrorsTotal        *prometheus.CounterVec
	userSyncTotal             *prometheus.CounterVec
	userSyncDuration          *prometheus.HistogramVec
	tierChangesTotal          *prometheus.CounterVec
	apiCallsTotal             *prometheus.CounterVec
	apiCallDuration           *prometheus.HistogramVec

	// Checkout and portal metrics
	checkoutSessionsCreatedTotal   *prometheus.CounterVec
	checkoutSessionsCompletedTotal *prometheus.CounterVec
	portalSessionsCreatedTotal     *prometheus.CounterVec
	creditPackPurchasesTotal       *prometheus.CounterVec
	revenueEstimate                *prometheus.CounterVec
}

// NewMetrics creates a new Prometheus metrics implementation for billing providers.
func NewMetrics(reg prometheus.Registerer, namespace string) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		webhookEventsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "webhook_events_total",
			Help:      "Total number of webhook events received from billing providers.",
		}, []string{"provider", "event_type", "status"}),

		webhookProcessingDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "webhook_processing_duration_seconds",
			Help:      "Duration of webhook processing in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"provider", "event_type"}),

		webhookErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "webhook_errors_total",
			Help:      "Total number of webhook processing errors.",
		}, []string{"provider", "error_type"}),

		userSyncTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "user_sync_total",
			Help:      "Total number of user synchronization operations.",
		}, []string{"provider", "status"}),

		userSyncDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "user_sync_duration_seconds",
			Help:      "Duration of user synchronization operations in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"provider"}),

		tierChangesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "tier_changes_total",
			Help:      "Total number of tier changes.",
		}, []string{"provider", "from_tier", "to_tier"}),

		apiCallsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "api_calls_total",
			Help:      "Total number of API calls to billing providers.",
		}, []string{"provider", "endpoint", "status"}),

		apiCallDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "api_call_duration_seconds",
			Help:      "Duration of API calls to billing providers in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"provider", "endpoint"}),

		// Checkout and portal metrics
		checkoutSessionsCreatedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "checkout_sessions_created_total",
			Help:      "Total number of checkout sessions created.",
		}, []string{"provider", "tier", "type"}),

		checkoutSessionsCompletedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "checkout_sessions_completed_total",
			Help:      "Total number of checkout sessions completed.",
		}, []string{"provider", "tier", "type"}),

		portalSessionsCreatedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "portal_sessions_created_total",
			Help:      "Total number of portal sessions created.",
		}, []string{"provider"}),

		creditPackPurchasesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "credit_pack_purchases_total",
			Help:      "Total number of credit pack purchases.",
		}, []string{"provider", "resource", "amount_range"}),

		revenueEstimate: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "billing",
			Name:      "revenue_estimate_total",
			Help:      "Estimated revenue (approximate).",
		}, []string{"provider", "tier", "type"}),
	}
}

func (m *Metrics) RecordWebhookEvent(provider, eventType, status string) {
	m.webhookEventsTotal.WithLabelValues(provider, eventType, status).Inc()
}

func (m *Metrics) RecordWebhookProcessingDuration(provider, eventType string, duration time.Duration) {
	m.webhookProcessingDuration.WithLabelValues(provider, eventType).Observe(duration.Seconds())
}

func (m *Metrics) RecordWebhookError(provider, errorType string) {
	m.webhookErrorsTotal.WithLabelValues(provider, errorType).Inc()
}

func (m *Metrics) RecordUserSync(provider, status string) {
	m.userSyncTotal.WithLabelValues(provider, status).Inc()
}

func (m *Metrics) RecordUserSyncDuration(provider string, duration time.Duration) {
	m.userSyncDuration.WithLabelValues(provider).Observe(duration.Seconds())
}

func (m *Metrics) RecordTierChange(provider, fromTier, toTier string) {
	m.tierChangesTotal.WithLabelValues(provider, fromTier, toTier).Inc()
}

func (m *Metrics) RecordAPICall(provider, endpoint, status string) {
	m.apiCallsTotal.WithLabelValues(provider, endpoint, status).Inc()
}

func (m *Metrics) RecordAPICallDuration(provider, endpoint string, duration time.Duration) {
	m.apiCallDuration.WithLabelValues(provider, endpoint).Observe(duration.Seconds())
}

// Checkout and portal metrics
func (m *Metrics) RecordCheckoutSessionCreated(provider, tier, sessionType string) {
	m.checkoutSessionsCreatedTotal.WithLabelValues(provider, tier, sessionType).Inc()
}

func (m *Metrics) RecordCheckoutSessionCompleted(provider, tier, sessionType string) {
	m.checkoutSessionsCompletedTotal.WithLabelValues(provider, tier, sessionType).Inc()
}

func (m *Metrics) RecordPortalSessionCreated(provider string) {
	m.portalSessionsCreatedTotal.WithLabelValues(provider).Inc()
}

func (m *Metrics) RecordCreditPackPurchase(provider, resource, amountRange string) {
	m.creditPackPurchasesTotal.WithLabelValues(provider, resource, amountRange).Inc()
}

func (m *Metrics) RecordRevenueEstimate(provider, tier, revenueType string, amount float64) {
	m.revenueEstimate.WithLabelValues(provider, tier, revenueType).Add(amount)
}

// DefaultMetrics returns a Metrics implementation using the default Prometheus registerer.
func DefaultMetrics(namespace string) billing.Metrics {
	return NewMetrics(prometheus.DefaultRegisterer, namespace)
}
