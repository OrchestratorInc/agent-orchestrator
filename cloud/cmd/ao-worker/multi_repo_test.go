package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// The agent's start folder is fixed at the session's first launch: a later
// change to the project's setting applies to new sessions only, so Claude can
// still resume the conversation it keeps under that folder.
func TestPinAgentStartsInKeepsTheFirstLaunchFolder(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "work", "alice")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	launch := worker.LaunchContext{
		Kind: "worker", RepositoryURL: "https://github.com/acme/alice",
		ExtraRepos:    []worker.RepoRef{{URL: "https://github.com/acme/julian"}},
		AgentStartsIn: domain.ProjectAgentStartsInParent,
	}
	if got := pinAgentStartsIn(discardLogger(), dataDir, launch, workspace); got != domain.ProjectAgentStartsInParent {
		t.Fatalf("first launch = %q", got)
	}
	launch.AgentStartsIn = domain.ProjectAgentStartsInRepository
	if got := pinAgentStartsIn(discardLogger(), dataDir, launch, workspace); got != domain.ProjectAgentStartsInParent {
		t.Fatalf("after the setting changed = %q, want the recorded parent folder", got)
	}
	fresh := t.TempDir()
	if got := pinAgentStartsIn(discardLogger(), fresh, launch, workspace); got != domain.ProjectAgentStartsInRepository {
		t.Fatalf("a fresh sandbox = %q, want the project's current setting", got)
	}
}

// Uncommitted work in an additional repository survives a delete and restore
// through refs/ao/preserved/<session> on that repository's own origin, and a
// clean checkout drops the ref so a restore never replays stale work.
func TestSiblingWorkIsPreservedAndRestored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	git := worker.ExecGitRunner{}
	root := t.TempDir()
	origin := filepath.Join(root, "julian.git")
	// Name the bare repository's default branch, so clones check out the
	// pushed main branch whatever init.defaultBranch the machine has.
	mustGit(t, ctx, git, root, "init", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	mustGit(t, ctx, git, root, "clone", origin, seed)
	configIdentity(t, ctx, git, seed)
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, ctx, git, seed, "add", "-A")
	mustGit(t, ctx, git, seed, "commit", "-m", "base")
	mustGit(t, ctx, git, seed, "push", "origin", "HEAD:refs/heads/main")

	sibling := filepath.Join(root, "sandbox", "julian")
	mustGit(t, ctx, git, root, "clone", origin, sibling)
	configIdentity(t, ctx, git, sibling)
	if err := os.WriteFile(filepath.Join(sibling, "a.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cp := &checkpointer{git: git, sessionID: "sess-1", logger: discardLogger(),
		siblings: []string{sibling}, siblingTrees: map[string]*string{}}
	cp.preserveSiblingWork(ctx)
	ref := preservedRefPrefix + "sess-1"
	if out, err := git.Run(ctx, origin, nil, "rev-parse", "--verify", ref); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("the sibling's work was not pushed: %v", err)
	}

	restored := filepath.Join(root, "restored", "julian")
	mustGit(t, ctx, git, root, "clone", origin, restored)
	configIdentity(t, ctx, git, restored)
	if err := applyPreservedRef(ctx, git, restored, ref); err != nil {
		t.Fatalf("applyPreservedRef: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(restored, "a.txt")); string(got) != "edited\n" {
		t.Fatalf("restored %q", got)
	}

	mustGit(t, ctx, git, sibling, "checkout", "--", "a.txt")
	cp.preserveSiblingWork(ctx)
	if _, err := git.Run(ctx, origin, nil, "rev-parse", "--verify", "--quiet", ref); err == nil {
		t.Fatal("a clean sibling kept its stale preserved ref")
	}
}
