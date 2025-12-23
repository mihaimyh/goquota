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

// DefaultMetrics returns a Metrics implementation using the default Prometheus registerer.
func DefaultMetrics(namespace string) billing.Metrics {
	return NewMetrics(prometheus.DefaultRegisterer, namespace)
}
