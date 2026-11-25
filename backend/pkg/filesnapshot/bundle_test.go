package filesnapshot

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bareStore is an empty bare repository a chain is fetched into, like the
// store a new sandbox, or a folder whose .git was replaced, starts from.
func bareStore(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

func showIn(t *testing.T, gitDir, object string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "show", object).CombinedOutput()
	if err != nil {
		t.Fatalf("git show %s: %v: %s", object, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Each turn's snapshot travels as a bundle of what the previous one lacks.
// Fetched oldest first into an empty store, the chain gives back every
// snapshot; a link without the one before it is refused.
//
// A user's fetch.prune=true would otherwise delete, on each later link, every
// snapshot that link doesn't carry, so the chain is fetched with it set.
func TestBundleChainCarriesSnapshotsToAnotherStore(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "fetch.prune")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	ctx := context.Background()
	repo := newRepo(t)
	gitDir := filepath.Join(repo, ".git")
	write(t, repo, "notes.txt", "turn one\n")
	first, second := Ref("s1", "turn-2"), Ref("s1", "turn-3")
	if err := Capture(ctx, repo, first); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "notes.txt", "turn two\n")
	if err := Capture(ctx, repo, second); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "base.bundle")
	prerequisites, err := BundleChain(ctx, gitDir, first, "", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(prerequisites) != 0 {
		t.Fatalf("a base needs %v, want nothing", prerequisites)
	}
	link := filepath.Join(t.TempDir(), "link.bundle")
	if prerequisites, err = BundleChain(ctx, gitDir, second, first, link); err != nil {
		t.Fatal(err)
	}
	if len(prerequisites) == 0 {
		t.Fatal("a link needs nothing; it should build on the base")
	}

	store := bareStore(t)
	if err := FetchChain(ctx, store, []string{base, link}); err != nil {
		t.Fatalf("FetchChain: %v", err)
	}
	if got := showIn(t, store, second+":notes.txt"); got != "turn two" {
		t.Fatalf("second snapshot's notes.txt = %q", got)
	}
	if got := showIn(t, store, first+":notes.txt"); got != "turn one" {
		t.Fatalf("first snapshot's notes.txt = %q", got)
	}

	broken := bareStore(t)
	if err := FetchChain(ctx, broken, []string{link}); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("FetchChain of a link without its base = %v, want ErrChainBroken", err)
	}
	if refs, _ := exec.Command("git", "--git-dir", broken, "for-each-ref").CombinedOutput(); strings.TrimSpace(string(refs)) != "" {
		t.Fatalf("a refused link left refs: %s", refs)
	}
}

// A plain folder's detached store bundles the same way.
func TestBundleChainFromADetachedStore(t *testing.T) {
	ctx := context.Background()
	folder, store := plainFolder(t)
	write(t, folder, "notes.md", "plain\n")
	ref := Ref("s1", "turn-2")
	if err := Capture(ctx, folder, ref); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "base.bundle")
	if _, err := BundleChain(ctx, store, ref, "", out); err != nil {
		t.Fatal(err)
	}
	dst := bareStore(t)
	if err := FetchChain(ctx, dst, []string{out}); err != nil {
		t.Fatal(err)
	}
	if got := showIn(t, dst, ref+":notes.md"); got != "plain" {
		t.Fatalf("notes.md = %q", got)
	}
}

func TestBundleChainOnlyHandlesSnapshotRefs(t *testing.T) {
	repo := newRepo(t)
	out := filepath.Join(t.TempDir(), "b.bundle")
	if _, err := BundleChain(context.Background(), filepath.Join(repo, ".git"), "refs/heads/main", "", out); err == nil {
		t.Fatal("BundleChain of a branch succeeded")
	}
}
