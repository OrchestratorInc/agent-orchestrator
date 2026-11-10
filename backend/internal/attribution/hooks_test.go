package attribution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testCoAuthor = "Orchestrator <1+orchestrator-test[bot]@users.noreply.github.com>"

func TestIdentityTrailer(t *testing.T) {
	id := Identity{Name: "Orchestrator", Slug: "orchestrator", UserID: 42}
	if got, want := id.Login(), "orchestrator[bot]"; got != want {
		t.Fatalf("Login() = %q, want %q", got, want)
	}
	if got, want := id.Trailer(), "Co-authored-by: Orchestrator <42+orchestrator[bot]@users.noreply.github.com>"; got != want {
		t.Fatalf("Trailer() = %q, want %q", got, want)
	}
	if Bot.UserID == 0 || Bot.Slug == "" {
		t.Fatalf("Bot identity is not configured: %+v", Bot)
	}
}

type repo struct {
	t        *testing.T
	main     string
	worktree string
	hooks    string
}

func (r repo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com",
		"GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_EDITOR=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r repo) message(rev string) string { return r.git(r.worktree, "log", "-1", "--format=%B", rev) }

func (r repo) commit(args ...string) {
	r.t.Helper()
	r.git(r.worktree, append([]string{"commit", "--allow-empty"}, args...)...)
}

// newRepo makes a repository with a linked session worktree configured the way
// the session manager configures one.
func newRepo(t *testing.T, coAuthor string, setup func(r repo)) repo {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts are exercised through Git's sh on Unix")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	r := repo{t: t, main: filepath.Join(root, "main"), worktree: filepath.Join(root, "wt"), hooks: filepath.Join(root, "ao", "git-hooks")}
	if err := os.MkdirAll(r.main, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git(r.main, "init", "-q", "-b", "main")
	r.git(r.main, "commit", "-q", "--allow-empty", "-m", "init")
	if setup != nil {
		setup(r)
	}
	r.git(r.main, "config", "extensions.worktreeConfig", "true")
	r.git(r.main, "worktree", "add", "-q", "-b", "orchestrator/s-1/root", r.worktree)
	if err := InstallHooks(r.hooks); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureWorktree(context.Background(), r.worktree, r.hooks, coAuthor); err != nil {
		t.Fatal(err)
	}
	return r
}

func countTrailer(msg string) int { return strings.Count(msg, "Co-authored-by: "+testCoAuthor) }

func TestHookAddsTrailerOnce(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.commit("-m", "feat: one")
	if got := countTrailer(r.message("HEAD")); got != 1 {
		t.Fatalf("trailer count after commit = %d, message:\n%s", got, r.message("HEAD"))
	}
	r.commit("--amend", "--no-edit")
	r.commit("--amend", "-m", r.message("HEAD"))
	if got := countTrailer(r.message("HEAD")); got != 1 {
		t.Fatalf("trailer count after amend = %d, message:\n%s", got, r.message("HEAD"))
	}
}

func TestHookKeepsOtherCoAuthors(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.commit("-m", "feat: two\n\nCo-Authored-By: Agent <agent@example.com>")
	msg := r.message("HEAD")
	if !strings.Contains(msg, "Co-Authored-By: Agent <agent@example.com>") || countTrailer(msg) != 1 {
		t.Fatalf("want both co-authors, got:\n%s", msg)
	}
	lines := strings.Split(msg, "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "Co-authored-by: Orchestrator") || !strings.HasPrefix(lines[len(lines)-2], "Co-Authored-By: Agent") {
		t.Fatalf("want one trailer block with Orchestrator last, got:\n%s", msg)
	}
}

func TestHookAddsOnNoVerifyAndKeepsOneOnRebase(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.commit("--no-verify", "-m", "feat: three")
	if got := countTrailer(r.message("HEAD")); got != 1 {
		t.Fatalf("--no-verify commit trailer count = %d", got)
	}
	r.commit("-m", "feat: four")
	r.git(r.worktree, "rebase", "-q", "-f", "HEAD~2")
	for _, rev := range []string{"HEAD", "HEAD~1"} {
		if got := countTrailer(r.message(rev)); got != 1 {
			t.Fatalf("%s trailer count after rebase = %d:\n%s", rev, got, r.message(rev))
		}
	}
}

// writeCommit commits a one-line file change in dir, so a replay never drops
// it as empty.
func (r repo) writeCommit(dir, file, msg string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git(dir, "add", file)
	r.git(dir, "commit", "-q", "-m", msg)
}

func TestHookLeavesReplayedCommitsAlone(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	// A colleague's branch, written outside the session.
	r.git(r.main, "checkout", "-q", "-b", "colleague")
	r.writeCommit(r.main, "c1", "colleague one")
	r.writeCommit(r.main, "c2", "colleague two")
	r.git(r.main, "checkout", "-q", "main")
	r.writeCommit(r.main, "m1", "upstream")

	// The agent rebases the colleague's branch onto main.
	r.git(r.worktree, "checkout", "-q", "-b", "rebased", "colleague")
	r.git(r.worktree, "rebase", "-q", "main")
	for _, rev := range []string{"HEAD", "HEAD~1"} {
		if msg := r.message(rev); strings.Contains(msg, "Co-authored-by") {
			t.Fatalf("rebased colleague commit %s got the trailer:\n%s", rev, msg)
		}
	}
	// And cherry-picks them, one and then a range.
	r.git(r.worktree, "checkout", "-q", "-b", "picked", "main")
	r.git(r.worktree, "cherry-pick", "colleague~1")
	if msg := r.message("HEAD"); strings.Contains(msg, "Co-authored-by") {
		t.Fatalf("cherry-picked commit got the trailer:\n%s", msg)
	}
	r.git(r.worktree, "reset", "-q", "--hard", "main")
	r.git(r.worktree, "cherry-pick", "main..colleague")
	for _, rev := range []string{"HEAD", "HEAD~1"} {
		if msg := r.message(rev); strings.Contains(msg, "Co-authored-by") {
			t.Fatalf("cherry-picked range commit %s got the trailer:\n%s", rev, msg)
		}
	}
	// The agent's own new commit after that is still credited.
	r.writeCommit(r.worktree, "own", "agent work")
	if got := countTrailer(r.message("HEAD")); got != 1 {
		t.Fatalf("own commit after replays trailer count = %d", got)
	}
}

func TestHookPutsTrailerLastAfterDashLine(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.commit("-m", "feat: x\n\nSummary\n\n---\nDetails")
	lines := strings.Split(r.message("HEAD"), "\n")
	if last := lines[len(lines)-1]; last != "Co-authored-by: "+testCoAuthor {
		t.Fatalf("trailer is not the last line:\n%s", r.message("HEAD"))
	}
}

func TestHookCommentCharacters(t *testing.T) {
	editor := filepath.Join(t.TempDir(), "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' \"$MSG\" > \"$1\"\n"), 0o700); err != nil { //nolint:gosec // test editor script
		t.Fatal(err)
	}
	editorCommit := func(r repo, msg string) error {
		cmd := exec.Command("git", "-C", r.worktree, "commit", "-q", "--allow-empty")
		cmd.Env = append(os.Environ(), "GIT_EDITOR="+editor, "MSG="+msg, "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}

	t.Run("auto with a message starting with #", func(t *testing.T) {
		r := newRepo(t, testCoAuthor, nil)
		r.git(r.worktree, "config", "core.commentChar", "auto")
		r.commit("-m", "#123 fix login")
		if got := countTrailer(r.message("HEAD")); got != 1 {
			t.Fatalf("trailer count = %d:\n%s", got, r.message("HEAD"))
		}
	})
	t.Run("regex-special comment char", func(t *testing.T) {
		r := newRepo(t, testCoAuthor, nil)
		r.git(r.worktree, "config", "core.commentChar", "*")
		if err := editorCommit(r, "fix: star\n* a comment"); err != nil {
			t.Fatal(err)
		}
		msg := r.message("HEAD")
		if countTrailer(msg) != 1 || strings.Contains(msg, "a comment") {
			t.Fatalf("message:\n%s", msg)
		}
		before := r.git(r.worktree, "rev-parse", "HEAD")
		if err := editorCommit(r, "* only a comment"); err == nil {
			t.Fatal("a comment-only message committed")
		}
		if r.git(r.worktree, "rev-parse", "HEAD") != before {
			t.Fatal("HEAD moved on a comment-only message")
		}
	})
	for _, cs := range []struct{ key, value string }{
		{"core.commentString", "//"},
		{"core.commentChar", "["},
	} {
		t.Run(cs.key+"="+cs.value, func(t *testing.T) {
			r := newRepo(t, testCoAuthor, nil)
			r.git(r.worktree, "config", cs.key, cs.value)
			if err := editorCommit(r, "fix: literal\n"+cs.value+" a comment"); err != nil {
				t.Fatal(err)
			}
			if msg := r.message("HEAD"); countTrailer(msg) != 1 || strings.Contains(msg, "a comment") {
				t.Fatalf("message:\n%s", msg)
			}
			// An untouched template (comments only) still aborts.
			before := r.git(r.worktree, "rev-parse", "HEAD")
			cmd := exec.Command("git", "-C", r.worktree, "commit", "--allow-empty")
			cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("comment-only template committed:\n%s\n%s", out, r.message("HEAD"))
			}
			if r.git(r.worktree, "rev-parse", "HEAD") != before {
				t.Fatal("HEAD moved on a comment-only template")
			}
		})
	}
}

func TestHookSkipsMergeCommits(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.git(r.main, "commit", "-q", "--allow-empty", "-m", "upstream")
	r.commit("-m", "feat: five")
	r.git(r.worktree, "merge", "--no-ff", "-q", "--no-edit", "main")
	if got := countTrailer(r.message("HEAD")); got != 0 {
		t.Fatalf("merge commit got the trailer:\n%s", r.message("HEAD"))
	}
	// A conflicted merge concluded with `git commit` is still a merge.
	if err := os.WriteFile(filepath.Join(r.main, "f"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(r.main, "add", "f")
	r.git(r.main, "commit", "-q", "-m", "main f")
	if err := os.WriteFile(filepath.Join(r.worktree, "f"), []byte("wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(r.worktree, "add", "f")
	r.commit("-m", "wt f")
	cmd := exec.Command("git", "-C", r.worktree, "merge", "-q", "main")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
	_ = cmd.Run() // conflicts
	if err := os.WriteFile(filepath.Join(r.worktree, "f"), []byte("both\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(r.worktree, "add", "f")
	r.git(r.worktree, "commit", "-q", "--no-edit")
	if parents := strings.Fields(r.git(r.worktree, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Fatalf("expected a merge commit, parents = %v", parents)
	}
	if got := countTrailer(r.message("HEAD")); got != 0 {
		t.Fatalf("concluded merge got the trailer:\n%s", r.message("HEAD"))
	}
}

func TestHookEmptyMessageStillAborts(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	before := r.git(r.worktree, "rev-parse", "HEAD")
	// GIT_EDITOR=true leaves the template untouched: git must still abort.
	cmd := exec.Command("git", "-C", r.worktree, "commit", "--allow-empty")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("empty commit succeeded:\n%s", out)
	}
	if after := r.git(r.worktree, "rev-parse", "HEAD"); after != before {
		t.Fatal("HEAD moved on an empty message")
	}
}

func TestHookNoCoAuthorAddsNothing(t *testing.T) {
	r := newRepo(t, "", nil)
	r.commit("-m", "feat: off")
	if msg := r.message("HEAD"); strings.Contains(msg, "Co-authored-by") {
		t.Fatalf("trailer added with attribution off:\n%s", msg)
	}
}

func TestHookOnlyAffectsSessionWorktree(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.git(r.main, "commit", "-q", "--allow-empty", "-m", "human commit")
	if msg := r.git(r.main, "log", "-1", "--format=%B"); strings.Contains(msg, "Co-authored-by") {
		t.Fatalf("main checkout got the trailer:\n%s", msg)
	}
	if out, err := exec.Command("git", "-C", r.main, "config", "--get", "core.hooksPath").Output(); err == nil {
		t.Fatalf("main checkout hooksPath = %q", out)
	}
}

func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestHookPassesThroughDefaultHooks(t *testing.T) {
	var marker string
	r := newRepo(t, testCoAuthor, func(r repo) {
		marker = filepath.Join(r.main, "pre-commit-ran")
		writeHook(t, filepath.Join(r.main, ".git", "hooks"), "pre-commit", "echo ran >> '"+marker+"'")
		writeHook(t, filepath.Join(r.main, ".git", "hooks"), "commit-msg", "grep -q 'feat:' \"$1\" || { echo 'bad message' >&2; exit 1; }")
	})
	r.commit("-m", "feat: hooks")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("repository pre-commit did not run: %v", err)
	}
	if countTrailer(r.message("HEAD")) != 1 {
		t.Fatal("trailer missing")
	}
	// A failing repository hook still blocks the commit.
	cmd := exec.Command("git", "-C", r.worktree, "commit", "--allow-empty", "-m", "nope")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "bad message") {
		t.Fatalf("failing commit-msg hook did not block: err=%v\n%s", err, out)
	}
}

func TestHookPassesThroughHuskyHooksPath(t *testing.T) {
	var marker string
	r := newRepo(t, testCoAuthor, func(r repo) {
		marker = filepath.Join(r.main, "husky-ran")
		writeHook(t, filepath.Join(r.main, ".husky"), "pre-commit", "echo \"$(basename \"$0\")\" >> '"+marker+"'")
		writeHook(t, filepath.Join(r.main, ".husky"), "prepare-commit-msg", "printf 'from husky\\n' >> \"$1\"")
		// Committed, as husky's hooks are, so the session worktree has them.
		r.git(r.main, "add", ".husky")
		r.git(r.main, "commit", "-q", "-m", "add husky")
		r.git(r.main, "config", "core.hooksPath", ".husky")
	})
	r.commit("-m", "feat: husky")
	if data, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(data)) != "pre-commit" {
		t.Fatalf("husky pre-commit did not run: %q %v", data, err)
	}
	msg := r.message("HEAD")
	if !strings.Contains(msg, "from husky") || countTrailer(msg) != 1 {
		t.Fatalf("want husky edit and trailer, got:\n%s", msg)
	}
}

func TestHookPicksUpHooksPathSetLater(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	marker := filepath.Join(r.main, "later-ran")
	writeHook(t, filepath.Join(r.worktree, ".husky", "_"), "pre-commit", "echo ran >> '"+marker+"'")
	// What `npm install` with husky does inside the session after it started.
	r.git(r.worktree, "config", "core.hooksPath", ".husky/_")
	r.commit("-m", "feat: later")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("hooksPath set after the session started was not run: %v", err)
	}
	if got := r.git(r.worktree, "config", "--worktree", "--get", "core.hooksPath"); got != r.hooks {
		t.Fatalf("worktree hooksPath = %q, want %q", got, r.hooks)
	}
}

func TestConfigureWorktreeSavesAndRestoresWorktreeHooksPath(t *testing.T) {
	var marker string
	r := newRepo(t, testCoAuthor, nil)
	marker = filepath.Join(r.main, "wt-hook-ran")
	writeHook(t, filepath.Join(r.main, "wthooks"), "pre-commit", "echo ran >> '"+marker+"'")
	ctx := context.Background()
	if err := RemoveFromWorktree(ctx, r.worktree); err != nil {
		t.Fatal(err)
	}
	r.git(r.worktree, "config", "--worktree", "core.hooksPath", filepath.Join(r.main, "wthooks"))
	if err := ConfigureWorktree(ctx, r.worktree, r.hooks, testCoAuthor); err != nil {
		t.Fatal(err)
	}
	// Idempotent: configuring again keeps the saved original.
	if err := ConfigureWorktree(ctx, r.worktree, r.hooks, testCoAuthor); err != nil {
		t.Fatal(err)
	}
	if got := r.git(r.worktree, "config", "--worktree", "--get", ConfigOriginalHooksPath); got != filepath.Join(r.main, "wthooks") {
		t.Fatalf("saved original = %q", got)
	}
	r.commit("-m", "feat: wt hooks")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("worktree-scoped original hook did not run: %v", err)
	}
	if countTrailer(r.message("HEAD")) != 1 {
		t.Fatal("trailer missing")
	}
	if err := RemoveFromWorktree(ctx, r.worktree); err != nil {
		t.Fatal(err)
	}
	if got := r.git(r.worktree, "config", "--worktree", "--get", "core.hooksPath"); got != filepath.Join(r.main, "wthooks") {
		t.Fatalf("restored hooksPath = %q", got)
	}
}

func TestInstallHooksIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	if err := InstallHooks(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "prepare-commit-msg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(dir); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(filepath.Join(dir, "prepare-commit-msg"))
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("unchanged hook was rewritten")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(HookNames) {
		t.Fatalf("hooks dir has %d entries, want %d", len(entries), len(HookNames))
	}
}

func TestConfigureWorktreeAddsRepositoryOnDemandHooks(t *testing.T) {
	r := newRepo(t, testCoAuthor, func(r repo) {
		writeHook(t, filepath.Join(r.main, ".git", "hooks"), "reference-transaction",
			"#!/bin/sh\ncat >/dev/null\necho ran >> \"$(git rev-parse --git-common-dir)/ref-tx.log\"\n")
	})
	if _, err := os.Stat(filepath.Join(r.hooks, "reference-transaction")); err != nil {
		t.Fatalf("on-demand hook not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.hooks, "post-index-change")); !os.IsNotExist(err) {
		t.Fatalf("hook the repository lacks was installed: %v", err)
	}
	r.commit("-m", "feat: refs")
	if _, err := os.Stat(filepath.Join(r.main, ".git", "ref-tx.log")); err != nil {
		t.Fatalf("repository reference-transaction hook did not run: %v", err)
	}
}

func TestRecordChoiceSurvivesRecreatedWorktree(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	ctx := context.Background()
	if err := RecordChoice(ctx, r.worktree, testCoAuthor); err != nil {
		t.Fatal(err)
	}
	r.git(r.main, "worktree", "remove", "--force", r.worktree)
	r.git(r.main, "worktree", "add", "-q", r.worktree, "orchestrator/s-1/root")
	if got := RecordedChoice(ctx, r.worktree); got != testCoAuthor {
		t.Fatalf("recorded choice = %q", got)
	}
	if ConfiguredFor(ctx, r.worktree, r.hooks) {
		t.Fatal("a recreated worktree kept the old hooks config")
	}
	if err := RecordChoice(ctx, r.worktree, ""); err != nil {
		t.Fatal(err)
	}
	if got := RecordedChoice(ctx, r.worktree); got != "" {
		t.Fatalf("cleared choice = %q", got)
	}
	if err := RecordChoice(ctx, r.worktree, ""); err != nil {
		t.Fatalf("clearing twice: %v", err)
	}
}

func TestHookIgnoresPersonalTrailerConfig(t *testing.T) {
	r := newRepo(t, testCoAuthor, nil)
	r.git(r.worktree, "config", "trailer.separators", "=")
	r.git(r.worktree, "config", "trailer.where", "start")
	r.git(r.worktree, "config", "trailer.ifexists", "add")
	r.commit("-m", "feat: example\n\nSigned-off-by: Dev <dev@example.com>")
	msg := r.message("HEAD")
	lines := strings.Split(msg, "\n")
	if countTrailer(msg) != 1 || strings.Contains(msg, ">=") || lines[len(lines)-1] != "Co-authored-by: "+testCoAuthor {
		t.Fatalf("message:\n%s", msg)
	}
}
