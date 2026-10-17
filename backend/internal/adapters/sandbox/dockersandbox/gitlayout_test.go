package dockersandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitMountsOf keeps the mounts that expose git metadata: anything in the git
// dir plus the worktree's .git pointer file.
func gitMountsOf(ms []mount, gitDir, ws string) []mount {
	var out []mount
	for _, m := range ms {
		if within(gitDir, m.Path) || m.Path == filepath.Join(ws, ".git") {
			out = append(out, m)
		}
	}
	return out
}

func requireParentsFirst(t *testing.T, ms []mount) {
	t.Helper()
	for i := range ms {
		for j := i + 1; j < len(ms); j++ {
			if ms[j].Path != ms[i].Path && within(ms[j].Path, ms[i].Path) {
				t.Fatalf("mount %s comes after its child %s: %+v", ms[j].Path, ms[i].Path, ms)
			}
		}
	}
}

func TestMountsPerAgentGitLayout(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	req := perAgentReq(wsOf(data))
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := s.mounts(req, g)
	if err != nil {
		t.Fatal(err)
	}
	git, admin := gitDirOf(data), adminOf(data)
	want := []mount{
		{Path: git, ReadOnly: true},
		{Path: filepath.Join(git, "objects")},
		{Path: filepath.Join(git, "refs")},
		{Path: filepath.Join(git, "logs"), Tmpfs: true, Writable: true},
		{Path: admin},
		{Path: filepath.Join(admin, "logs"), Tmpfs: true, Writable: true},
		{Path: filepath.Join(admin, "commondir"), ReadOnly: true},
		{Path: filepath.Join(admin, "gitdir"), ReadOnly: true},
		{Path: filepath.Join(wsOf(data), ".git"), ReadOnly: true},
		// Bound read-only whatever extensions.worktreeConfig says: it can be
		// turned on later, and host git then obeys this file.
		{Path: filepath.Join(admin, "config.worktree"), ReadOnly: true},
	}
	if got := gitMountsOf(ms, git, wsOf(data)); !reflect.DeepEqual(got, want) {
		t.Fatalf("git mounts =\n%+v\nwant\n%+v", got, want)
	}
	requireParentsFirst(t, ms)
	for _, d := range []string{filepath.Join(git, "logs"), filepath.Join(admin, "logs")} {
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
			t.Fatalf("tmpfs mountpoint %s not created: %v", d, err)
		}
	}
	fi, err := os.Lstat(filepath.Join(admin, "config.worktree"))
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 || fi.Mode().Perm() != 0o644 {
		t.Fatalf("config.worktree = %v, %v; want an empty 0644 regular file", fi, err)
	}
}

func TestMountsPerAgentKeepsExistingConfigWorktreeReadOnly(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	cfg := filepath.Join(adminOf(data), "config.worktree")
	mustWrite(t, cfg, "[core]\n\tfsmonitor = planted\n")
	req := perAgentReq(wsOf(data))
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := s.mounts(req, g)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range ms {
		if m.Path == cfg {
			found = m.ReadOnly
		}
	}
	if !found {
		t.Fatalf("existing config.worktree not bound read-only in %+v", ms)
	}
	if body, err := os.ReadFile(cfg); err != nil || !strings.Contains(string(body), "planted") {
		t.Fatalf("existing config.worktree was rewritten: %q, %v", body, err)
	}
}

func TestMountsSharedGitLayout(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	req := perAgentReq(wsOf(data))
	req.Config.Mode = domain.SandboxModeShared
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := s.mounts(req, g)
	if err != nil {
		t.Fatal(err)
	}
	git := gitDirOf(data)
	want := []mount{
		{Path: git, ReadOnly: true},
		{Path: filepath.Join(git, "objects")},
		{Path: filepath.Join(git, "refs")},
		{Path: filepath.Join(git, "logs"), Tmpfs: true, Writable: true},
		{Path: filepath.Join(git, "worktrees")},
	}
	if got := gitMountsOf(ms, git, wsOf(data)); !reflect.DeepEqual(got, want) {
		t.Fatalf("git mounts =\n%+v\nwant\n%+v", got, want)
	}
	requireParentsFirst(t, ms)
}

// The layout reaches docker as ordered --mount binds, and every host git
// call disables fsmonitor and hooks.
func TestEnsurePassesGitLayoutToDocker(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	if _, err := s.Ensure(context.Background(), perAgentReq(wsOf(data))); err != nil {
		t.Fatal(err)
	}
	run := runCall(t, fr) + " "
	git := gitDirOf(data)
	// Reflogs land on container-local tmpfs, mounted after the bind each one
	// shadows, so host git never appends through a planted symlink.
	order := []string{roBindArg(git), bindArg(filepath.Join(git, "objects")),
		"--mount type=tmpfs,destination=" + filepath.Join(git, "logs") + ",tmpfs-mode=1777 ", bindArg(adminOf(data)),
		"--mount type=tmpfs,destination=" + filepath.Join(adminOf(data), "logs") + ",tmpfs-mode=1777 ",
		roBindArg(filepath.Join(adminOf(data), "commondir")), roBindArg(filepath.Join(wsOf(data), ".git"))}
	last := -1
	for _, want := range order {
		i := strings.Index(run, want)
		if i < 0 || i < last {
			t.Fatalf("docker run missing or misordered %q:\n%s", want, run)
		}
		last = i
	}
	if strings.Contains(run, "src="+filepath.Join(git, "logs")) {
		t.Fatalf("host reflogs are bind-mounted:\n%s", run)
	}
	if strings.Contains(run, " -v ") {
		t.Fatalf("docker run still uses -v:\n%s", run)
	}
	for _, dir := range []string{"/repo", wsOf(data)} {
		want := "git -c core.fsmonitor=false -c core.hooksPath=/dev/null -C " + dir + " rev-parse --path-format=absolute --git-common-dir"
		found := false
		for _, c := range fr.gitCalls {
			found = found || c == want
		}
		if !found {
			t.Fatalf("missing hardened git call %q in %q", want, fr.gitCalls)
		}
	}
}

func TestResolveGitRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(t *testing.T, fr *fakeRunner, data string, req *ports.SandboxRequest)
		want   string
	}{
		"empty repo path": {func(_ *testing.T, _ *fakeRunner, _ string, req *ports.SandboxRequest) { req.RepoPath = "" }, "repository"},
		"workspace of another repo": {func(_ *testing.T, fr *fakeRunner, data string, _ *ports.SandboxRequest) {
			fr.git = map[string]string{"-C " + wsOf(data) + " rev-parse": filepath.Join(data, "other", ".git") + "\n"}
		}, "does not belong to repository"},
		"no objects dir": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			if err := os.RemoveAll(filepath.Join(gitDirOf(data), "objects")); err != nil {
				t.Fatal(err)
			}
		}, "objects"},
		"refs is a symlink": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			refs := filepath.Join(gitDirOf(data), "refs")
			if err := os.RemoveAll(refs); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), refs); err != nil {
				t.Fatal(err)
			}
		}, "refs"},
		"no admin dir": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			mustWrite(t, filepath.Join(adminOf(data), "gitdir"), "/elsewhere/.git\n")
		}, "worktree"},
		"two admin dirs": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			mustWrite(t, filepath.Join(gitDirOf(data), "worktrees", "x", "gitdir"), filepath.Join(wsOf(data), ".git")+"\n")
		}, "worktree"},
		"symlinked commondir": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			p := filepath.Join(adminOf(data), "commondir")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(writeOutside(t, "../.."), p); err != nil {
				t.Fatal(err)
			}
		}, "commondir"},
		"main checkout": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			dotGit := filepath.Join(wsOf(data), ".git")
			if err := os.Remove(dotGit); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dotGit, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "per-agent sandbox needs an AO-managed worktree, not the repository's main checkout"},
		"symlinked worktree logs": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			if err := os.Symlink(t.TempDir(), filepath.Join(adminOf(data), "logs")); err != nil {
				t.Fatal(err)
			}
		}, "logs"},
		"symlinked config.worktree": {func(t *testing.T, _ *fakeRunner, data string, _ *ports.SandboxRequest) {
			if err := os.Symlink(writeOutside(t, "secret"), filepath.Join(adminOf(data), "config.worktree")); err != nil {
				t.Fatal(err)
			}
		}, "config.worktree"},
	} {
		t.Run(name, func(t *testing.T) {
			s, fr, data := testSandbox(t, nil)
			req := perAgentReq(wsOf(data))
			tc.mutate(t, fr, data, &req)
			_, err := s.resolveGit(context.Background(), req)
			if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrSandboxMisconfigured mentioning %q", err, tc.want)
			}
		})
	}
}

func TestEnsureReadsHostGitIdentity(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	fr.git = map[string]string{"-C /repo config --get user.name": "Ada Lovelace\n"}
	target, err := s.Ensure(context.Background(), perAgentReq(wsOf(data)))
	if err != nil {
		t.Fatal(err)
	}
	if target.GitUserName != "Ada Lovelace" || target.GitUserEmail != "" {
		t.Fatalf("identity = %q <%q>, want the host name and no email", target.GitUserName, target.GitUserEmail)
	}
}

// --mount does not create a missing source, so Ensure must refuse rather
// than hand docker a dangling bind.
func TestEnsureMissingBindSourceIsMisconfigured(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	req := perAgentReq(filepath.Join(data, "worktrees", "q", "s1"))
	req.ProjectID = "q"
	req.Config.Mode = domain.SandboxModeShared
	_, err := s.Ensure(context.Background(), req)
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), filepath.Join(data, "worktrees", "q")) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured naming the missing source", err)
	}
}

func readEnvFile(t *testing.T, target ports.SandboxTarget) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(target.SessionDir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExecSetsGitConfigEnv(t *testing.T) {
	cases := map[string]struct {
		name, email string
		env         map[string]string
		want        []string
		absent      []string
	}{
		"safe.directory only": {
			want:   []string{"GIT_CONFIG_COUNT='1'", "GIT_CONFIG_KEY_0='safe.directory'", "GIT_CONFIG_VALUE_0='*'"},
			absent: []string{"GIT_CONFIG_KEY_1"},
		},
		"identity": {
			name: "Ada", email: "ada@example.com",
			want: []string{"GIT_CONFIG_COUNT='3'", "GIT_CONFIG_KEY_0='safe.directory'", "GIT_CONFIG_KEY_1='user.name'",
				"GIT_CONFIG_VALUE_1='Ada'", "GIT_CONFIG_KEY_2='user.email'", "GIT_CONFIG_VALUE_2='ada@example.com'"},
		},
		"email only": {
			email: "ada@example.com",
			want:  []string{"GIT_CONFIG_COUNT='2'", "GIT_CONFIG_KEY_1='user.email'"},
		},
		"appends to caller entries": {
			name: "Ada",
			env:  map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.editor", "GIT_CONFIG_VALUE_0": "vi"},
			want: []string{"GIT_CONFIG_COUNT='3'", "GIT_CONFIG_KEY_0='core.editor'", "GIT_CONFIG_VALUE_0='vi'",
				"GIT_CONFIG_KEY_1='safe.directory'", "GIT_CONFIG_KEY_2='user.name'"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, data := testSandbox(t, nil)
			target := execTarget(t, s)
			target.GitUserName, target.GitUserEmail = tc.name, tc.email
			if _, err := s.Exec(target, filepath.Join(data, "w"), tc.env, []string{"claude"}); err != nil {
				t.Fatal(err)
			}
			body := readEnvFile(t, target)
			for _, w := range tc.want {
				if !strings.Contains(body, w+"\n") {
					t.Fatalf("env file missing %q:\n%s", w, body)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Fatalf("env file has %q:\n%s", a, body)
				}
			}
		})
	}
}

func TestExecRejectsInvalidGitConfigCount(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	target := execTarget(t, s)
	_, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"GIT_CONFIG_COUNT": "x"}, []string{"claude"})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured", err)
	}
}

// Real git: the admin dir is found from the git dir's own records, never
// from the worktree's .git file, which the container could rewrite.

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepoWithWorktree returns a repository and a linked worktree "wt" of it.
func newRepoWithWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base := t.TempDir()
	repo, wt = filepath.Join(base, "repo"), filepath.Join(base, "wt")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "wt", wt)
	return repo, wt
}

func realReq(t *testing.T, repo, ws string) (*Sandbox, ports.SandboxRequest) {
	t.Helper()
	data := t.TempDir()
	s := newWithRunner(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees")}, execRunner{}, "/usr/bin/docker")
	return s, ports.SandboxRequest{SessionID: "s1", ProjectID: "p", RepoPath: repo, WorkspacePath: ws,
		Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}}
}

func TestResolveGitRealWorktree(t *testing.T) {
	repo, wt := newRepoWithWorktree(t)
	gitDir := gitRun(t, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	own := filepath.Join(gitDir, "worktrees", "wt")
	gitRun(t, repo, "config", "user.name", "Ada")

	// A decoy admin dir pointing elsewhere, a non-regular gitdir that would
	// match, and a worktree .git rewritten to name the decoy.
	decoy := filepath.Join(gitDir, "worktrees", "x")
	mustWrite(t, filepath.Join(decoy, "gitdir"), "/elsewhere/.git\n")
	mustWrite(t, filepath.Join(decoy, "commondir"), "../..\n")
	mustWrite(t, filepath.Join(decoy, "HEAD"), gitRun(t, repo, "rev-parse", "HEAD")+"\n")
	link := filepath.Join(gitDir, "worktrees", "y")
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(own, "gitdir"), filepath.Join(link, "gitdir")); err != nil {
		t.Fatal(err)
	}
	s, req := realReq(t, repo, wt)
	if _, err := s.resolveGit(context.Background(), req); err != nil {
		t.Fatalf("untampered worktree: %v", err)
	}
	mustWrite(t, filepath.Join(wt, ".git"), "gitdir: "+decoy+"\n")
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if physicalPath(g.admin) != physicalPath(own) {
		t.Fatalf("admin = %s, want %s", g.admin, own)
	}
	if g.userName != "Ada" || g.userEmail != "" {
		t.Fatalf("identity = %q <%q>, want Ada and no email", g.userName, g.userEmail)
	}
}

func TestResolveGitRealRejectsForeignWorktree(t *testing.T) {
	repoA, _ := newRepoWithWorktree(t)
	_, wtB := newRepoWithWorktree(t)
	s, req := realReq(t, repoA, wtB)
	_, err := s.resolveGit(context.Background(), req)
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "does not belong to repository") {
		t.Fatalf("err = %v, want a repository mismatch", err)
	}
}

func TestResolveGitRealCreatesConfigWorktreeWithExtensionOff(t *testing.T) {
	repo, wt := newRepoWithWorktree(t)
	s, req := realReq(t, repo, wt)
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(g.admin, "config.worktree")); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
		t.Fatalf("config.worktree = %v, %v; want a 0644 regular file", fi, err)
	}
}
