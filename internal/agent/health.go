package agent

import (
	"context"
	"net/http"
	"sync/atomic"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// HealthServer exposes /healthz, /readyz, and /startupz endpoints.
type HealthServer struct {
	initialSyncDone atomic.Bool
	server          *http.Server
}

// NewHealthServer creates a health server on the given address (e.g., ":8082").
func NewHealthServer(addr string) *HealthServer {
	hs := &HealthServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", hs.handleHealthz)
	mux.HandleFunc("/readyz", hs.handleReadyz)
	mux.HandleFunc("/startupz", hs.handleStartupz)

	hs.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return hs
}

// MarkReady signals that the initial sync has completed.
func (hs *HealthServer) MarkReady() {
	hs.initialSyncDone.Store(true)
}

// Start begins serving health endpoints. Blocks until ctx is cancelled.
func (hs *HealthServer) Start(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("health")

	go func() {
		<-ctx.Done()
		hs.server.Close()
	}()

	log.Info("health server starting", "addr", hs.server.Addr)
	if err := hs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error(err, "health server error")
	}
}

func (hs *HealthServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (hs *HealthServer) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if hs.initialSyncDone.Load() {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready"))
	}
}

func (hs *HealthServer) handleStartupz(w http.ResponseWriter, _ *http.Request) {
	if hs.initialSyncDone.Load() {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("starting"))
	}
}
