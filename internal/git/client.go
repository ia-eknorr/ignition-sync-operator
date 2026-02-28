package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Result holds the outcome of a clone or fetch operation.
type Result struct {
	Commit string
	Ref    string
}

// Client is the interface for git operations.
type Client interface {
	// LsRemote resolves a ref to a commit SHA via a single HTTP/SSH call
	// without cloning the repository. Used by the controller.
	LsRemote(ctx context.Context, repoURL, ref string, auth transport.AuthMethod) (Result, error)

	// CloneOrFetch clones the repo if the target directory is empty,
	// or fetches + checks out the ref if already cloned.
	// Used by the agent sidecar.
	CloneOrFetch(ctx context.Context, repoURL, ref, path string, auth transport.AuthMethod) (Result, error)
}

// GoGitClient implements Client using go-git.
type GoGitClient struct{}

var _ Client = (*GoGitClient)(nil)

func (g *GoGitClient) LsRemote(ctx context.Context, repoURL, ref string, auth transport.AuthMethod) (Result, error) {
	remote := gogit.NewRemote(nil, &gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})

	refs, err := remote.ListContext(ctx, &gogit.ListOptions{Auth: auth})
	if err != nil {
		return Result{}, fmt.Errorf("ls-remote %s: %w", repoURL, err)
	}

	// If ref is already a full SHA, return it directly
	if plumbing.IsHash(ref) {
		return Result{Commit: ref, Ref: ref}, nil
	}

	// Search for matching ref: exact tag, then branch
	candidates := []string{
		"refs/tags/" + ref,
		"refs/heads/" + ref,
	}

	for _, candidate := range candidates {
		for _, r := range refs {
			if r.Name().String() == candidate {
				return Result{Commit: r.Hash().String(), Ref: ref}, nil
			}
		}
	}

	// Check for peeled tag refs (annotated tags have ^{} entries)
	for _, candidate := range candidates {
		peeledName := candidate + "^{}"
		for _, r := range refs {
			if r.Name().String() == peeledName {
				return Result{Commit: r.Hash().String(), Ref: ref}, nil
			}
		}
	}

	return Result{}, fmt.Errorf("ref %q not found in remote %s", ref, repoURL)
}

func (g *GoGitClient) CloneOrFetch(ctx context.Context, repoURL, ref, path string, auth transport.AuthMethod) (Result, error) {
	// Check if the directory already contains a cloned repo
	if isCloned(path) {
		return g.fetchAndCheckout(ctx, repoURL, ref, path, auth)
	}
	return g.cloneAndCheckout(ctx, repoURL, ref, path, auth)
}

func (g *GoGitClient) cloneAndCheckout(ctx context.Context, repoURL, ref, path string, auth transport.AuthMethod) (Result, error) {
	repo, err := gogit.PlainCloneContext(ctx, path, false, &gogit.CloneOptions{
		URL:   repoURL,
		Auth:  auth,
		Depth: 1,
	})
	if err != nil {
		return Result{}, fmt.Errorf("git clone %s: %w", repoURL, err)
	}

	return checkoutRef(repo, ref)
}

func (g *GoGitClient) fetchAndCheckout(ctx context.Context, repoURL, ref, path string, auth transport.AuthMethod) (Result, error) {
	repo, err := gogit.PlainOpen(path)
	if err != nil {
		return Result{}, fmt.Errorf("opening repo at %s: %w", path, err)
	}

	// Ensure the remote URL matches the CR spec (handles repo URL changes).
	if err := ensureRemoteURL(repo, repoURL); err != nil {
		return Result{}, err
	}

	err = repo.FetchContext(ctx, &gogit.FetchOptions{
		Auth:  auth,
		Force: true,
		Tags:  gogit.AllTags,
	})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return Result{}, fmt.Errorf("git fetch: %w", err)
	}

	return checkoutRef(repo, ref)
}

// ensureRemoteURL updates the origin remote URL if it differs from the desired URL.
func ensureRemoteURL(repo *gogit.Repository, desiredURL string) error {
	remote, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("getting origin remote: %w", err)
	}
	urls := remote.Config().URLs
	if len(urls) > 0 && urls[0] == desiredURL {
		return nil
	}
	// Remove and re-add origin with the correct URL
	if err := repo.DeleteRemote("origin"); err != nil {
		return fmt.Errorf("deleting origin remote: %w", err)
	}
	if _, err := repo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{desiredURL},
	}); err != nil {
		return fmt.Errorf("creating origin remote: %w", err)
	}
	return nil
}

// checkoutRef resolves a ref (branch, tag, or commit SHA) and checks it out.
func checkoutRef(repo *gogit.Repository, ref string) (Result, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return Result{}, fmt.Errorf("getting worktree: %w", err)
	}

	hash, err := resolveRef(repo, ref)
	if err != nil {
		return Result{}, err
	}

	if err := wt.Checkout(&gogit.CheckoutOptions{
		Hash:  hash,
		Force: true,
	}); err != nil {
		return Result{}, fmt.Errorf("checkout %s: %w", ref, err)
	}

	return Result{
		Commit: hash.String(),
		Ref:    ref,
	}, nil
}

// resolveRef tries to resolve a ref as: exact commit SHA, tag, then branch.
func resolveRef(repo *gogit.Repository, ref string) (plumbing.Hash, error) {
	// Try as a full SHA
	if plumbing.IsHash(ref) {
		return plumbing.NewHash(ref), nil
	}

	// Try as a tag
	tagRef, err := repo.Tag(ref)
	if err == nil {
		return tagRef.Hash(), nil
	}

	// Try as refs/tags/
	resolved, err := repo.ResolveRevision(plumbing.Revision("refs/tags/" + ref))
	if err == nil {
		return *resolved, nil
	}

	// Try as a branch (remote tracking)
	resolved, err = repo.ResolveRevision(plumbing.Revision("refs/remotes/origin/" + ref))
	if err == nil {
		return *resolved, nil
	}

	// Last resort: let go-git try to resolve it
	resolved, err = repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("cannot resolve ref %q: %w", ref, err)
	}
	return *resolved, nil
}

// isCloned checks if a directory contains a valid git repository.
func isCloned(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// NativeGitClient implements Client using the native git binary via exec.Command.
// Unlike GoGitClient, it streams pack data rather than loading it into memory,
// making it suitable for large repositories where go-git causes OOM kills.
type NativeGitClient struct{}

var _ Client = (*NativeGitClient)(nil)

// LsRemote is not supported by NativeGitClient. The controller uses GoGitClient for this.
func (g *NativeGitClient) LsRemote(_ context.Context, _, _ string, _ transport.AuthMethod) (Result, error) {
	return Result{}, fmt.Errorf("NativeGitClient does not support LsRemote")
}

// CloneOrFetch clones or fetches using the native git binary.
// The transport.AuthMethod parameter is ignored; auth is configured via
// GIT_SSH_KEY_FILE (SSH key path) or GIT_TOKEN_FILE (token path) env vars.
func (g *NativeGitClient) CloneOrFetch(ctx context.Context, repoURL, ref, path string, _ transport.AuthMethod) (Result, error) {
	authURL, env, cleanup, err := buildGitEnv(repoURL)
	if err != nil {
		return Result{}, fmt.Errorf("setting up git env: %w", err)
	}
	defer cleanup()

	if isCloned(path) {
		return nativeFetchAndCheckout(ctx, authURL, ref, path, env)
	}
	return nativeCloneAndCheckout(ctx, authURL, ref, path, env)
}

// buildGitEnv prepares environment variables for native git commands.
// For SSH repos, copies the key to /tmp with 0600 permissions and sets GIT_SSH_COMMAND.
// For token repos, injects the token into the URL.
// Returns the (possibly modified) URL, env vars, a cleanup func, and any error.
func buildGitEnv(repoURL string) (string, []string, func(), error) {
	base := []string{
		"HOME=/tmp",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
	}
	noop := func() {}

	if keyFile := os.Getenv("GIT_SSH_KEY_FILE"); keyFile != "" {
		keyData, err := os.ReadFile(keyFile)
		if err != nil {
			return repoURL, nil, noop, fmt.Errorf("reading SSH key %s: %w", keyFile, err)
		}
		tmpKey := "/tmp/stoker-ssh-key"
		if err := os.WriteFile(tmpKey, keyData, 0600); err != nil {
			return repoURL, nil, noop, fmt.Errorf("writing SSH key to /tmp: %w", err)
		}
		env := append(base, fmt.Sprintf(
			"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o BatchMode=yes -o IdentitiesOnly=yes",
			tmpKey,
		))
		return repoURL, env, func() { _ = os.Remove(tmpKey) }, nil
	}

	if tokenFile := os.Getenv("GIT_TOKEN_FILE"); tokenFile != "" {
		tokenData, err := os.ReadFile(tokenFile)
		if err != nil {
			return repoURL, nil, noop, fmt.Errorf("reading token file %s: %w", tokenFile, err)
		}
		token := strings.TrimSpace(string(tokenData))
		return injectTokenIntoURL(repoURL, token), base, noop, nil
	}

	return repoURL, base, noop, nil
}

// injectTokenIntoURL inserts an OAuth token credential into an HTTPS git URL.
func injectTokenIntoURL(repoURL, token string) string {
	if after, ok := strings.CutPrefix(repoURL, "https://"); ok {
		return "https://x-access-token:" + token + "@" + after
	}
	if after, ok := strings.CutPrefix(repoURL, "http://"); ok {
		return "http://x-access-token:" + token + "@" + after
	}
	return repoURL
}

func nativeCloneAndCheckout(ctx context.Context, repoURL, ref, path string, env []string) (Result, error) {
	// Shallow clone the default branch, then fetch the specific ref.
	// Using --depth=1 avoids loading the full pack history into memory.
	if _, err := runGit(ctx, []string{"clone", "--depth=1", repoURL, path}, "", env); err != nil {
		return Result{}, fmt.Errorf("git clone: %w", err)
	}
	if _, err := runGit(ctx, []string{"fetch", "--depth=1", "origin", ref}, path, env); err != nil {
		return Result{}, fmt.Errorf("git fetch %s: %w", ref, err)
	}
	if _, err := runGit(ctx, []string{"checkout", "-f", "FETCH_HEAD"}, path, env); err != nil {
		return Result{}, fmt.Errorf("git checkout FETCH_HEAD: %w", err)
	}
	return nativeRevParse(ctx, ref, path, env)
}

func nativeFetchAndCheckout(ctx context.Context, repoURL, ref, path string, env []string) (Result, error) {
	// Update remote URL in case it changed in the CR spec.
	if _, err := runGit(ctx, []string{"remote", "set-url", "origin", repoURL}, path, env); err != nil {
		return Result{}, fmt.Errorf("git remote set-url: %w", err)
	}
	if _, err := runGit(ctx, []string{"fetch", "--depth=1", "origin", ref}, path, env); err != nil {
		return Result{}, fmt.Errorf("git fetch: %w", err)
	}
	if _, err := runGit(ctx, []string{"checkout", "-f", "FETCH_HEAD"}, path, env); err != nil {
		return Result{}, fmt.Errorf("git checkout: %w", err)
	}
	return nativeRevParse(ctx, ref, path, env)
}

func nativeRevParse(ctx context.Context, ref, path string, env []string) (Result, error) {
	commit, err := runGit(ctx, []string{"rev-parse", "HEAD"}, path, env)
	if err != nil {
		return Result{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return Result{Commit: commit, Ref: ref}, nil
}

// runGit runs a git command and returns the trimmed combined output.
func runGit(ctx context.Context, args []string, dir string, extraEnv []string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeOutput(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// tokenRe matches credential tokens embedded in git URLs (https://user:token@host).
var tokenRe = regexp.MustCompile(`://[^@\s]+@`)

// sanitizeOutput strips credential tokens from git output before logging or surfacing in status.
func sanitizeOutput(s string) string {
	return tokenRe.ReplaceAllString(strings.TrimSpace(s), "://<redacted>@")
}
