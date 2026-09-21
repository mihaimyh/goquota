package prommetrics

import (
	"fmt"
	"testing"
	"time"
)

// TestDefaultMetricsIdempotent is a regression test for OBS-2/BILLING-5:
// constructing the default metrics twice for the same namespace must not panic
// and must return the same instance.
func TestDefaultMetricsIdempotent(t *testing.T) {
	namespace := fmt.Sprintf("billing_idem_%d", time.Now().UnixNano())

	first := DefaultMetrics(namespace)
	second := DefaultMetrics(namespace)

	if first == nil || second == nil {
		t.Fatal("DefaultMetrics returned nil")
	}
	if first != second {
		t.Fatal("DefaultMetrics returned different instances for the same namespace")
	}
}
