package forkseed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/gitbundle"
)

// pushedSource is a worktree cloned from a bare origin, with one pushed
// commit, one unpushed commit, an uncommitted edit and a new file.
func pushedSource(t *testing.T) (origin, src, pushed string) {
	t.Helper()
	origin = filepath.Join(t.TempDir(), "origin.git")
	git(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
	src = filepath.Join(t.TempDir(), "src")
	git(t, filepath.Dir(src), "clone", "-q", origin, src)
	write(t, filepath.Join(src, "a.txt"), "one\n")
	git(t, src, "add", ".")
	git(t, src, "commit", "-qm", "pushed")
	git(t, src, "push", "-q", "origin", "HEAD:main")
	pushed = git(t, src, "rev-parse", "HEAD")
	write(t, filepath.Join(src, "b.txt"), "unpushed\n")
	git(t, src, "add", ".")
	git(t, src, "commit", "-qm", "unpushed")
	write(t, filepath.Join(src, "a.txt"), "edited\n")
	write(t, filepath.Join(src, "new.txt"), "new\n")
	return origin, src, pushed
}

// A bundle holds only what the remote lacks: the unpushed commit and the
// seed. A fresh clone of the remote takes it, checks out the source's HEAD
// and gets every file the source had.
func TestBundleCarriesUnpushedWorkToAFreshClone(t *testing.T) {
	ctx := context.Background()
	origin, src, pushed := pushedSource(t)
	ref := RefPrefix + "s1"
	head, err := Capture(ctx, src, ref)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "seed.bundle")
	prerequisites, err := Bundle(ctx, src, ref, out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prerequisites, []string{pushed}) {
		t.Fatalf("prerequisites = %v, want the pushed commit %s", prerequisites, pushed)
	}
	if fromHeader, err := gitbundle.Prerequisites(out); err != nil || !reflect.DeepEqual(fromHeader, prerequisites) {
		t.Fatalf("bundle header prerequisites = %v, %v", fromHeader, err)
	}

	fork := filepath.Join(t.TempDir(), "fork")
	git(t, filepath.Dir(fork), "clone", "-q", origin, fork)
	if err := Unbundle(ctx, fork, out, ref); err != nil {
		t.Fatalf("Unbundle: %v", err)
	}
	git(t, fork, "checkout", "-q", "-b", "fork", head)
	if err := Apply(ctx, fork, ref); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for name, want := range map[string]string{"a.txt": "edited\n", "b.txt": "unpushed\n", "new.txt": "new\n"} {
		if got := read(t, filepath.Join(fork, name)); got != want {
			t.Errorf("fork %s = %q, want %q", name, got, want)
		}
	}
}

// A repository with no remote, such as a standalone agent's, bundles its
// whole history, so an empty repository can take it.
func TestBundleWithoutRemotesIsSelfContained(t *testing.T) {
	ctx := context.Background()
	src := sourceRepo(t)
	ref := RefPrefix + "s1"
	if _, err := Capture(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "seed.bundle")
	prerequisites, err := Bundle(ctx, src, ref, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(prerequisites) != 0 {
		t.Fatalf("prerequisites = %v, want none", prerequisites)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	git(t, filepath.Dir(empty), "init", "-q", empty)
	if err := Unbundle(ctx, empty, out, ref); err != nil {
		t.Fatalf("Unbundle into an empty repository: %v", err)
	}
}

func TestUnbundleRefusesAMissingPrerequisite(t *testing.T) {
	ctx := context.Background()
	_, src, _ := pushedSource(t)
	ref := RefPrefix + "s1"
	if _, err := Capture(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "seed.bundle")
	if _, err := Bundle(ctx, src, ref, out); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	git(t, filepath.Dir(empty), "init", "-q", empty)
	if err := Unbundle(ctx, empty, out, ref); !errors.Is(err, ErrPrerequisiteMissing) {
		t.Fatalf("Unbundle without the pushed history = %v, want ErrPrerequisiteMissing", err)
	}
	if refs := git(t, empty, "for-each-ref", RefPrefix); refs != "" {
		t.Fatalf("a refused unbundle left refs: %q", refs)
	}
}

func TestBundleAndUnbundleOnlyHandleSeedRefs(t *testing.T) {
	ctx := context.Background()
	src := sourceRepo(t)
	out := filepath.Join(t.TempDir(), "seed.bundle")
	if _, err := Bundle(ctx, src, "refs/heads/main", out); err == nil {
		t.Fatal("Bundle of a branch succeeded")
	}
	if err := Unbundle(ctx, src, out, "refs/heads/main"); err == nil {
		t.Fatal("Unbundle into a branch succeeded")
	}
}

// A relative bundle path names the file in the caller's folder, for git and
// for reading the bundle's header alike.
func TestBundleResolvesRelativePaths(t *testing.T) {
	ctx := context.Background()
	src := sourceRepo(t)
	ref := RefPrefix + "s1"
	if _, err := Capture(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Chdir(work)
	if _, err := Bundle(ctx, src, ref, "seed.bundle"); err != nil {
		t.Fatalf("Bundle with a relative path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "seed.bundle")); err != nil {
		t.Fatalf("the bundle isn't in the caller's folder: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	git(t, filepath.Dir(empty), "init", "-q", empty)
	if err := Unbundle(ctx, empty, "seed.bundle", ref); err != nil {
		t.Fatalf("Unbundle with a relative path: %v", err)
	}
}
