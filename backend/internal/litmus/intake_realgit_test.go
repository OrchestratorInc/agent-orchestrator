package litmus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests here run the real git end to end (mirror, fetch, pins, numstat, name-status, patch) over repos in a temp
// dir, under a global git config that would break a parser that trusts the defaults. No network, no gh.

const hostileGitConfig = "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n[diff]\n\tnoprefix = true\n\tmnemonicPrefix = true\n\texternal = /nonexistent/difftool\n[core]\n\tquotePath = true\n[color]\n\tui = always\n"

// runGit runs git in dir and returns its trimmed output.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// realGitPR makes an upstream repo with a base commit on main, a PR branch that build changes and commits (the head),
// and a later commit on main only (the base the PR reports); a mirror of main cloned from it, as Litmus keeps; and a
// service whose gh answers with that PR. The user's git config is replaced by hostileGitConfig for the test.
func realGitPR(t *testing.T, changedFiles int, build func(work string)) (svc *Service, mirror, base, head string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".gitconfig"), hostileGitConfig)
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	work := t.TempDir()
	runGit(t, work, "init", "--quiet", "-b", "main")
	writeFile(t, filepath.Join(work, "docs", "readme.txt"), "one\ntwo\nthree\n")
	writeFile(t, filepath.Join(work, "src", "old.go"), "package main\n\nfunc a() {}\nfunc b() {}\nfunc c() {}\nfunc d() {}\nfunc e() {}\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "--quiet", "-m", "base")
	runGit(t, work, "checkout", "--quiet", "-b", "pr")
	build(work)
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "--quiet", "-m", "the change")
	head = runGit(t, work, "rev-parse", "HEAD")
	runGit(t, work, "checkout", "--quiet", "main")
	writeFile(t, filepath.Join(work, "main-only.txt"), "main moved on after the PR branched\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "--quiet", "-m", "main moves on")
	base = runGit(t, work, "rev-parse", "HEAD")

	prJSON, err := json.Marshal(map[string]any{
		"number": 12, "title": "Add things", "body": "", "state": "OPEN", "author": map[string]string{"login": "dev"},
		"baseRefName": "main", "baseRefOid": base, "headRefName": "pr", "headRefOid": head, "changedFiles": changedFiles,
		"comments": []any{}, "reviews": []any{}, "closingIssuesReferences": []any{}, "statusCheckRollup": []any{}, "url": "https://github.com/acme/widgets/pull/12",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	svc = New(NewStore(t.TempDir()), func() time.Time { return now }).WithGH(&fakeGH{t: t, err: map[string]error{}, out: map[string][]byte{"pr view": prJSON}})
	svc.runner, svc.retryPause = execRunner{}, 0
	mirror = svc.store.mirrorDir("acme/widgets")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Dir(mirror), "clone", "--quiet", "--bare", "--single-branch", "--branch", "main", work, mirror)
	runGit(t, mirror, "config", "gc.auto", "0")
	return svc, mirror, base, head
}

func TestIntakeWithRealGitReadsRenamesBinariesAndHardPaths(t *testing.T) {
	const nonASCII, spaced = "docs/résumé-日本.txt", "my dir/with space.txt"
	svc, mirror, base, head := realGitPR(t, 5, func(work string) {
		writeFile(t, filepath.Join(work, "docs", "readme.txt"), "one\ntwo\nthree\nfour\n")
		runGit(t, work, "mv", "src/old.go", "src/renamed.go")
		writeFile(t, filepath.Join(work, "src", "renamed.go"), "package main\n\nfunc a() {}\nfunc b() {}\nfunc c() {}\nfunc d() {}\nfunc e() {}\nfunc f() {}\n")
		writeFile(t, filepath.Join(work, "img", "new.bin"), "\x00\x01\x02\x03binary\x00")
		writeFile(t, filepath.Join(work, spaced), "hello\n")
		writeFile(t, filepath.Join(work, nonASCII), "bonjour\n")
	})
	run, err := svc.Intake(ctx, "s1", "https://github.com/acme/widgets/pull/12")
	// main moved one commit after the PR branched: the base build is the fork point, and the gap says so
	fork := runGit(t, mirror, "merge-base", base, head)
	forkGap := fmt.Sprintf("The PR branched from main 1 commit ago; 'main' in this report is that branch point (%.8s), not today's main.", fork)
	if err != nil || run.HeadSHA != head || run.BaseSHA != fork || fork == base || !slices.Equal(run.Gaps, []string{forkGap}) {
		t.Fatalf("run %+v err %v", run, err)
	}
	list := strings.Split(strings.TrimSuffix(readRunFile(t, svc, run, "files.txt"), "\n"), "\n")
	want := []string{
		"A\t-\t-\timg/new.bin",
		"A\t1\t0\t" + nonASCII,
		"A\t1\t0\t" + spaced,
		"M\t1\t0\tdocs/readme.txt",
		"R\t1\t0\tsrc/renamed.go\tsrc/old.go",
	}
	if got := list[1:]; !strings.HasPrefix(list[0], "# status") || !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("files.txt:\n%s", strings.Join(list, "\n")) // main-only.txt is not the PR's: three dots
	}
	// the config really does break the defaults: a plain git diff here has no a/ b/ prefixes and quotes the non-ASCII path
	if plain := runGit(t, mirror, "diff", "--patch", "--no-ext-diff", "--no-color", base+"..."+head); strings.Contains(plain, "diff --git a/docs/readme.txt b/docs/readme.txt") || !strings.Contains(plain, `\303`) {
		t.Fatalf("the test's git config does not change git's default patch:\n%s", plain)
	}
	patch := readRunFile(t, svc, run, "diff.patch")
	for _, header := range []string{
		"diff --git a/docs/readme.txt b/docs/readme.txt\n",
		"diff --git a/" + nonASCII + " b/" + nonASCII + "\n",
		"diff --git a/" + spaced + " b/" + spaced + "\n",
		"diff --git a/src/old.go b/src/renamed.go\n", "rename from src/old.go\nrename to src/renamed.go\n",
		"Binary files /dev/null and b/img/new.bin differ\n",
	} {
		if !strings.Contains(patch, header) {
			t.Errorf("diff.patch lacks %q", header)
		}
	}
	if strings.Contains(patch, `\303`) || strings.Contains(patch, "main-only") {
		t.Errorf("diff.patch quotes a path or holds main's own change:\n%s", patch)
	}
	var paths []string
	for _, f := range splitDiff(patch) {
		paths = append(paths, f.path)
	}
	if !slices.Equal(slices.Sorted(slices.Values(paths)), []string{"docs/readme.txt", nonASCII, "img/new.bin", spaced, "src/renamed.go"}) {
		t.Errorf("splitDiff paths %q", paths)
	}
	pins := strings.Fields(runGit(t, mirror, "for-each-ref", "--format=%(refname)", keepRefs))
	if !slices.Equal(slices.Sorted(slices.Values(pins)), slices.Sorted(slices.Values([]string{keepRefs + head, keepRefs + base, keepRefs + fork}))) {
		t.Errorf("pins %q", pins)
	}
	pack := readRunFile(t, svc, run, "context-pack.md")
	if !strings.Contains(pack, "Size: 5 files") || !strings.Contains(pack, "## Gaps in this pack") || !strings.Contains(pack, forkGap) {
		t.Errorf("pack:\n%s", pack[:min(len(pack), 1500)])
	}
}

// A patch past the cap is read only that far from git's stdout, git is stopped there, and the file list stays whole.
func TestIntakeWithRealGitStopsAtThePatchCapAndKeepsTheFileList(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	lines := maxPatch/len(line) + 20000 // about 1.5 MB past the cap
	svc, mirror, base, head := realGitPR(t, 2, func(work string) {
		writeFile(t, filepath.Join(work, "big.txt"), strings.Repeat(line, lines))
		writeFile(t, filepath.Join(work, "small.txt"), "small\n")
	})
	run, err := svc.Intake(ctx, "s1", "https://github.com/acme/widgets/pull/12")
	if err != nil {
		t.Fatal(err)
	}
	fork := runGit(t, mirror, "merge-base", base, head)
	if want := []string{fmt.Sprintf("The PR branched from main 1 commit ago; 'main' in this report is that branch point (%.8s), not today's main.", fork), "The PR's patch is longer than 5.0 MB; I was shown the first 5.0 MB of it and read the rest with git (files.txt lists every file)."}; !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q", run.Gaps)
	}
	patch := readRunFile(t, svc, run, "diff.patch")
	cutAt := strings.Index(patch, "\n[diff.patch cut at 5.0 MB; `git diff ")
	if cutAt < 0 || cutAt > maxPatch || cutAt < maxPatch-200 || !strings.HasSuffix(patch, "shows the rest]\n") { // the cut is at the last whole line
		t.Fatalf("diff.patch: %d bytes, trailer at %d, ends %q", len(patch), cutAt, patch[max(len(patch)-150, 0):])
	}
	if list := readRunFile(t, svc, run, "files.txt"); !strings.Contains(list, "A\t"+strconv.Itoa(lines)+"\t0\tbig.txt\n") || !strings.Contains(list, "A\t1\t0\tsmall.txt\n") {
		t.Fatalf("files.txt is not whole:\n%s", list)
	}
}
