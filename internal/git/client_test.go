package git

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
)

func newAdvRefs(refs map[string]string, peeled map[string]string) *packp.AdvRefs {
	ar := packp.NewAdvRefs()
	for name, hash := range refs {
		ar.References[name] = plumbing.NewHash(hash)
	}
	for name, hash := range peeled {
		ar.Peeled[name] = plumbing.NewHash(hash)
	}
	return ar
}

func TestMatchRef_AnnotatedTag(t *testing.T) {
	tagObjHash := "6019298770000000000000000000000000000000"
	commitHash := "78ace97500000000000000000000000000000000"

	ar := newAdvRefs(
		map[string]string{"refs/tags/2.2.3": tagObjHash},
		map[string]string{"refs/tags/2.2.3": commitHash},
	)

	res, err := matchRef(ar, "2.2.3", "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Commit != commitHash {
		t.Errorf("expected commit hash %s (peeled), got %s (tag object)", commitHash, res.Commit)
	}
	if res.Ref != "2.2.3" {
		t.Errorf("expected ref %q, got %q", "2.2.3", res.Ref)
	}
}

func TestMatchRef_LightweightTag(t *testing.T) {
	commitHash := "78ace97500000000000000000000000000000000"

	ar := newAdvRefs(
		map[string]string{"refs/tags/v1.0": commitHash},
		nil,
	)

	res, err := matchRef(ar, "v1.0", "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Commit != commitHash {
		t.Errorf("expected %s, got %s", commitHash, res.Commit)
	}
}

func TestMatchRef_Branch(t *testing.T) {
	commitHash := "abcdef1234567890abcdef1234567890abcdef12"

	ar := newAdvRefs(
		map[string]string{"refs/heads/main": commitHash},
		nil,
	)

	res, err := matchRef(ar, "main", "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Commit != commitHash {
		t.Errorf("expected %s, got %s", commitHash, res.Commit)
	}
}

func TestMatchRef_FullSHA(t *testing.T) {
	sha := "abcdef1234567890abcdef1234567890abcdef12"

	res, err := matchRef(packp.NewAdvRefs(), sha, "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Commit != sha {
		t.Errorf("expected %s, got %s", sha, res.Commit)
	}
}

func TestMatchRef_TagPreferredOverBranch(t *testing.T) {
	tagHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	branchHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	ar := newAdvRefs(
		map[string]string{
			"refs/tags/v1.0":  tagHash,
			"refs/heads/v1.0": branchHash,
		},
		nil,
	)

	res, err := matchRef(ar, "v1.0", "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Commit != tagHash {
		t.Errorf("expected tag hash %s, got %s", tagHash, res.Commit)
	}
}

func TestMatchRef_NotFound(t *testing.T) {
	ar := newAdvRefs(
		map[string]string{"refs/heads/main": "abcdef1234567890abcdef1234567890abcdef12"},
		nil,
	)

	_, err := matchRef(ar, "nonexistent", "https://example.com/repo.git")
	if err == nil {
		t.Fatal("expected error for nonexistent ref")
	}
}
