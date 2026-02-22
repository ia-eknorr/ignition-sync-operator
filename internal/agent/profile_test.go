package agent

import (
	"os"
	"path/filepath"
	"testing"

	stokerv1alpha1 "github.com/ia-eknorr/stoker-operator/api/v1alpha1"
)

func TestResolveTemplate_AllFields(t *testing.T) {
	ctx := &TemplateContext{
		GatewayName: "gw-blue",
		Namespace:   "prod",
		Ref:         "refs/heads/main",
		Commit:      "abc123",
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
		Vars:        map[string]string{},
	}

	_, err := resolveTemplate("{{.Vars.missing}}", ctx)
	if err == nil {
		t.Error("expected error for missing template key")
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

	profile := &stokerv1alpha1.SyncProfileSpec{
		Mappings: []stokerv1alpha1.SyncMapping{
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

	plan, err := buildSyncPlan(profile, ctx, repoPath, liveDir, nil)
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

func TestBuildSyncPlan_WithDeploymentMode(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	writeFile(t, filepath.Join(repoPath, "shared", "a.txt"), "a")
	writeFile(t, filepath.Join(repoPath, "modes", "dev", "b.txt"), "b")

	profile := &stokerv1alpha1.SyncProfileSpec{
		Mappings: []stokerv1alpha1.SyncMapping{
			{Source: "shared", Destination: "config/resources/core", Type: "dir"},
		},
		DeploymentMode: &stokerv1alpha1.DeploymentModeSpec{
			Name:   "dev",
			Source: "modes/dev",
		},
	}

	ctx := &TemplateContext{GatewayName: "gw", Namespace: "default", Vars: map[string]string{}}

	plan, err := buildSyncPlan(profile, ctx, repoPath, liveDir, nil)
	if err != nil {
		t.Fatalf("buildSyncPlan: %v", err)
	}

	// Should have 2 mappings: user mapping + deployment mode.
	if len(plan.Mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(plan.Mappings))
	}

	last := plan.Mappings[len(plan.Mappings)-1]
	if last.Destination != "config/resources/core" {
		t.Errorf("deployment mode destination = %q, want config/resources/core", last.Destination)
	}
	if last.Source != filepath.Join(repoPath, "modes", "dev") {
		t.Errorf("deployment mode source = %q", last.Source)
	}
}

func TestBuildSyncPlan_RequiredMissing(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatal(err)
	}

	profile := &stokerv1alpha1.SyncProfileSpec{
		Mappings: []stokerv1alpha1.SyncMapping{
			{Source: "nonexistent", Destination: "config", Type: "dir", Required: true},
		},
	}

	ctx := &TemplateContext{GatewayName: "gw", Namespace: "default", Vars: map[string]string{}}

	_, err := buildSyncPlan(profile, ctx, repoPath, liveDir, nil)
	if err == nil {
		t.Error("expected error for required missing source")
	}
}

func TestBuildSyncPlan_ExcludeMerging(t *testing.T) {
	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	liveDir := filepath.Join(tmp, "live")

	writeFile(t, filepath.Join(repoPath, "src", "a.txt"), "a")

	profile := &stokerv1alpha1.SyncProfileSpec{
		Mappings: []stokerv1alpha1.SyncMapping{
			{Source: "src", Destination: "dst", Type: "dir"},
		},
		ExcludePatterns: []string{"**/*.bak"},
	}

	ctx := &TemplateContext{GatewayName: "gw", Namespace: "default", Vars: map[string]string{}}
	crExcludes := []string{"**/*.tmp", "**/*.log"}

	plan, err := buildSyncPlan(profile, ctx, repoPath, liveDir, crExcludes)
	if err != nil {
		t.Fatalf("buildSyncPlan: %v", err)
	}

	// Should have CR excludes + profile excludes.
	if len(plan.ExcludePatterns) != 3 {
		t.Errorf("expected 3 exclude patterns, got %d: %v", len(plan.ExcludePatterns), plan.ExcludePatterns)
	}
}

func TestParseCRExcludes(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"**/*.log", 1},
		{"**/*.log, **/*.tmp", 2},
		{"  a , b , , c ", 3},
	}

	for _, tt := range tests {
		got := parseCRExcludes(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseCRExcludes(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}

func TestBuildTemplateContext(t *testing.T) {
	cfg := &Config{
		GatewayName: "gw-test",
		CRNamespace: "my-ns",
	}
	meta := &Metadata{
		Ref:    "refs/heads/main",
		Commit: "deadbeef",
	}
	vars := map[string]string{"site": "us-east-1"}

	ctx := buildTemplateContext(cfg, meta, vars)

	if ctx.GatewayName != "gw-test" {
		t.Errorf("GatewayName = %q", ctx.GatewayName)
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
	if ctx.Vars["site"] != "us-east-1" {
		t.Errorf("Vars[site] = %q", ctx.Vars["site"])
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
