package agent

import (
	"os"
	"path/filepath"
	"testing"

	stokertypes "github.com/ia-eknorr/stoker-operator/pkg/types"
)

func TestResolveTemplate_AllFields(t *testing.T) {
	ctx := &TemplateContext{
		GatewayName: "gw-blue",
		Namespace:   "prod",
		Ref:         "refs/heads/main",
		Commit:      "abc123",
		CRName:      "my-stoker",
		Labels:      map[string]string{"site": "us-east-1", "tier": "edge"},
		Vars:        map[string]string{"env": "production", "region": "us-east"},
	}

	tests := []struct {
		tmpl string
		want string
	}{
		{"{{.GatewayName}}", "gw-blue"},
		{"{{.Namespace}}", "prod"},
		{"{{.Ref}}", "refs/heads/main"},
		{"{{.Commit}}", "abc123"},
		{"{{.CRName}}", "my-stoker"},
		{"{{.Labels.site}}", "us-east-1"},
		{"sites/{{.Labels.tier}}/config", "sites/edge/config"},
		{"{{.Vars.env}}", "production"},
		{"config/{{.Vars.region}}/overlay", "config/us-east/overlay"},
		{"no-template", "no-template"},
	}

	for _, tt := range tests {
		got, err := resolveTemplate(tt.tmpl, ctx)
		if err != nil {
			t.Errorf("resolveTemplate(%q): %v", tt.tmpl, err)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveTemplate(%q) = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

func TestResolveTemplate_MissingKey(t *testing.T) {
	ctx := &TemplateContext{
		GatewayName: "gw",
		Labels:      map[string]string{},
		Vars:        map[string]string{},
	}

	tests := []string{
		"{{.Vars.missing}}",
		"{{.Labels.missing}}",
	}
	for _, tmpl := range tests {
		_, err := resolveTemplate(tmpl, ctx)
		if err == nil {
			t.Errorf("expected error for missing key in %q", tmpl)
		}
	}
}

func TestValidateResolvedPath_Traversal(t *testing.T) {
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"config/resources", false},
		{"projects/myproject", false},
		{"../escape", true},
		{"config/../../etc", true},
		{"/absolute/path", true},
		{".", false},
		{"config", false},
	}

	for _, tt := range tests {
		err := validateResolvedPath(tt.path, "test")
		if (err != nil) != tt.wantErr {
			t.Errorf("validateResolvedPath(%q): err=%v, wantErr=%v", tt.path, err, tt.wantErr)
		}
	}
}

func TestBuildSyncPlan_Basic(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	// Create source dirs.
	writeFile(t, filepath.Join(repoPath, "shared", "config.json"), "shared")
	writeFile(t, filepath.Join(repoPath, "site", "us-east", "override.json"), "override")

	profile := &stokertypes.ResolvedProfile{
		Mappings: []stokertypes.ResolvedMapping{
			{Source: "shared", Destination: "config/resources/core", Type: "dir"},
			{Source: "site/{{.Vars.region}}", Destination: "config/resources/core", Type: "dir"},
		},
		Vars: map[string]string{"region": "us-east"},
	}

	ctx := &TemplateContext{
		GatewayName: "gw-1",
		Namespace:   "default",
		Vars:        map[string]string{"region": "us-east"},
	}

	plan, err := buildSyncPlan(profile, ctx, repoPath, liveDir)
	if err != nil {
		t.Fatalf("buildSyncPlan: %v", err)
	}

	if len(plan.Mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(plan.Mappings))
	}

	if plan.Mappings[0].Destination != "config/resources/core" {
		t.Errorf("mapping[0].Destination = %q, want config/resources/core", plan.Mappings[0].Destination)
	}
	if plan.Mappings[1].Source != filepath.Join(repoPath, "site", "us-east") {
		t.Errorf("mapping[1].Source = %q, want %s", plan.Mappings[1].Source, filepath.Join(repoPath, "site", "us-east"))
	}
}

func TestBuildSyncPlan_RequiredMissing(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatal(err)
	}

	profile := &stokertypes.ResolvedProfile{
		Mappings: []stokertypes.ResolvedMapping{
			{Source: "nonexistent", Destination: "config", Type: "dir", Required: true},
		},
	}

	ctx := &TemplateContext{GatewayName: "gw", Namespace: "default", Vars: map[string]string{}}

	_, err := buildSyncPlan(profile, ctx, repoPath, liveDir)
	if err == nil {
		t.Error("expected error for required missing source")
	}
}

func TestBuildSyncPlan_ExcludesFromProfile(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	writeFile(t, filepath.Join(repoPath, "src", "a.txt"), "a")

	profile := &stokertypes.ResolvedProfile{
		Mappings: []stokertypes.ResolvedMapping{
			{Source: "src", Destination: "dst", Type: "dir"},
		},
		ExcludePatterns: []string{"**/*.bak", "**/*.tmp", "**/*.log"},
	}

	ctx := &TemplateContext{GatewayName: "gw", Namespace: "default", Vars: map[string]string{}}

	plan, err := buildSyncPlan(profile, ctx, repoPath, liveDir)
	if err != nil {
		t.Fatalf("buildSyncPlan: %v", err)
	}

	// Excludes come directly from the resolved profile (controller already merged defaults).
	if len(plan.ExcludePatterns) != 3 {
		t.Errorf("expected 3 exclude patterns, got %d: %v", len(plan.ExcludePatterns), plan.ExcludePatterns)
	}
}

func TestBuildTemplateContext(t *testing.T) {
	cfg := &Config{
		GatewayName: "gw-test",
		PodName:     "ignition-0",
		CRName:      "my-cr",
		CRNamespace: "my-ns",
	}
	meta := &Metadata{
		Ref:    "refs/heads/main",
		Commit: "deadbeef",
	}
	vars := map[string]string{"site": "us-east-1"}
	labels := map[string]string{"app": "ignition", "tier": "edge"}

	ctx := buildTemplateContext(cfg, meta, vars, labels)

	if ctx.GatewayName != "gw-test" {
		t.Errorf("GatewayName = %q", ctx.GatewayName)
	}
	if ctx.PodName != "ignition-0" {
		t.Errorf("PodName = %q, want ignition-0", ctx.PodName)
	}
	if ctx.Namespace != "my-ns" {
		t.Errorf("Namespace = %q", ctx.Namespace)
	}
	if ctx.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q", ctx.Ref)
	}
	if ctx.Commit != "deadbeef" {
		t.Errorf("Commit = %q", ctx.Commit)
	}
	if ctx.CRName != "my-cr" {
		t.Errorf("CRName = %q", ctx.CRName)
	}
	if ctx.Labels["app"] != "ignition" {
		t.Errorf("Labels[app] = %q", ctx.Labels["app"])
	}
	if ctx.Labels["tier"] != "edge" {
		t.Errorf("Labels[tier] = %q", ctx.Labels["tier"])
	}
	if ctx.Vars["site"] != "us-east-1" {
		t.Errorf("Vars[site] = %q", ctx.Vars["site"])
	}
}

func TestResolveTemplate_PodName(t *testing.T) {
	ctx := &TemplateContext{
		GatewayName: "ignition",
		PodName:     "ignition-2",
		Vars:        map[string]string{},
	}
	got, err := resolveTemplate("system-{{.PodName}}", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "system-ignition-2" {
		t.Errorf("got %q, want system-ignition-2", got)
	}
}

func TestApplyTemplateFunc_TextFile(t *testing.T) {
	ctx := &TemplateContext{
		GatewayName: "gw-site1",
		PodName:     "ignition-0",
		Namespace:   "prod",
		Vars:        map[string]string{"deploymentMode": "production"},
	}
	fn := buildApplyTemplateFunc(ctx)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	writeFile(t, path, `{"systemName": "{{.GatewayName}}", "mode": "{{.Vars.deploymentMode}}"}`)

	if err := fn(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"systemName": "gw-site1", "mode": "production"}`
	if string(content) != want {
		t.Errorf("got %q, want %q", string(content), want)
	}
}

func TestApplyTemplateFunc_BinaryFileRejected(t *testing.T) {
	ctx := &TemplateContext{GatewayName: "gw", Vars: map[string]string{}}
	fn := buildApplyTemplateFunc(ctx)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "binary.bin")
	// Write a file with a null byte — should be rejected.
	if err := os.WriteFile(path, []byte("hello\x00world"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := fn(path); err == nil {
		t.Error("expected error for binary file, got nil")
	}
}

func TestApplyTemplateFunc_NoTemplateSkipped(t *testing.T) {
	ctx := &TemplateContext{GatewayName: "gw", Vars: map[string]string{}}
	fn := buildApplyTemplateFunc(ctx)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "plain.txt")
	original := "no template syntax here"
	writeFile(t, path, original)

	if err := fn(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(path)
	if string(content) != original {
		t.Errorf("file was modified unexpectedly: %q", string(content))
	}
}

func TestBuildSyncPlan_TemplateFlag(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	writeFile(t, filepath.Join(repoPath, "config", "system.json"), `{"name":"{{.GatewayName}}"}`)

	profile := &stokertypes.ResolvedProfile{
		Mappings: []stokertypes.ResolvedMapping{
			{Source: "config", Destination: "config/resources", Type: "dir", Template: true},
		},
	}
	ctx := &TemplateContext{GatewayName: "gw-site1", Vars: map[string]string{}}

	plan, err := buildSyncPlan(profile, ctx, repoPath, liveDir)
	if err != nil {
		t.Fatalf("buildSyncPlan: %v", err)
	}
	if !plan.Mappings[0].Template {
		t.Error("expected Template=true to propagate to SyncPlan mapping")
	}
	if plan.ApplyTemplate == nil {
		t.Error("expected ApplyTemplate func to be set")
	}
}

// Helpers

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
