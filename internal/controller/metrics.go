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

	crInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "cr_info",
			Help:      "Info metric (always 1) carrying CR labels for PromQL joins.",
		},
		[]string{"name", "namespace", "git_repo", "git_ref", "auth_type", "polling_interval"},
	)

	crPaused = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "cr_paused",
			Help:      "Whether the GatewaySync CR is paused (1=paused, 0=active).",
		},
		[]string{"name", "namespace"},
	)

	conditionStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "condition_status",
			Help:      "Status of each condition type on the GatewaySync CR (1=True, 0=False).",
		},
		[]string{"name", "namespace", "type"},
	)

	gatewaySyncStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "gateway_sync_status",
			Help:      "Sync status of each gateway (0=Pending, 1=Synced, 2=Error, 3=MissingSidecar).",
		},
		[]string{"name", "namespace", "gateway"},
	)

	gatewayLastSyncTS = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "gateway_last_sync_timestamp_seconds",
			Help:      "Unix timestamp of each gateway's last sync.",
		},
		[]string{"name", "namespace", "gateway"},
	)

	gatewaysMissingSidecar = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stoker",
			Subsystem: "controller",
			Name:      "gateways_missing_sidecar",
			Help:      "Number of gateways missing the stoker-agent sidecar.",
		},
		[]string{"name", "namespace"},
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
		crInfo,
		crPaused,
		conditionStatus,
		gatewaySyncStatus,
		gatewayLastSyncTS,
		gatewaysMissingSidecar,
	)
}

// observeGatewayMetrics updates the gauge metrics after gateway discovery and condition updates.
func observeGatewayMetrics(gs *stokerv1alpha1.GatewaySync) {
	name, ns := gs.Name, gs.Namespace

	gatewaysDiscovered.WithLabelValues(name, ns).Set(float64(len(gs.Status.DiscoveredGateways)))

	syncedCount := 0
	missingSidecarCount := 0
	for _, gw := range gs.Status.DiscoveredGateways {
		if gw.SyncStatus == stokertypes.SyncStatusSynced {
			syncedCount++
		}
		if gw.SyncStatus == stokertypes.SyncStatusMissingSidecar {
			missingSidecarCount++
		}

		// Per-gateway sync status
		gatewaySyncStatus.WithLabelValues(name, ns, gw.Name).Set(syncStatusToFloat(gw.SyncStatus))

		// Per-gateway last sync timestamp
		if gw.LastSyncTime != nil {
			gatewayLastSyncTS.WithLabelValues(name, ns, gw.Name).Set(float64(gw.LastSyncTime.Unix()))
		}
	}
	gatewaysSynced.WithLabelValues(name, ns).Set(float64(syncedCount))
	gatewaysMissingSidecar.WithLabelValues(name, ns).Set(float64(missingSidecarCount))

	// Condition status for all condition types
	readyVal := 0.0
	for _, c := range gs.Status.Conditions {
		val := 0.0
		if c.Status == metav1.ConditionTrue {
			val = 1.0
		}
		conditionStatus.WithLabelValues(name, ns, c.Type).Set(val)

		if c.Type == conditions.TypeReady && c.Status == metav1.ConditionTrue {
			readyVal = 1.0
		}
	}
	crReady.WithLabelValues(name, ns).Set(readyVal)

	// CR info gauge — clear stale label combos then set with current values.
	crInfo.DeletePartialMatch(prometheus.Labels{"name": name, "namespace": ns})
	pollingInterval := gs.Spec.Polling.Interval
	if pollingInterval == "" {
		pollingInterval = "60s"
	}
	crInfo.WithLabelValues(name, ns, gs.Spec.Git.Repo, gs.Spec.Git.Ref, resolveAuthType(gs.Spec.Git.Auth), pollingInterval).Set(1)
}

// syncStatusToFloat maps gateway sync status strings to numeric gauge values.
func syncStatusToFloat(status string) float64 {
	switch status {
	case stokertypes.SyncStatusPending:
		return 0
	case stokertypes.SyncStatusSynced:
		return 1
	case stokertypes.SyncStatusError:
		return 2
	case stokertypes.SyncStatusMissingSidecar:
		return 3
	default:
		return 0
	}
}

// cleanupCRMetrics removes all metric series associated with a CR being deleted.
func cleanupCRMetrics(name, namespace string) {
	labels := prometheus.Labels{"name": name, "namespace": namespace}
	reconcileDuration.DeletePartialMatch(labels)
	reconcileTotal.DeletePartialMatch(labels)
	refResolveDuration.DeletePartialMatch(labels)
	gatewaysDiscovered.DeletePartialMatch(labels)
	gatewaysSynced.DeletePartialMatch(labels)
	crReady.DeletePartialMatch(labels)
	crInfo.DeletePartialMatch(labels)
	crPaused.DeletePartialMatch(labels)
	conditionStatus.DeletePartialMatch(labels)
	gatewaySyncStatus.DeletePartialMatch(labels)
	gatewayLastSyncTS.DeletePartialMatch(labels)
	gatewaysMissingSidecar.DeletePartialMatch(labels)
}
