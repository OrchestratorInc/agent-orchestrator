package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeRemoteURL(t *testing.T) {
	t.Parallel()
	same := []string{
		"https://github.com/Acme/App.git",
		"https://github.com/acme/app",
		"https://github.com/acme/app/",
		"https://x-access-token:secret@github.com/acme/app.git",
		"http://github.com:443/acme/app",
		"git@github.com:acme/app.git",
		"git@GitHub.com:Acme/App",
		"ssh://git@github.com/acme/app.git",
		"ssh://git@github.com:22/acme/app",
		"github.com:acme/app.git",
	}
	for _, remote := range same {
		if got := NormalizeRemoteURL(remote); got != "github.com/acme/app" {
			t.Errorf("NormalizeRemoteURL(%q) = %q", remote, got)
		}
	}
	for _, remote := range []string{"", "app", "/srv/git/app.git", "https://github.com/", "git@github.com:"} {
		if got := NormalizeRemoteURL(remote); got != "" {
			t.Errorf("NormalizeRemoteURL(%q) = %q, want empty", remote, got)
		}
	}
	if NormalizeRemoteURL("https://github.com/acme/app") == NormalizeRemoteURL("https://github.com/acme/app-ui") ||
		NormalizeRemoteURL("https://github.com/acme/app") == NormalizeRemoteURL("https://gitlab.com/acme/app") {
		t.Fatal("different repositories normalized equal")
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
}

// upstreamRepository is a bare repository with one commit on main.
func upstreamRepository(t *testing.T) string {
	t.Helper()
	seed := initReviewBaseRepository(t)
	bare := filepath.Join(t.TempDir(), "upstream.git")
	gitRun(t, seed, "clone", "--bare", seed, bare)
	return bare
}

// templateCheckout clones upstream into dir, the way a workspace template
// does, and points origin at remote.
func templateCheckout(t *testing.T, upstream, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, filepath.Dir(dir), "clone", "--quiet", upstream, dir)
	gitRun(t, dir, "config", "user.name", "Template")
	gitRun(t, dir, "config", "user.email", "template@example.test")
	gitRun(t, dir, "remote", "set-url", "origin", remote)
}

// commitAndPush commits one new file on top of clone's HEAD and pushes it to
// branch on origin, returning the pushed commit.
func commitAndPush(t *testing.T, clone, branch, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(clone, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "add", file)
	gitRun(t, clone, "commit", "-qm", file)
	gitRun(t, clone, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	return gitOutput(t, clone, "rev-parse", "HEAD")
}

func TestFindExistingCheckoutMatchesNormalizedOrigin(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	templateCheckout(t, upstream, filepath.Join(home, "work", "other"), "https://github.com/acme/other.git")
	templateCheckout(t, upstream, filepath.Join(home, "work", "app"), "git@github.com:Acme/App.git")
	templateCheckout(t, upstream, filepath.Join(home, "app-copy"), "https://github.com/acme/app")
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	found, ok := FindExistingCheckout(context.Background(), ExecGitRunner{}, home, "https://github.com/acme/app")
	if !ok || found != filepath.Join(home, "work", "app") {
		t.Fatalf("found = %q, %v; want the $HOME/work checkout first", found, ok)
	}
	found, ok = FindExistingCheckout(context.Background(), ExecGitRunner{}, home, "https://github.com/acme/app",
		filepath.Join(home, "work", "app"))
	if !ok || found != filepath.Join(home, "app-copy") {
		t.Fatalf("found = %q, %v; want the one-level $HOME checkout", found, ok)
	}
	if found, ok := FindExistingCheckout(context.Background(), ExecGitRunner{}, home, "https://github.com/acme/missing"); ok {
		t.Fatalf("found %q for a repository nobody cloned", found)
	}
}

// A clean checkout is adopted: it moves onto a new session branch from
// origin/<default>, its git configuration is untouched, and a restarted
// worker keeps using it without switching again.
func TestResolveReusedCheckoutAdoptsCleanCheckout(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")
	gitRun(t, checkout, "checkout", "-q", "-b", "feature/template")
	configBefore, err := os.ReadFile(filepath.Join(checkout, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	defaultWorkspace := filepath.Join(home, "repository")
	dataDir := filepath.Join(home, ".ao", "worker")

	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home, defaultWorkspace, dataDir,
		"https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if !decision.Reused || !decision.AdoptedNow || decision.Workspace != checkout || decision.Stash != "" {
		t.Fatalf("decision = %+v", decision)
	}
	if branch := gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"); branch != "ao/session-1" {
		t.Fatalf("branch = %q", branch)
	}
	if head, base := gitOutput(t, checkout, "rev-parse", "HEAD"), gitOutput(t, checkout, "rev-parse", "origin/main"); head != base {
		t.Fatalf("session branch %s is not based on origin/main %s", head, base)
	}
	if upstreamBranch, err := exec.Command("git", "-C", checkout, "rev-parse", "--abbrev-ref", "@{upstream}").Output(); err == nil {
		t.Fatalf("session branch tracks %s; want no upstream", upstreamBranch)
	}
	configAfter, err := os.ReadFile(filepath.Join(checkout, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Fatalf("adoption rewrote the checkout's git config:\n%s", configAfter)
	}

	restart := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home, defaultWorkspace, dataDir,
		"https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if !restart.Reused || restart.AdoptedNow || restart.Workspace != checkout {
		t.Fatalf("restart decision = %+v", restart)
	}
}

// A session continuing a branch that already exists on the remote is adopted
// onto that branch's commits, not onto a new branch from origin/<default>.
func TestResolveReusedCheckoutContinuesRemoteBranch(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	pusher := filepath.Join(home, "pusher")
	templateCheckout(t, upstream, pusher, upstream)
	pushed := commitAndPush(t, pusher, "feature/x", "FEATURE.md")
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")
	configBefore, err := os.ReadFile(filepath.Join(checkout, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}

	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home,
		filepath.Join(home, "repository"), filepath.Join(home, ".ao", "worker"),
		"https://github.com/acme/app", "session-1", "feature/x", "main")
	if !decision.Reused || !decision.AdoptedNow || !decision.BranchCreated || decision.Workspace != checkout {
		t.Fatalf("decision = %+v", decision)
	}
	if branch := gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"); branch != "feature/x" {
		t.Fatalf("branch = %q, want feature/x", branch)
	}
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != pushed {
		t.Fatalf("HEAD = %s, want the pushed feature/x commit %s", head, pushed)
	}
	configAfter, err := os.ReadFile(filepath.Join(checkout, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Fatalf("adoption rewrote the checkout's git config:\n%s", configAfter)
	}
}

// Uncommitted changes in the template's checkout are saved in a stash named
// after the session, never discarded.
func TestResolveReusedCheckoutStashesTrackedChanges(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app")
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "scratch.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home, filepath.Join(home, "repository"),
		filepath.Join(home, ".ao", "worker"), "https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if !decision.Reused || !strings.HasPrefix(decision.Stash, "ao-session session-1: changes found before switching to ao/session-1") {
		t.Fatalf("decision = %+v", decision)
	}
	if stashes := gitOutput(t, checkout, "stash", "list"); !strings.Contains(stashes, "ao-session session-1") {
		t.Fatalf("stash list = %q", stashes)
	}
	if saved := gitOutput(t, checkout, "show", "stash@{0}:README.md"); saved != "local edit" {
		t.Fatalf("stashed README = %q", saved)
	}
	if status := gitOutput(t, checkout, "status", "--porcelain", "--untracked-files=no"); status != "" {
		t.Fatalf("tracked changes left behind: %q", status)
	}
	if _, err := os.Stat(filepath.Join(checkout, "scratch.txt")); err != nil {
		t.Fatalf("untracked file was disturbed: %v", err)
	}
}

// A checkout whose changes cannot be stashed (here: an unresolved merge) is
// left exactly as it is and the session clones into the default workspace;
// the choice sticks across worker restarts.
func TestResolveReusedCheckoutFallsBackWhenChangesCannotBeStashed(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app")
	gitRun(t, checkout, "checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, checkout, "commit", "-qam", "side")
	gitRun(t, checkout, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, checkout, "commit", "-qam", "main")
	if err := exec.Command("git", "-C", checkout, "merge", "side").Run(); err == nil {
		t.Fatal("expected a merge conflict")
	}
	defaultWorkspace := filepath.Join(home, "repository")
	dataDir := filepath.Join(home, ".ao", "worker")

	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home, defaultWorkspace, dataDir,
		"https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if decision.Reused || decision.Workspace != defaultWorkspace || !strings.Contains(decision.Reason, "could not be stashed") {
		t.Fatalf("decision = %+v", decision)
	}
	if branch := gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"); branch != "main" {
		t.Fatalf("branch = %q, want the checkout left on main", branch)
	}
	if status := gitOutput(t, checkout, "status", "--porcelain"); !strings.Contains(status, "UU README.md") {
		t.Fatalf("the conflicted checkout was disturbed: %q", status)
	}
	again := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home, defaultWorkspace, dataDir,
		"https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if again.Reused || again.Workspace != defaultWorkspace {
		t.Fatalf("restart decision = %+v, want the recorded default workspace", again)
	}
}

func TestResolveReusedCheckoutWithoutMatchKeepsDefault(t *testing.T) {
	t.Parallel()
	requireGit(t)
	home := t.TempDir()
	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home, filepath.Join(home, "repository"),
		filepath.Join(home, ".ao", "worker"), "https://github.com/acme/app", "session-1", "ao/session-1", "main")
	if decision.Reused || decision.Workspace != filepath.Join(home, "repository") || decision.Reason == "" {
		t.Fatalf("decision = %+v", decision)
	}
}

// The refresh fetches from the authorized URL with every other credential
// helper cleared, after checking the checkout's origin is that repository.
func TestPrepareReusedCheckoutFetchesWithGrantOnly(t *testing.T) {
	t.Parallel()
	runner := &reuseRecorderRunner{origin: "git@github.com:Acme/App.git\n"}
	grant := CheckoutGrantResponse{CloneURL: "https://github.com/acme/app", Token: "ghs_token", ExpiresAt: time.Now().Add(time.Hour)}
	if err := PrepareReusedCheckout(context.Background(), runner, "/home/ubuntu/work/app", grant); err != nil {
		t.Fatal(err)
	}
	fetch := runner.commands[len(runner.commands)-1]
	want := []string{"-c", "credential.helper=", "fetch", "--prune", "--", "https://github.com/acme/app", "+refs/heads/*:refs/remotes/origin/*"}
	if strings.Join(fetch, " ") != strings.Join(want, " ") {
		t.Fatalf("fetch = %v", fetch)
	}
	if runner.env["AO_GIT_TOKEN"] != "ghs_token" || runner.env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("fetch environment = %v", runner.env)
	}
	mismatch := &reuseRecorderRunner{origin: "https://github.com/acme/other\n"}
	if err := PrepareReusedCheckout(context.Background(), mismatch, "/home/ubuntu/work/app", grant); err == nil {
		t.Fatal("a checkout of another repository was refreshed")
	}
}

type reuseRecorderRunner struct {
	origin   string
	commands [][]string
	env      map[string]string
}

func (r *reuseRecorderRunner) Run(_ context.Context, _ string, env map[string]string, args ...string) (string, error) {
	r.commands = append(r.commands, args)
	if len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
		return r.origin, nil
	}
	if env != nil {
		r.env = map[string]string{}
		for key, value := range env {
			r.env[key] = value
		}
	}
	return "", nil
}

// A just-adopted branch is fast-forwarded to the fetched base; a branch with
// its own commits is not rewritten.
func TestAdvanceAdoptedCheckoutFastForwardsOnly(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, upstream)
	gitRun(t, checkout, "checkout", "-q", "--no-track", "-b", "ao/session-1", "origin/main")

	other := filepath.Join(home, "other")
	templateCheckout(t, upstream, other, upstream)
	if err := os.WriteFile(filepath.Join(other, "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, other, "add", "NEW.md")
	gitRun(t, other, "commit", "-qm", "upstream moves on")
	gitRun(t, other, "push", "-q", "origin", "main")
	gitRun(t, checkout, "fetch", "-q", "origin")

	if err := AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "ao/session-1", "main", true, ""); err != nil {
		t.Fatal(err)
	}
	if head, base := gitOutput(t, checkout, "rev-parse", "HEAD"), gitOutput(t, checkout, "rev-parse", "origin/main"); head != base {
		t.Fatalf("HEAD %s was not fast-forwarded to origin/main %s", head, base)
	}

	if err := os.WriteFile(filepath.Join(checkout, "MINE.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, checkout, "add", "MINE.md")
	gitRun(t, checkout, "commit", "-qm", "session work")
	mine := gitOutput(t, checkout, "rev-parse", "HEAD")
	_ = AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "ao/session-1", "main", true, "")
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != mine {
		t.Fatalf("session work was rewritten: HEAD %s, want %s", head, mine)
	}
}

// When the template cloned before the session's branch reached the remote,
// adoption cuts the branch from origin/<default>; once the fetch brings the
// remote branch in, the branch adoption created moves onto it. A branch with
// work of its own only fast-forwards.
func TestAdvanceAdoptedCheckoutContinuesRemoteBranch(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	pusher := filepath.Join(home, "pusher")
	templateCheckout(t, upstream, pusher, upstream)
	gitRun(t, pusher, "checkout", "-q", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(pusher, "FEATURE.md"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, pusher, "add", "FEATURE.md")
	gitRun(t, pusher, "commit", "-qm", "feature work")
	gitRun(t, pusher, "checkout", "-q", "main")
	commitAndPush(t, pusher, "main", "MAIN.md") // main moves past feature/x's base

	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")
	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home,
		filepath.Join(home, "repository"), filepath.Join(home, ".ao", "worker"),
		"https://github.com/acme/app", "session-1", "feature/x", "main")
	if !decision.AdoptedNow || !decision.BranchCreated {
		t.Fatalf("decision = %+v", decision)
	}
	gitRun(t, pusher, "push", "-q", "origin", "feature/x")
	pushed := gitOutput(t, pusher, "rev-parse", "feature/x")
	fetch := func() { gitRun(t, checkout, "fetch", "-q", upstream, "+refs/heads/*:refs/remotes/origin/*") }
	fetch()

	if err := AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "feature/x", "main", decision.BranchCreated, decision.AdoptedAt); err != nil {
		t.Fatal(err)
	}
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != pushed {
		t.Fatalf("HEAD = %s, want the pushed feature/x commit %s", head, pushed)
	}
	if branch := gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"); branch != "feature/x" {
		t.Fatalf("branch = %q, want feature/x", branch)
	}

	if err := os.WriteFile(filepath.Join(checkout, "MINE.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, checkout, "add", "MINE.md")
	gitRun(t, checkout, "commit", "-qm", "session work")
	mine := gitOutput(t, checkout, "rev-parse", "HEAD")
	commitAndPush(t, pusher, "main", "LATER.md")
	fetch()
	_ = AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "feature/x", "main", false, "")
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != mine {
		t.Fatalf("session work was rewritten: HEAD %s, want %s", head, mine)
	}
}

// The environment-scoped git setup replaces every other credential helper
// with the session helper and sets the AO identity, without any config file.
func TestReusedCheckoutGitEnvironmentScopesHelper(t *testing.T) {
	t.Parallel()
	requireGit(t)
	repo := initReviewBaseRepository(t)
	gitRun(t, repo, "config", "credential.helper", "store")
	dataDir := t.TempDir()
	command := exec.Command("git", "config", "--get-all", "credential.helper")
	command.Dir = repo
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	for key, value := range ReusedCheckoutGitEnvironment(dataDir) {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	helpers := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if last := helpers[len(helpers)-1]; last != GitCredentialHelperPath(dataDir) {
		t.Fatalf("helpers = %q, want the session helper last", helpers)
	}
	if len(helpers) < 2 || helpers[len(helpers)-2] != "" {
		t.Fatalf("helpers = %q, want an empty reset before the session helper", helpers)
	}
	environment := ReusedCheckoutGitEnvironment(dataDir)
	if environment["GIT_CONFIG_COUNT"] != "5" || environment["GIT_CONFIG_KEY_3"] != "user.name" ||
		environment["GIT_CONFIG_VALUE_4"] != cloudGitAuthorEmail {
		t.Fatalf("environment = %v", environment)
	}
}

// A template clone with a stale origin/<branch> gets a session branch cut from
// it; when the remote branch was since rebased, the branch adoption created
// still moves onto the rebased commits.
func TestAdvanceAdoptedCheckoutFollowsRebasedRemoteBranch(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	pusher := filepath.Join(home, "pusher")
	templateCheckout(t, upstream, pusher, upstream)
	gitRun(t, pusher, "checkout", "-q", "-b", "feature/x")
	commitAndPush(t, pusher, "feature/x", "FEATURE.md")
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")

	gitRun(t, pusher, "checkout", "-q", "main")
	commitAndPush(t, pusher, "main", "MAIN.md")
	gitRun(t, pusher, "checkout", "-q", "feature/x")
	gitRun(t, pusher, "rebase", "-q", "main")
	gitRun(t, pusher, "push", "-q", "--force", "origin", "feature/x")
	rebased := gitOutput(t, pusher, "rev-parse", "HEAD")

	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home,
		filepath.Join(home, "repository"), filepath.Join(home, ".ao", "worker"),
		"https://github.com/acme/app", "session-1", "feature/x", "main")
	if !decision.BranchCreated {
		t.Fatalf("decision = %+v", decision)
	}
	gitRun(t, checkout, "fetch", "-q", upstream, "+refs/heads/*:refs/remotes/origin/*")
	if err := AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "feature/x", "main", decision.BranchCreated, decision.AdoptedAt); err != nil {
		t.Fatal(err)
	}
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != rebased {
		t.Fatalf("HEAD = %s, want the rebased feature/x commit %s", head, rebased)
	}
}

// A checkout already on the requested branch still has its tracked changes
// stashed, so the session never starts inside someone else's edits.
func TestResolveReusedCheckoutStashesChangesOnRequestedBranch(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")
	gitRun(t, checkout, "checkout", "-q", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home,
		filepath.Join(home, "repository"), filepath.Join(home, ".ao", "worker"),
		"https://github.com/acme/app", "session-1", "feature/x", "main")
	if !decision.Reused || decision.Stash == "" || decision.BranchCreated {
		t.Fatalf("decision = %+v, want a stash and the existing branch", decision)
	}
	if !strings.Contains(decision.Stash, "changes found before working on ") {
		t.Fatalf("stash message %q should say the checkout was already on the branch", decision.Stash)
	}
	if status := gitOutput(t, checkout, "status", "--porcelain", "--untracked-files=no"); status != "" {
		t.Fatalf("tracked changes left in the checkout:\n%s", status)
	}
}

// A startup retry runs the post-fetch advance again with the same adoption
// decision. A commit the user made on the adopted branch in between (their own
// checkout, the same unix user) must survive: the branch only moves onto
// origin/<branch> while it still points where adoption left it.
func TestAdvanceAdoptedCheckoutKeepsCommitsMadeAfterAdoption(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	pusher := filepath.Join(home, "pusher")
	templateCheckout(t, upstream, pusher, upstream)
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")
	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home,
		filepath.Join(home, "repository"), filepath.Join(home, ".ao", "worker"),
		"https://github.com/acme/app", "session-1", "feature/x", "main")
	if !decision.BranchCreated || decision.AdoptedAt == "" {
		t.Fatalf("decision = %+v, want a created branch with its adopted commit", decision)
	}
	gitRun(t, pusher, "checkout", "-q", "-b", "feature/x")
	commitAndPush(t, pusher, "feature/x", "FEATURE.md")
	gitRun(t, checkout, "fetch", "-q", upstream, "+refs/heads/*:refs/remotes/origin/*")

	// The first startup failed after adoption; meanwhile the user commits.
	if err := os.WriteFile(filepath.Join(checkout, "USER.md"), []byte("user work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, checkout, "add", "USER.md")
	gitRun(t, checkout, "commit", "-qm", "user work on the adopted branch")
	userCommit := gitOutput(t, checkout, "rev-parse", "HEAD")

	_ = AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "feature/x", "main", decision.BranchCreated, decision.AdoptedAt)
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != userCommit {
		t.Fatalf("the retry discarded the user's commit: HEAD %s, want %s", head, userCommit)
	}
}

// The advance never touches a branch the user switched away from.
func TestAdvanceAdoptedCheckoutLeavesAnotherCheckedOutBranch(t *testing.T) {
	t.Parallel()
	requireGit(t)
	upstream := upstreamRepository(t)
	home := t.TempDir()
	pusher := filepath.Join(home, "pusher")
	templateCheckout(t, upstream, pusher, upstream)
	checkout := filepath.Join(home, "work", "app")
	templateCheckout(t, upstream, checkout, "https://github.com/acme/app.git")
	decision := ResolveReusedCheckout(context.Background(), ExecGitRunner{}, home,
		filepath.Join(home, "repository"), filepath.Join(home, ".ao", "worker"),
		"https://github.com/acme/app", "session-1", "feature/x", "main")
	commitAndPush(t, pusher, "main", "LATER.md")
	gitRun(t, checkout, "fetch", "-q", upstream, "+refs/heads/*:refs/remotes/origin/*")
	gitRun(t, checkout, "checkout", "-q", "-b", "mine", "HEAD")
	mine := gitOutput(t, checkout, "rev-parse", "HEAD")
	if err := AdvanceAdoptedCheckout(context.Background(), ExecGitRunner{}, checkout, "feature/x", "main", decision.BranchCreated, decision.AdoptedAt); err != nil {
		t.Fatal(err)
	}
	if head := gitOutput(t, checkout, "rev-parse", "HEAD"); head != mine {
		t.Fatalf("the user's branch moved: HEAD %s, want %s", head, mine)
	}
}
