package controller

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestReconcileMetricsIncrement(t *testing.T) {
	before := testutil.ToFloat64(reconcileTotal.WithLabelValues("test-cr", "test-ns", "success"))

	reconcileTotal.WithLabelValues("test-cr", "test-ns", "success").Inc()

	after := testutil.ToFloat64(reconcileTotal.WithLabelValues("test-cr", "test-ns", "success"))
	if after != before+1 {
		t.Errorf("expected reconcile_total to increment by 1, got %f -> %f", before, after)
	}
}

func TestGatewayGaugesSet(t *testing.T) {
	gatewaysDiscovered.WithLabelValues("test-cr", "test-ns").Set(3)
	gatewaysSynced.WithLabelValues("test-cr", "test-ns").Set(2)
	crReady.WithLabelValues("test-cr", "test-ns").Set(1)

	if v := testutil.ToFloat64(gatewaysDiscovered.WithLabelValues("test-cr", "test-ns")); v != 3 {
		t.Errorf("expected gateways_discovered=3, got %f", v)
	}
	if v := testutil.ToFloat64(gatewaysSynced.WithLabelValues("test-cr", "test-ns")); v != 2 {
		t.Errorf("expected gateways_synced=2, got %f", v)
	}
	if v := testutil.ToFloat64(crReady.WithLabelValues("test-cr", "test-ns")); v != 1 {
		t.Errorf("expected cr_ready=1, got %f", v)
	}
}

func TestRefResolveDurationObserve(t *testing.T) {
	refResolveDuration.WithLabelValues("test-cr", "test-ns").Observe(0.5)

	count := testutil.CollectAndCount(refResolveDuration)
	if count <= 0 {
		t.Errorf("expected ref_resolve_duration to have series after observation, got %d", count)
	}
}

func TestGitHubAppTokenExpiryGauge(t *testing.T) {
	githubAppTokenExpiry.WithLabelValues("12345", "67890").Set(1700000000)

	v := testutil.ToFloat64(githubAppTokenExpiry.WithLabelValues("12345", "67890"))
	if v != 1700000000 {
		t.Errorf("expected token_expiry=1700000000, got %f", v)
	}
}
