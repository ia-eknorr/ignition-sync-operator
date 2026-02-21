package syncengine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutePlan_OrderedOverlay(t *testing.T) {
	// Later mappings should overwrite files from earlier mappings.
	tmp := t.TempDir()

	// Create two source directories with overlapping files.
	srcBase := filepath.Join(tmp, "src-base")
	srcOverlay := filepath.Join(tmp, "src-overlay")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	writeTestFile(t, filepath.Join(srcBase, "config.json"), "base-content")
	writeTestFile(t, filepath.Join(srcBase, "shared.txt"), "shared")
	writeTestFile(t, filepath.Join(srcOverlay, "config.json"), "overlay-content")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: srcBase, Destination: "config", Type: "dir"},
			{Source: srcOverlay, Destination: "config", Type: "dir"},
		},
		StagingDir: staging,
		LiveDir:    live,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	// config.json should have overlay content (second mapping wins).
	got := readTestFile(t, filepath.Join(live, "config", "config.json"))
	if got != "overlay-content" {
		t.Errorf("expected overlay-content, got %q", got)
	}

	// shared.txt should still exist from base.
	got = readTestFile(t, filepath.Join(live, "config", "shared.txt"))
	if got != "shared" {
		t.Errorf("expected shared, got %q", got)
	}

	if result.FilesAdded != 2 {
		t.Errorf("expected 2 added, got %d", result.FilesAdded)
	}
}

func TestExecutePlan_FileMapping(t *testing.T) {
	tmp := t.TempDir()

	srcFile := filepath.Join(tmp, "src", "special.yaml")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	writeTestFile(t, srcFile, "file-content")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: srcFile, Destination: "deep/nested/special.yaml", Type: "file"},
		},
		StagingDir: staging,
		LiveDir:    live,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	got := readTestFile(t, filepath.Join(live, "deep", "nested", "special.yaml"))
	if got != "file-content" {
		t.Errorf("expected file-content, got %q", got)
	}

	if result.FilesAdded != 1 {
		t.Errorf("expected 1 added, got %d", result.FilesAdded)
	}
}

func TestExecutePlan_OrphanCleanup_ManagedOnly(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	// Source has one file.
	writeTestFile(t, filepath.Join(src, "keep.txt"), "keep")

	// Live has an orphan in the managed dir and a file outside managed dirs.
	writeTestFile(t, filepath.Join(live, "config", "orphan.txt"), "orphan")
	writeTestFile(t, filepath.Join(live, "unmanaged", "safe.txt"), "safe")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: src, Destination: "config", Type: "dir"},
		},
		StagingDir: staging,
		LiveDir:    live,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	// Orphan in managed dir should be deleted.
	if _, err := os.Stat(filepath.Join(live, "config", "orphan.txt")); !os.IsNotExist(err) {
		t.Error("orphan.txt should have been deleted")
	}

	// File outside managed dirs should be preserved.
	got := readTestFile(t, filepath.Join(live, "unmanaged", "safe.txt"))
	if got != "safe" {
		t.Errorf("expected safe, got %q", got)
	}

	if result.FilesDeleted != 1 {
		t.Errorf("expected 1 deleted, got %d", result.FilesDeleted)
	}
}

func TestExecutePlan_DryRun(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	writeTestFile(t, filepath.Join(src, "new.txt"), "new-content")
	writeTestFile(t, filepath.Join(src, "changed.txt"), "updated")
	writeTestFile(t, filepath.Join(live, "config", "changed.txt"), "original")
	writeTestFile(t, filepath.Join(live, "config", "orphan.txt"), "delete-me")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: src, Destination: "config", Type: "dir"},
		},
		StagingDir: staging,
		LiveDir:    live,
		DryRun:     true,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	// Verify diff was computed.
	if result.DryRunDiff == nil {
		t.Fatal("expected DryRunDiff to be set")
	}
	if len(result.DryRunDiff.Added) != 1 {
		t.Errorf("expected 1 added, got %d: %v", len(result.DryRunDiff.Added), result.DryRunDiff.Added)
	}
	if len(result.DryRunDiff.Modified) != 1 {
		t.Errorf("expected 1 modified, got %d: %v", len(result.DryRunDiff.Modified), result.DryRunDiff.Modified)
	}
	if len(result.DryRunDiff.Deleted) != 1 {
		t.Errorf("expected 1 deleted, got %d: %v", len(result.DryRunDiff.Deleted), result.DryRunDiff.Deleted)
	}

	// Live dir should be unchanged (dry-run).
	got := readTestFile(t, filepath.Join(live, "config", "changed.txt"))
	if got != "original" {
		t.Errorf("dry-run should not modify live; got %q", got)
	}
	if _, err := os.Stat(filepath.Join(live, "config", "new.txt")); !os.IsNotExist(err) {
		t.Error("dry-run should not create new files in live")
	}
	got = readTestFile(t, filepath.Join(live, "config", "orphan.txt"))
	if got != "delete-me" {
		t.Errorf("dry-run should not delete files from live; got %q", got)
	}
}

func TestExecutePlan_ExcludePatterns(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	writeTestFile(t, filepath.Join(src, "keep.txt"), "keep")
	writeTestFile(t, filepath.Join(src, "skip.log"), "skip")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: src, Destination: "data", Type: "dir"},
		},
		ExcludePatterns: []string{"**/*.log"},
		StagingDir:      staging,
		LiveDir:         live,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	// keep.txt should be synced.
	got := readTestFile(t, filepath.Join(live, "data", "keep.txt"))
	if got != "keep" {
		t.Errorf("expected keep, got %q", got)
	}

	// skip.log should be excluded.
	if _, err := os.Stat(filepath.Join(live, "data", "skip.log")); !os.IsNotExist(err) {
		t.Error("skip.log should have been excluded")
	}

	if result.FilesAdded != 1 {
		t.Errorf("expected 1 added, got %d", result.FilesAdded)
	}
}

func TestExecutePlan_ProtectedPaths(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	// Source has a .resources directory (should be skipped).
	writeTestFile(t, filepath.Join(src, "keep.txt"), "keep")
	writeTestFile(t, filepath.Join(src, ".resources", "internal.bin"), "internal")

	// Live has a .resources directory that must be preserved.
	writeTestFile(t, filepath.Join(live, "data", ".resources", "existing.bin"), "preserve")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: src, Destination: "data", Type: "dir"},
		},
		StagingDir: staging,
		LiveDir:    live,
	}

	_, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	// .resources in live should be preserved.
	got := readTestFile(t, filepath.Join(live, "data", ".resources", "existing.bin"))
	if got != "preserve" {
		t.Errorf("expected preserve, got %q", got)
	}
}

func TestExecutePlan_ModifiedCounting(t *testing.T) {
	// Regression test: ensure modified files are counted correctly.
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	writeTestFile(t, filepath.Join(src, "existing.txt"), "new-version")
	writeTestFile(t, filepath.Join(live, "data", "existing.txt"), "old-version")

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: src, Destination: "data", Type: "dir"},
		},
		StagingDir: staging,
		LiveDir:    live,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	if result.FilesAdded != 0 {
		t.Errorf("expected 0 added, got %d", result.FilesAdded)
	}
	if result.FilesModified != 1 {
		t.Errorf("expected 1 modified, got %d", result.FilesModified)
	}
}

func TestExecutePlan_SkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()

	src := filepath.Join(tmp, "src")
	staging := filepath.Join(tmp, "staging")
	live := filepath.Join(tmp, "live")

	// Create a regular file and a symlink in the source directory.
	writeTestFile(t, filepath.Join(src, "real.txt"), "real-content")

	// Create a symlink pointing to the regular file.
	if err := os.Symlink(filepath.Join(src, "real.txt"), filepath.Join(src, "link.txt")); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	engine := &Engine{}
	plan := &SyncPlan{
		Mappings: []ResolvedMapping{
			{Source: src, Destination: "data", Type: "dir"},
		},
		StagingDir: staging,
		LiveDir:    live,
	}

	result, err := engine.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	// Regular file should be synced.
	got := readTestFile(t, filepath.Join(live, "data", "real.txt"))
	if got != "real-content" {
		t.Errorf("expected real-content, got %q", got)
	}

	// Symlink should NOT be synced.
	if _, err := os.Lstat(filepath.Join(live, "data", "link.txt")); !os.IsNotExist(err) {
		t.Error("symlink link.txt should not have been synced to live")
	}

	if result.FilesAdded != 1 {
		t.Errorf("expected 1 added (only real.txt), got %d", result.FilesAdded)
	}
}

// Helpers

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
