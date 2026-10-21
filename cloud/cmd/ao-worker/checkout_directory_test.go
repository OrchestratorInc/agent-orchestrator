package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// A working folder the template already created stops the session with its
// own startup error, naming the folder and both ways out; one AO cannot
// prepare is reported as unusable, pointing at the project setting.
func TestWorkingFolderCheckoutFailuresHaveTheirOwnStartupErrors(t *testing.T) {
	occupied := checkoutStartupFailure(fmt.Errorf("%w: ~/work/alice", worker.ErrWorkingDirectoryOccupied))
	want := "~/work/alice already exists (created by your Coder template). " +
		"Untick it in the project's Template options, or turn on 'Run the agent as the workspace user' to reuse it. " +
		"Start a new session after changing it."
	if occupied.Code != domain.WorkspaceStartupErrorWorkingDirectoryOccupied || occupied.Status != worker.WorkspaceStartupFailed ||
		occupied.Message != want {
		t.Fatalf("occupied failure = %+v", occupied)
	}
	unusable := checkoutStartupFailure(fmt.Errorf("%w: ~/work/alice does not exist and AO could not create it: permission denied",
		worker.ErrCheckoutDirectoryUnusable))
	if unusable.Code != domain.WorkspaceStartupErrorWorkingDirectory ||
		!strings.Contains(unusable.Message, "this project's working folder: ~/work/alice does not exist") ||
		!strings.Contains(unusable.Message, "Project settings > Repositories") {
		t.Fatalf("unusable failure = %+v", unusable)
	}
}

// The worker reads the working-folder checkout once, keeps it out of the
// agent's environment, and settles the session on the folder only when it is
// AO's own.
func TestResolveCheckoutDirectoryUsesOnlyAOsOwnFolder(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "work", "alice")
	t.Setenv(worker.CheckoutDirectoryEnv, dir)
	t.Setenv(worker.UserHomeEnv, home)
	t.Setenv("AO_WORKSPACE_DIR", "")
	if got := readCheckoutDirectory(); got != dir {
		t.Fatalf("checkout directory = %q", got)
	}
	if _, present := os.LookupEnv(worker.CheckoutDirectoryEnv); present {
		t.Fatal("the checkout directory stays in the environment the agent inherits")
	}
	if err := resolveCheckoutDirectory(discardLogger(), dir); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AO_WORKSPACE_DIR") != dir {
		t.Fatalf("AO_WORKSPACE_DIR = %q", os.Getenv("AO_WORKSPACE_DIR"))
	}
	if err := os.WriteFile(filepath.Join(home, "work", "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := resolveCheckoutDirectory(discardLogger(), filepath.Join(home, "work", "file"))
	if !errors.Is(err, worker.ErrWorkingDirectoryOccupied) || !strings.HasSuffix(err.Error(), "~/work/file") {
		t.Fatalf("a folder that is not AO's = %v", err)
	}
}

// Additional repositories are cloned beside a working-folder checkout
// (~/work/<name>), into the folders the bootstrap prepared, and nothing is
// created under the durable root's repository folder.
func TestExtraReposAreClonedBesideTheWorkingFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	git := worker.ExecGitRunner{}
	root := t.TempDir()
	origin := filepath.Join(root, "remotes", "billing.git")
	mustGit(t, ctx, git, root, "init", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	mustGit(t, ctx, git, root, "clone", origin, seed)
	configIdentity(t, ctx, git, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("billing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, ctx, git, seed, "add", "-A")
	mustGit(t, ctx, git, seed, "commit", "-m", "base")
	mustGit(t, ctx, git, seed, "push", "origin", "HEAD:refs/heads/main")

	home := filepath.Join(root, "home", "ubuntu")
	workspace := filepath.Join(home, "work", "alice")
	// The bootstrap created the checkout and sibling folders empty.
	for _, dir := range []string{workspace, filepath.Join(home, "work", "billing")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap := worker.BootstrapResponse{SessionID: "sess-1", Launch: worker.LaunchContext{
		RepositoryURL: "https://github.com/acme/alice", Branch: "ao/sess-1",
		ExtraRepos: []worker.RepoRef{{URL: origin}},
	}}
	cloneExtraRepos(ctx, discardLogger(), "", bootstrap, workspace, filepath.Join(home, ".ao", "worker"), false)
	if _, err := os.Stat(filepath.Join(home, "work", "billing", "README.md")); err != nil {
		t.Fatalf("billing was not cloned beside the working folder: %v", err)
	}
	if branch := strings.TrimSpace(mustGit(t, ctx, git, filepath.Join(home, "work", "billing"), "symbolic-ref", "--short", "HEAD")); branch != "ao/sess-1" {
		t.Fatalf("billing branch = %q", branch)
	}
	if _, err := os.Stat(filepath.Join(home, "repository")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a durable root checkout appeared: %v", err)
	}
	scripts := worker.SessionScriptEnvironment{WorkspaceDir: workspace, ReposRoot: filepath.Dir(workspace)}.Map()
	if scripts["AO_WORKTREE_PATH"] != workspace || scripts["AO_REPOS_ROOT"] != filepath.Join(home, "work") {
		t.Fatalf("script environment = %v", scripts)
	}
}

// A working folder that is not AO's is never the session's workspace: the
// session works in an empty folder of AO's own (so its shell and agent never
// open in the template's folder), and its Cleanup hook runs no Cleanup.
func TestRefusedWorkingFolderGetsAWorkspaceOfAOsOwn(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "worker")
	t.Setenv(worker.UserHomeEnv, home)
	t.Setenv("AO_WORKSPACE_DIR", "")
	occupied := filepath.Join(home, "work", "alice")
	if err := os.MkdirAll(filepath.Dir(occupied), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), occupied); err != nil {
		t.Fatal(err)
	}
	workspace, err := settleCheckoutDirectory(discardLogger(), occupied, dataDir)
	if !errors.Is(err, worker.ErrWorkingDirectoryOccupied) {
		t.Fatalf("error = %v", err)
	}
	if want := filepath.Join(dataDir, refusedCheckoutWorkspace); workspace != want || os.Getenv("AO_WORKSPACE_DIR") != want {
		t.Fatalf("workspace = %q, AO_WORKSPACE_DIR = %q, want %q", workspace, os.Getenv("AO_WORKSPACE_DIR"), want)
	}
	if err := worker.WriteSkippedCleanupHook(dataDir, worker.CleanupSkipNoWorkingFolder); err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(filepath.Join(dataDir, worker.CleanupHookFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hook), occupied) || strings.Contains(string(hook), "cd ") ||
		!strings.Contains(string(hook), worker.CleanupHookSkippedPrefix+worker.CleanupSkipNoWorkingFolder) {
		t.Fatalf("hook = %q", hook)
	}
	owned := filepath.Join(home, "work", "bob")
	if workspace, err := settleCheckoutDirectory(discardLogger(), owned, dataDir); err != nil || workspace != owned {
		t.Fatalf("AO's own folder = %q, %v", workspace, err)
	}
}

// Beside a working folder, an additional repository's folder the template
// already made (its own clone) is refused like the main folder: the worker
// reports the repository as skipped when it starts, never checks it out
// there, and marks the session so its Setup and Cleanup steps for it are
// skipped. A session that is not a dedicated-user working folder clears the
// mark.
func TestAWorkingFoldersRepositoryFolderTheTemplateMadeIsSkipped(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home", "ubuntu")
	workspace := filepath.Join(home, "work", "alice")
	templateClone := filepath.Join(root, "template-billing")
	for _, dir := range []string{workspace, templateClone} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(templateClone, filepath.Join(home, "work", "billing")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(worker.UserHomeEnv, home)
	dataDir := filepath.Join(root, "data")
	bootstrap := worker.BootstrapResponse{SessionID: "sess-1", Launch: worker.LaunchContext{
		RepositoryURL: "https://github.com/acme/alice", Branch: "ao/sess-1",
		// A local origin that does not exist, so a clone attempt fails fast.
		ExtraRepos: []worker.RepoRef{{URL: filepath.Join(root, "remotes", "billing")}},
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	recordOwnedRepoFolders(logger, dataDir, true, bootstrap.Launch, workspace)
	if !strings.Contains(logs.String(), "multi-repo: additional repository skipped: ~/work/billing already exists (created by your Coder template)") ||
		!worker.OwnedRepoFoldersOnly(dataDir) {
		t.Fatalf("logs = %s, marked = %v", logs.String(), worker.OwnedRepoFoldersOnly(dataDir))
	}
	logs.Reset()
	cloneExtraRepos(context.Background(), logger, "", bootstrap, workspace, dataDir, false)
	if strings.Contains(logs.String(), "unavailable") {
		t.Fatalf("the worker tried to check billing out in the template's folder: %s", logs.String())
	}
	if entries, _ := os.ReadDir(templateClone); len(entries) != 0 {
		t.Fatalf("the template's folder changed: %v", entries)
	}
	recordOwnedRepoFolders(logger, dataDir, false, bootstrap.Launch, workspace)
	if worker.OwnedRepoFoldersOnly(dataDir) {
		t.Fatal("a session that is not a working folder kept the mark")
	}
}

// A refused additional repository folder (the template's own clone) is not
// one of the session's: the checkpointer neither preserves nor restores work
// in it, and the agent CLI is not told it is a session repository. Without
// the owned-folders mark (a workspace-user session) it still is.
func TestARefusedRepositoryFolderIsNotCheckpointedOrAdvertised(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "work", "alice")
	templateClone := filepath.Join(root, "template-billing")
	for _, dir := range []string{workspace, templateClone, filepath.Join(root, "work", "api")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(templateClone, filepath.Join(root, "work", "billing")); err != nil {
		t.Fatal(err)
	}
	launch := worker.LaunchContext{RepositoryURL: "https://github.com/acme/alice", ExtraRepos: []worker.RepoRef{
		{URL: "https://github.com/acme/billing"}, {URL: "https://github.com/acme/api"},
	}}
	dataDir := filepath.Join(root, "data")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	recordOwnedRepoFolders(logger, dataDir, true, launch, workspace)
	if got, want := siblingCheckoutPaths(launch, workspace, dataDir), []string{filepath.Join(root, "work", "api")}; !slices.Equal(got, want) {
		t.Errorf("checkpointed siblings = %q, want %q", got, want)
	}
	if got := sessionRepositories(launch, workspace, dataDir); got != "acme/alice,acme/api" {
		t.Errorf("session repositories = %q", got)
	}

	recordOwnedRepoFolders(logger, dataDir, false, launch, workspace)
	if got := siblingCheckoutPaths(launch, workspace, dataDir); len(got) != 2 {
		t.Errorf("checkpointed siblings without the mark = %q", got)
	}
	if got := sessionRepositories(launch, workspace, dataDir); got != "acme/alice,acme/billing,acme/api" {
		t.Errorf("session repositories without the mark = %q", got)
	}
}

// A terminal agent that could not start after Setup is reported with its own
// code, a message naming the Retry, and what failed as the detail.
func TestAgentStartFailureIsAWorkspaceStartupError(t *testing.T) {
	event := agentStartFailure(errors.New("load coding-agent credential: unavailable"))
	if event.Status != worker.WorkspaceStartupFailed || event.Code != domain.WorkspaceStartupErrorAgentStart ||
		!strings.Contains(event.Message, "Retry") || !strings.Contains(event.Detail, "credential") {
		t.Fatalf("agent start failure = %+v", event)
	}
}
