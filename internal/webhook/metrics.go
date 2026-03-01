package webhook

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	webhookReceiverRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stoker",
			Subsystem: "webhook_receiver",
			Name:      "requests_total",
			Help:      "Total number of webhook receiver requests.",
		},
		[]string{"source", "status_code"},
	)

	webhookInjectorInjectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stoker",
			Subsystem: "webhook_injector",
			Name:      "injections_total",
			Help:      "Total number of sidecar injection attempts.",
		},
		[]string{"namespace", "result"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		webhookReceiverRequestsTotal,
		webhookInjectorInjectionsTotal,
	)
}
