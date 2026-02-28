//go:build integration

package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNativeGitClientClone_PublicDemo validates that NativeGitClient can clone
// the publicdemo-all repo without OOMing. Run with:
//   GIT_SSH_KEY_FILE=/tmp/test-ssh-key go test ./internal/git -tags integration -run TestNativeGitClientClone_PublicDemo -v -timeout 120s
func TestNativeGitClientClone_PublicDemo(t *testing.T) {
	keyFile := os.Getenv("GIT_SSH_KEY_FILE")
	if keyFile == "" {
		t.Skip("GIT_SSH_KEY_FILE not set")
	}

	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := &NativeGitClient{}
	result, err := client.CloneOrFetch(ctx, "org-147873951@github.com:inductive-automation/publicdemo-all.git", "main", repoPath, nil)
	if err != nil {
		t.Fatalf("CloneOrFetch failed: %v", err)
	}

	t.Logf("Clone succeeded: commit=%s ref=%s", result.Commit, result.Ref)

	if result.Commit == "" {
		t.Error("expected non-empty commit SHA")
	}

	// Verify .git dir and at least one file exist
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		t.Errorf(".git dir missing: %v", err)
	}
}

func TestNativeGitClientFetch_PublicDemo(t *testing.T) {
	keyFile := os.Getenv("GIT_SSH_KEY_FILE")
	if keyFile == "" {
		t.Skip("GIT_SSH_KEY_FILE not set")
	}

	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	ctx := context.Background()
	client := &NativeGitClient{}
	repoURL := "org-147873951@github.com:inductive-automation/publicdemo-all.git"

	// First call: clone
	r1, err := client.CloneOrFetch(ctx, repoURL, "main", repoPath, nil)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	t.Logf("Clone: commit=%s", r1.Commit)

	// Second call: fetch (isCloned returns true)
	r2, err := client.CloneOrFetch(ctx, repoURL, "main", repoPath, nil)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	t.Logf("Fetch: commit=%s", r2.Commit)

	if r1.Commit != r2.Commit {
		t.Errorf("commit mismatch after fetch: clone=%s fetch=%s", r1.Commit, r2.Commit)
	}
}
