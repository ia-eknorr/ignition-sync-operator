package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
	"github.com/ia-eknorr/stoker-operator/pkg/conditions"
	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

// Reconcile result label values.
const (
	resultSuccess = "success"
	resultError   = "error"
	resultRequeue = "requeue"
)

var (
	reconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of GatewaySync reconciliation in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"name", "namespace"},
	)

	reconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "reconcile_total",
			Help:      "Total number of GatewaySync reconciliations.",
		},
		[]string{"name", "namespace", "result"},
	)

	refResolveDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "ref_resolve_duration_seconds",
			Help:      "Duration of git ref resolution (ls-remote) in seconds.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"name", "namespace"},
	)

	gatewaysDiscovered = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "gateways_discovered",
			Help:      "Number of gateways discovered by the controller.",
		},
		[]string{"name", "namespace"},
	)

	gatewaysSynced = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "gateways_synced",
			Help:      "Number of gateways in Synced state.",
		},
		[]string{"name", "namespace"},
	)

	crReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "cr_ready",
			Help:      "Whether the GatewaySync CR is Ready (1=ready, 0=not ready).",
		},
		[]string{"name", "namespace"},
	)

	githubAppTokenExpiry = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "github_app_token_expiry_timestamp_seconds",
			Help:      "Unix timestamp when the cached GitHub App token expires.",
		},
		[]string{"app_id", "installation_id"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		reconcileDuration,
		reconcileTotal,
		refResolveDuration,
		gatewaysDiscovered,
		gatewaysSynced,
		crReady,
		githubAppTokenExpiry,
	)
}

// observeGatewayMetrics updates the gauge metrics after gateway discovery and condition updates.
func observeGatewayMetrics(gs *stokerv1alpha1.GatewaySync) {
	gatewaysDiscovered.WithLabelValues(gs.Name, gs.Namespace).Set(float64(len(gs.Status.DiscoveredGateways)))

	syncedCount := 0
	for _, gw := range gs.Status.DiscoveredGateways {
		if gw.SyncStatus == stokertypes.SyncStatusSynced {
			syncedCount++
		}
	}
	gatewaysSynced.WithLabelValues(gs.Name, gs.Namespace).Set(float64(syncedCount))

	readyVal := 0.0
	for _, c := range gs.Status.Conditions {
		if c.Type == conditions.TypeReady && c.Status == metav1.ConditionTrue {
			readyVal = 1.0
			break
		}
	}
	crReady.WithLabelValues(gs.Name, gs.Namespace).Set(readyVal)
}
