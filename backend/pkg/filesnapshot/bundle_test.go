package filesnapshot

import (
	"context"
	"errors"
	"os"
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

// Paths relative to the caller's folder name the same files they would
// anywhere else, though git runs from a folder of its own.
func TestBundleChainResolvesRelativePaths(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, repo, "notes.txt", "relative\n")
	ref := Ref("s1", "turn-2")
	if err := Capture(ctx, repo, ref); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Chdir(work)
	src, err := filepath.Rel(work, filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BundleChain(ctx, src, ref, "", "base.bundle"); err != nil {
		t.Fatalf("BundleChain with relative paths: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "base.bundle")); err != nil {
		t.Fatalf("the bundle isn't in the caller's folder: %v", err)
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", "dst.git").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := FetchChain(ctx, "dst.git", []string{"base.bundle"}); err != nil {
		t.Fatalf("FetchChain with relative paths: %v", err)
	}
	if got := showIn(t, filepath.Join(work, "dst.git"), ref+":notes.txt"); got != "relative" {
		t.Fatalf("notes.txt = %q", got)
	}
}

// An object directory inherited from the process's environment must not
// take the fetched objects somewhere other than the store.
func TestFetchChainIgnoresAnInheritedObjectDirectory(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, repo, "notes.txt", "kept\n")
	ref := Ref("s1", "turn-2")
	if err := Capture(ctx, repo, ref); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "base.bundle")
	if _, err := BundleChain(ctx, filepath.Join(repo, ".git"), ref, "", out); err != nil {
		t.Fatal(err)
	}
	store := bareStore(t)
	elsewhere := t.TempDir()
	t.Setenv("GIT_OBJECT_DIRECTORY", elsewhere)
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", elsewhere)
	if err := FetchChain(ctx, store, []string{out}); err != nil {
		t.Fatalf("FetchChain: %v", err)
	}
	if err := os.Unsetenv("GIT_OBJECT_DIRECTORY"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("GIT_ALTERNATE_OBJECT_DIRECTORIES"); err != nil {
		t.Fatal(err)
	}
	if got := showIn(t, store, ref+":notes.txt"); got != "kept" {
		t.Fatalf("notes.txt = %q; the objects went elsewhere", got)
	}
}

// Only a missing earlier link is a broken chain. A store that isn't a
// repository, or a cancelled request, keeps its own error.
func TestFetchChainReportsOtherFailuresAsThemselves(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	first, second := Ref("s1", "turn-2"), Ref("s1", "turn-3")
	if err := Capture(ctx, repo, first); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "notes.txt", "later\n")
	if err := Capture(ctx, repo, second); err != nil {
		t.Fatal(err)
	}
	// A link needs the commits of the link before it, so its check runs.
	out := filepath.Join(t.TempDir(), "link.bundle")
	if prerequisites, err := BundleChain(ctx, filepath.Join(repo, ".git"), second, first, out); err != nil || len(prerequisites) == 0 {
		t.Fatalf("BundleChain = %v, %v; want a link with prerequisites", prerequisites, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.git")
	if err := FetchChain(ctx, missing, []string{out}); err == nil || errors.Is(err, ErrChainBroken) {
		t.Fatalf("FetchChain into a store that isn't a repository = %v, want its own error", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := FetchChain(cancelled, bareStore(t), []string{out}); !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchChain with a cancelled context = %v, want context.Canceled", err)
	}
}
