package agent

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// AgentMetrics holds Prometheus metrics for the agent sidecar.
// It uses a standalone registry (agent is not a controller-runtime manager).
type AgentMetrics struct {
	registry *prometheus.Registry

	SyncDuration      *prometheus.HistogramVec
	SyncTotal         *prometheus.CounterVec
	FilesChanged      *prometheus.GaugeVec
	GitFetchDuration  *prometheus.HistogramVec
	GitFetchTotal     *prometheus.CounterVec
	ScanDuration      prometheus.Histogram
	ScanTotal         *prometheus.CounterVec
	DesignerBlocked   prometheus.Gauge
	LastSyncTimestamp prometheus.Gauge
	LastSyncSuccess   prometheus.Gauge

	// Tier 2 metrics
	FilesAdded             *prometheus.GaugeVec
	FilesModified          *prometheus.GaugeVec
	FilesDeleted           *prometheus.GaugeVec
	DesignerSessionsActive prometheus.Gauge
	SyncSkippedTotal       *prometheus.CounterVec
	GatewayStartupDuration prometheus.Histogram
}

// NewAgentMetrics creates and registers all agent metrics on a standalone registry.
func NewAgentMetrics() *AgentMetrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	reg.MustRegister(collectors.NewGoCollector())

	m := &AgentMetrics{
		registry: reg,

		SyncDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "sync_duration_seconds",
				Help:      "Duration of file sync operations in seconds.",
				Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
			[]string{"profile"},
		),
		SyncTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "sync_total",
				Help:      "Total number of sync operations.",
			},
			[]string{"profile", "result"},
		),
		FilesChanged: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "files_changed",
				Help:      "Number of files changed in the last sync.",
			},
			[]string{"profile"},
		),
		GitFetchDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "git_fetch_duration_seconds",
				Help:      "Duration of git clone/fetch operations in seconds.",
				Buckets:   []float64{0.5, 1, 2, 5, 10, 30, 60, 120},
			},
			[]string{"operation"},
		),
		GitFetchTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "git_fetch_total",
				Help:      "Total number of git clone/fetch operations.",
			},
			[]string{"operation", "result"},
		),
		ScanDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "scan_duration_seconds",
				Help:      "Duration of Ignition scan API calls in seconds.",
				Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
		),
		ScanTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "scan_total",
				Help:      "Total number of Ignition scan operations.",
			},
			[]string{"result"},
		),
		DesignerBlocked: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "designer_sessions_blocked",
				Help:      "Whether sync is currently blocked by active designer sessions (1=blocked, 0=not).",
			},
		),
		LastSyncTimestamp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "last_sync_timestamp_seconds",
				Help:      "Unix timestamp of the last successful sync.",
			},
		),
		LastSyncSuccess: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "last_sync_success",
				Help:      "Whether the last sync was successful (1=success, 0=error).",
			},
		),

		FilesAdded: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "files_added",
				Help:      "Number of files added in the last sync.",
			},
			[]string{"profile"},
		),
		FilesModified: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "files_modified",
				Help:      "Number of files modified in the last sync.",
			},
			[]string{"profile"},
		),
		FilesDeleted: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "files_deleted",
				Help:      "Number of files deleted in the last sync.",
			},
			[]string{"profile"},
		),
		DesignerSessionsActive: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "designer_sessions_active",
				Help:      "Number of active Ignition Designer sessions.",
			},
		),
		SyncSkippedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "sync_skipped_total",
				Help:      "Total number of sync operations skipped.",
			},
			[]string{"reason"},
		),
		GatewayStartupDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "stoker",
				Subsystem: "agent",
				Name:      "gateway_startup_duration_seconds",
				Help:      "Time from agent startup to gateway becoming responsive.",
				Buckets:   []float64{5, 10, 30, 60, 120, 300, 600},
			},
		),
	}

	reg.MustRegister(
		m.SyncDuration,
		m.SyncTotal,
		m.FilesChanged,
		m.GitFetchDuration,
		m.GitFetchTotal,
		m.ScanDuration,
		m.ScanTotal,
		m.DesignerBlocked,
		m.LastSyncTimestamp,
		m.LastSyncSuccess,
		m.FilesAdded,
		m.FilesModified,
		m.FilesDeleted,
		m.DesignerSessionsActive,
		m.SyncSkippedTotal,
		m.GatewayStartupDuration,
	)

	return m
}

// Handler returns an http.Handler that serves the metrics endpoint.
func (m *AgentMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
