package storagemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFindOffloadTargetsOnlyIgnoredDependencyFolders(t *testing.T) {
	dir := t.TempDir()
	newRepoWorktree(t, dir)
	targets, err := findOffloadTargets(context.Background(), dir, []string{"out-custom", ".ao", "notes", "../escape"})
	if err != nil {
		t.Fatalf("find targets: %v", err)
	}
	var rels []string
	for _, target := range targets {
		rels = append(rels, target.Rel)
		if target.Bytes <= 0 {
			t.Fatalf("target %s has no size", target.Rel)
		}
	}
	want := []string{"dist", "node_modules", "out-custom", "packages/x/node_modules"}
	if !reflect.DeepEqual(rels, want) {
		t.Fatalf("targets = %v, want %v (tracked build/, untracked notes/, .env and .ao/ must be left alone)", rels, want)
	}
}

func TestOffloadWorktreeRemovesOnlyTargetsAndRecordsFacts(t *testing.T) {
	store := newFakeStore()
	svc, layout := newTestService(t, store, nil, nil, nil)
	worktree := filepath.Join(layout.Worktrees(), "p", "p-1")
	newRepoWorktree(t, worktree)
	project := domain.ProjectRecord{ID: "p", Config: domain.ProjectConfig{StorageOffloadPaths: []string{"out-custom"}}}

	facts, err := svc.offloadWorktree(context.Background(), project, worktree)
	if err != nil {
		t.Fatalf("offload: %v", err)
	}
	if len(facts.OffloadedPaths) != 4 || facts.OffloadedBytes <= 0 {
		t.Fatalf("facts = %+v", facts)
	}
	for _, gone := range []string{"node_modules", "dist", "out-custom", "packages/x/node_modules"} {
		if pathExists(filepath.Join(worktree, filepath.FromSlash(gone))) {
			t.Fatalf("%s should be offloaded", gone)
		}
	}
	for _, kept := range []string{"build/tracked.txt", ".env", ".ao/state.json", "notes/todo.txt", "packages/x/index.js", "package-lock.json"} {
		if !pathExists(filepath.Join(worktree, filepath.FromSlash(kept))) {
			t.Fatalf("%s must be kept", kept)
		}
	}
}

func TestOffloadRefusesWorktreeOutsideManagedRoot(t *testing.T) {
	svc, _ := newTestService(t, newFakeStore(), nil, nil, nil)
	outside := t.TempDir()
	if _, err := svc.offloadWorktree(context.Background(), domain.ProjectRecord{ID: "p"}, outside); err == nil {
		t.Fatal("offload outside the managed worktrees must be refused")
	}
}

func TestRestoreDependenciesRunsSetupOrReportsMissingScript(t *testing.T) {
	store := newFakeStore()
	svc, layout := newTestService(t, store, nil, nil, nil)
	worktree := filepath.Join(layout.Worktrees(), "p", "p-1")
	writeFile(t, filepath.Join(worktree, "package.json"), "{}")
	rec := domain.SessionRecord{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{WorkspacePath: worktree}}
	ctx := context.Background()

	// Never offloaded: nothing to do.
	if needs, finish, err := svc.RestoreDependencies(ctx, domain.ProjectRecord{ID: "p"}, rec); needs || finish != nil || err != nil {
		t.Fatalf("not offloaded: needs=%v finish=%v err=%v", needs, finish != nil, err)
	}

	store.facts["p-1"] = domain.SessionStorageFacts{SessionID: "p-1", OffloadedAt: time.Now().Add(-time.Hour), OffloadedPaths: []string{"node_modules"}}
	needs, finish, err := svc.RestoreDependencies(ctx, domain.ProjectRecord{ID: "p"}, rec)
	if err != nil || needs || finish == nil {
		t.Fatalf("no setup script: needs=%v finish=%v err=%v", needs, finish != nil, err)
	}
	finish(nil)
	if facts := store.facts["p-1"]; !facts.Offloaded() || !strings.Contains(facts.RestoreError, "no Setup script") {
		t.Fatalf("facts = %+v, want the missing Setup reported", facts)
	}

	withSetup := domain.ProjectRecord{ID: "p", Config: domain.ProjectConfig{PostCreate: []string{"npm ci"}}}
	needs, finish, err = svc.RestoreDependencies(ctx, withSetup, rec)
	if err != nil || !needs || finish == nil {
		t.Fatalf("with setup: needs=%v finish=%v err=%v", needs, finish != nil, err)
	}
	finish(errors.New("npm failed"))
	if facts := store.facts["p-1"]; !facts.Offloaded() || facts.RestoreError != "npm failed" {
		t.Fatalf("failed setup facts = %+v", facts)
	}
	_, finish, _ = svc.RestoreDependencies(ctx, withSetup, rec)
	finish(nil)
	if facts := store.facts["p-1"]; facts.Offloaded() || facts.RestoreError != "" {
		t.Fatalf("restored facts = %+v", facts)
	}
}

func TestOffloadAndRestoreThroughTemplateWhenCloneSupported(t *testing.T) {
	store := newFakeStore()
	svc, layout := newTestService(t, store, nil, nil, nil)
	if !svc.cloneSupported() {
		t.Skip("this filesystem does not clone copy-on-write")
	}
	worktree := filepath.Join(layout.Worktrees(), "p", "p-1")
	newRepoWorktree(t, worktree)
	project := domain.ProjectRecord{ID: "p", Config: domain.ProjectConfig{PostCreate: []string{"npm ci"}}}
	ctx := context.Background()

	facts, err := svc.offloadWorktree(ctx, project, worktree)
	if err != nil {
		t.Fatalf("offload: %v", err)
	}
	if facts.TemplateKeys["node_modules"] == "" {
		t.Fatalf("node_modules with a lockfile should become a template: %+v", facts)
	}
	facts.SessionID, facts.OffloadedAt = "p-1", time.Now().Add(-time.Hour)
	store.facts["p-1"] = facts

	rec := domain.SessionRecord{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{WorkspacePath: worktree}}
	_, finish, err := svc.RestoreDependencies(ctx, project, rec)
	if err != nil || finish == nil {
		t.Fatalf("restore: finish=%v err=%v", finish != nil, err)
	}
	content, readErr := os.ReadFile(filepath.Join(worktree, "node_modules", "dep", "index.js"))
	if readErr != nil || string(content) != "dependency code" {
		t.Fatalf("cloned node_modules content=%q err=%v", content, readErr)
	}
}

func TestWithinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on this Windows host")
		}
		t.Fatal(err)
	}
	if Within(root, filepath.Join(root, "link", "file")) {
		t.Fatal("a path through a symlink out of root must not count as inside")
	}
	if !Within(root, filepath.Join(root, "child")) {
		t.Fatal("a child path must count as inside")
	}
	if Within(root, root) {
		t.Fatal("root itself must not be a removable target")
	}
}

func TestIsProtected(t *testing.T) {
	for _, rel := range []string{".env", "config/.env.local", ".git", "a/.ssh/keys", "certs/server.pem", "tls/server.key", ".ao/run", ".claude"} {
		if !isProtected(rel) {
			t.Fatalf("%s must be protected", rel)
		}
	}
	for _, rel := range []string{"node_modules", "frontend/dist", "target"} {
		if isProtected(rel) {
			t.Fatalf("%s must not be protected", rel)
		}
	}
}

func TestLockfileHashDependsOnLockfileAndRuntime(t *testing.T) {
	dir := t.TempDir()
	if lockfileHash(dir, "v24") != "" {
		t.Fatal("no lockfile must give no hash")
	}
	writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "a")
	first := lockfileHash(dir, "v24")
	if first == "" || first == lockfileHash(dir, "v22") {
		t.Fatal("hash must change with the Node major version")
	}
	writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "b")
	if lockfileHash(dir, "v24") == first {
		t.Fatal("hash must change with the lockfile")
	}
}
