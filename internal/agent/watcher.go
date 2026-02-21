package agent

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Watcher watches the metadata ConfigMap for changes and emits events on a channel.
type Watcher struct {
	client    client.Client
	namespace string
	cmName    string
	syncCh    chan struct{}
	period    time.Duration
}

// NewWatcher creates a Watcher for the metadata ConfigMap.
func NewWatcher(c client.Client, namespace, crName string, syncPeriod time.Duration) *Watcher {
	return &Watcher{
		client:    c,
		namespace: namespace,
		cmName:    MetadataConfigMapName(crName),
		syncCh:    make(chan struct{}, 1),
		period:    syncPeriod,
	}
}

// Events returns the channel that receives sync trigger notifications.
func (w *Watcher) Events() <-chan struct{} {
	return w.syncCh
}

// trigger sends a non-blocking notification on the sync channel.
func (w *Watcher) trigger() {
	select {
	case w.syncCh <- struct{}{}:
	default:
		// Channel already has a pending event.
	}
}

// Run starts both the ConfigMap poller and fallback timer.
// Blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("watcher")

	// Start ConfigMap poller in background.
	go w.pollConfigMap(ctx)

	// Fallback timer — ensures sync even if poller misses events.
	ticker := time.NewTicker(w.period)
	defer ticker.Stop()

	log.Info("watcher started", "configmap", w.cmName, "fallbackPeriod", w.period)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.V(1).Info("fallback timer triggered sync")
			w.trigger()
		}
	}
}

// pollConfigMap periodically reads the metadata ConfigMap and triggers sync on change.
func (w *Watcher) pollConfigMap(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("cm-poller")

	var lastVersion string
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cm := &corev1.ConfigMap{}
			key := client.ObjectKey{Name: w.cmName, Namespace: w.namespace}
			if err := w.client.Get(ctx, key, cm); err != nil {
				log.V(1).Info("metadata ConfigMap not yet available", "error", err)
				continue
			}

			if cm.ResourceVersion != lastVersion {
				if lastVersion != "" {
					log.Info("metadata ConfigMap changed", "version", cm.ResourceVersion)
					w.trigger()
				}
				lastVersion = cm.ResourceVersion
			}
		}
	}
}
