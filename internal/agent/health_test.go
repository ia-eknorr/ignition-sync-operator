package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthServer_ReadyzReturns503WhenShuttingDown(t *testing.T) {
	hs := NewHealthServer(":0")
	hs.MarkReady()

	// Before shutdown: readyz should return 200.
	rec := httptest.NewRecorder()
	hs.handleReadyz(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 before shutdown, got %d", rec.Code)
	}

	// Mark shutting down.
	hs.MarkNotReady()

	// After shutdown: readyz should return 503.
	rec = httptest.NewRecorder()
	hs.handleReadyz(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after MarkNotReady, got %d", rec.Code)
	}

	// Healthz should still return 200 (liveness is always ok).
	rec = httptest.NewRecorder()
	hs.handleHealthz(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected healthz 200 during shutdown, got %d", rec.Code)
	}
}

func TestHealthServer_IsShuttingDown(t *testing.T) {
	hs := NewHealthServer(":0")

	if hs.IsShuttingDown() {
		t.Fatal("expected IsShuttingDown=false initially")
	}

	hs.MarkNotReady()

	if !hs.IsShuttingDown() {
		t.Fatal("expected IsShuttingDown=true after MarkNotReady")
	}
}
