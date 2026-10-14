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
	for _, worktreeConfig := range []bool{false, true} {
		t.Run("worktreeConfig="+map[bool]string{false: "off", true: "on"}[worktreeConfig], func(t *testing.T) {
			s, fr, data := testSandbox(t, nil)
			if worktreeConfig {
				fr.git = map[string]string{"extensions.worktreeConfig": "true\n"}
			}
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
				{Path: filepath.Join(git, "logs")},
				{Path: admin},
				{Path: filepath.Join(admin, "commondir"), ReadOnly: true},
				{Path: filepath.Join(admin, "gitdir"), ReadOnly: true},
				{Path: filepath.Join(wsOf(data), ".git"), ReadOnly: true},
			}
			if worktreeConfig {
				want = append(want, mount{Path: filepath.Join(admin, "config.worktree"), ReadOnly: true})
			}
			if got := gitMountsOf(ms, git, wsOf(data)); !reflect.DeepEqual(got, want) {
				t.Fatalf("git mounts =\n%+v\nwant\n%+v", got, want)
			}
			requireParentsFirst(t, ms)
			if fi, err := os.Lstat(filepath.Join(git, "logs")); err != nil || !fi.IsDir() {
				t.Fatalf("logs dir not created: %v", err)
			}
			fi, err := os.Lstat(filepath.Join(admin, "config.worktree"))
			if worktreeConfig && (err != nil || !fi.Mode().IsRegular() || fi.Size() != 0) {
				t.Fatalf("config.worktree = %v, %v; want an empty regular file", fi, err)
			}
			if !worktreeConfig && err == nil {
				t.Fatal("config.worktree created although the extension is off")
			}
		})
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
		{Path: filepath.Join(git, "logs")},
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
	order := []string{roBindArg(git), bindArg(filepath.Join(git, "objects")), bindArg(adminOf(data)),
		roBindArg(filepath.Join(adminOf(data), "commondir")), roBindArg(filepath.Join(wsOf(data), ".git"))}
	last := -1
	for _, want := range order {
		i := strings.Index(run, want)
		if i < 0 || i < last {
			t.Fatalf("docker run missing or misordered %q:\n%s", want, run)
		}
		last = i
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
		"symlinked config.worktree": {func(t *testing.T, fr *fakeRunner, data string, _ *ports.SandboxRequest) {
			fr.git = map[string]string{"extensions.worktreeConfig": "true\n"}
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

func TestResolveGitRealWorktreeConfigExtension(t *testing.T) {
	repo, wt := newRepoWithWorktree(t)
	gitRun(t, repo, "config", "extensions.worktreeConfig", "true")
	s, req := realReq(t, repo, wt)
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !g.worktreeConfig {
		t.Fatal("worktreeConfig extension not detected")
	}
	if fi, err := os.Lstat(filepath.Join(g.admin, "config.worktree")); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
		t.Fatalf("config.worktree = %v, %v; want a 0644 regular file", fi, err)
	}
}
