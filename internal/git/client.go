package git

import (
	"context"
	"fmt"
	"os"

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
		URL:  repoURL,
		Auth: auth,
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
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = gogit.PlainOpen(path)
	return err == nil
}
