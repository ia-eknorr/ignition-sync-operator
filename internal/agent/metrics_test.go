package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewAgentMetrics(t *testing.T) {
	m := NewAgentMetrics()
	if m == nil {
		t.Fatal("expected non-nil AgentMetrics")
	}
	if m.registry == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestAgentMetrics_SyncObservations(t *testing.T) {
	m := NewAgentMetrics()

	m.SyncDuration.WithLabelValues("default").Observe(1.5)
	m.SyncTotal.WithLabelValues("default", "success").Inc()
	m.FilesChanged.WithLabelValues("default").Set(5)

	if v := testutil.ToFloat64(m.SyncTotal.WithLabelValues("default", "success")); v != 1 {
		t.Errorf("expected sync_total=1, got %f", v)
	}
	if v := testutil.ToFloat64(m.FilesChanged.WithLabelValues("default")); v != 5 {
		t.Errorf("expected files_changed=5, got %f", v)
	}
}

func TestAgentMetrics_GitFetchObservations(t *testing.T) {
	m := NewAgentMetrics()

	m.GitFetchDuration.WithLabelValues("clone").Observe(3.0)
	m.GitFetchTotal.WithLabelValues("clone", "success").Inc()
	m.GitFetchTotal.WithLabelValues("fetch", "error").Inc()

	if v := testutil.ToFloat64(m.GitFetchTotal.WithLabelValues("clone", "success")); v != 1 {
		t.Errorf("expected git_fetch_total clone/success=1, got %f", v)
	}
	if v := testutil.ToFloat64(m.GitFetchTotal.WithLabelValues("fetch", "error")); v != 1 {
		t.Errorf("expected git_fetch_total fetch/error=1, got %f", v)
	}
}

func TestAgentMetrics_ScanObservations(t *testing.T) {
	m := NewAgentMetrics()

	m.ScanDuration.Observe(0.3)
	m.ScanTotal.WithLabelValues("success").Inc()
	m.ScanTotal.WithLabelValues("success").Inc()
	m.ScanTotal.WithLabelValues("error").Inc()

	if v := testutil.ToFloat64(m.ScanTotal.WithLabelValues("success")); v != 2 {
		t.Errorf("expected scan_total success=2, got %f", v)
	}
	if v := testutil.ToFloat64(m.ScanTotal.WithLabelValues("error")); v != 1 {
		t.Errorf("expected scan_total error=1, got %f", v)
	}
}

func TestAgentMetrics_DesignerBlockedGauge(t *testing.T) {
	m := NewAgentMetrics()

	m.DesignerBlocked.Set(1)
	if v := testutil.ToFloat64(m.DesignerBlocked); v != 1 {
		t.Errorf("expected designer_blocked=1, got %f", v)
	}

	m.DesignerBlocked.Set(0)
	if v := testutil.ToFloat64(m.DesignerBlocked); v != 0 {
		t.Errorf("expected designer_blocked=0, got %f", v)
	}
}

func TestAgentMetrics_LastSyncGauges(t *testing.T) {
	m := NewAgentMetrics()

	m.LastSyncTimestamp.Set(1700000000)
	m.LastSyncSuccess.Set(1)

	if v := testutil.ToFloat64(m.LastSyncTimestamp); v != 1700000000 {
		t.Errorf("expected last_sync_timestamp=1700000000, got %f", v)
	}
	if v := testutil.ToFloat64(m.LastSyncSuccess); v != 1 {
		t.Errorf("expected last_sync_success=1, got %f", v)
	}
}

func TestAgentMetrics_FileBreakdownGauges(t *testing.T) {
	m := NewAgentMetrics()

	m.FilesAdded.WithLabelValues("default").Set(3)
	m.FilesModified.WithLabelValues("default").Set(2)
	m.FilesDeleted.WithLabelValues("default").Set(1)

	if v := testutil.ToFloat64(m.FilesAdded.WithLabelValues("default")); v != 3 {
		t.Errorf("expected files_added=3, got %f", v)
	}
	if v := testutil.ToFloat64(m.FilesModified.WithLabelValues("default")); v != 2 {
		t.Errorf("expected files_modified=2, got %f", v)
	}
	if v := testutil.ToFloat64(m.FilesDeleted.WithLabelValues("default")); v != 1 {
		t.Errorf("expected files_deleted=1, got %f", v)
	}
}

func TestAgentMetrics_DesignerSessionsActive(t *testing.T) {
	m := NewAgentMetrics()

	m.DesignerSessionsActive.Set(3)
	if v := testutil.ToFloat64(m.DesignerSessionsActive); v != 3 {
		t.Errorf("expected designer_sessions_active=3, got %f", v)
	}

	m.DesignerSessionsActive.Set(0)
	if v := testutil.ToFloat64(m.DesignerSessionsActive); v != 0 {
		t.Errorf("expected designer_sessions_active=0, got %f", v)
	}
}

func TestAgentMetrics_SyncSkippedTotal(t *testing.T) {
	m := NewAgentMetrics()

	reasons := []string{"paused", "commit_unchanged", "profile_error", "designer_blocked"}
	for _, reason := range reasons {
		m.SyncSkippedTotal.WithLabelValues(reason).Inc()
	}
	m.SyncSkippedTotal.WithLabelValues("paused").Inc() // paused should be 2

	if v := testutil.ToFloat64(m.SyncSkippedTotal.WithLabelValues("paused")); v != 2 {
		t.Errorf("expected sync_skipped_total{reason=paused}=2, got %f", v)
	}
	if v := testutil.ToFloat64(m.SyncSkippedTotal.WithLabelValues("commit_unchanged")); v != 1 {
		t.Errorf("expected sync_skipped_total{reason=commit_unchanged}=1, got %f", v)
	}
}

func TestAgentMetrics_GatewayStartupDuration(t *testing.T) {
	m := NewAgentMetrics()

	m.GatewayStartupDuration.Observe(15.5)
	m.GatewayStartupDuration.Observe(45.0)

	count := testutil.CollectAndCount(m.GatewayStartupDuration)
	if count <= 0 {
		t.Errorf("expected gateway_startup_duration to have series, got %d", count)
	}
}

func TestAgentMetrics_Handler(t *testing.T) {
	m := NewAgentMetrics()

	// Observe some metrics so they appear in output.
	m.SyncTotal.WithLabelValues("default", "success").Inc()
	m.LastSyncSuccess.Set(1)
	m.SyncSkippedTotal.WithLabelValues("paused").Inc()
	m.FilesAdded.WithLabelValues("default").Set(1)
	m.FilesModified.WithLabelValues("default").Set(2)
	m.FilesDeleted.WithLabelValues("default").Set(0)
	m.DesignerSessionsActive.Set(0)
	m.GatewayStartupDuration.Observe(30.0)

	handler := m.Handler()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Verify key metric families appear in the output.
	for _, metric := range []string{
		"stoker_agent_sync_total",
		"stoker_agent_last_sync_success",
		"stoker_agent_files_added",
		"stoker_agent_files_modified",
		"stoker_agent_files_deleted",
		"stoker_agent_sync_skipped_total",
		"stoker_agent_designer_sessions_active",
		"stoker_agent_gateway_startup_duration_seconds",
		"process_cpu_seconds_total",
		"go_goroutines",
	} {
		if !strings.Contains(bodyStr, metric) {
			t.Errorf("expected %q in /metrics output", metric)
		}
	}
}
