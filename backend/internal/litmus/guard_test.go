package litmus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// GuardEnv keeps git settings already in the environment and adds its own after them.
func TestGuardEnvKeepsExistingGitConfig(t *testing.T) {
	env := map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "safe.directory", "GIT_CONFIG_VALUE_0": "*"}
	runDir := t.TempDir()
	GuardEnv(env, runDir)
	if env["GIT_CONFIG_KEY_0"] != "safe.directory" || env["GIT_CONFIG_VALUE_0"] != "*" {
		t.Fatalf("existing setting overwritten: %v", env)
	}
	n := 1 + len(pushPrefixes)
	if env["GIT_CONFIG_COUNT"] != strconv.Itoa(n) || env["GIT_CONFIG_KEY_1"] != "url.litmus-blocks-push://.pushInsteadOf" || env["GIT_CONFIG_VALUE_"+strconv.Itoa(n-1)] != "/" {
		t.Fatalf("push block not appended: %v", env)
	}
	// gh must find no login: an empty config dir inside the run dir and no token.
	if token, ok := env["GH_TOKEN"]; !ok || token != "" {
		t.Fatalf("GH_TOKEN must be set empty: %q %v", token, ok)
	}
	cfg := env["GH_CONFIG_DIR"]
	if cfg == "" || filepath.Dir(cfg) != runDir {
		t.Fatalf("GH_CONFIG_DIR %q not inside the run dir %q", cfg, runDir)
	}
	if entries, err := os.ReadDir(cfg); err != nil || len(entries) != 0 {
		t.Fatalf("GH_CONFIG_DIR must exist and be empty: %v %v", err, entries)
	}
}

// GuardEnv must stop a real git push to every remote form while fetch keeps working.
func TestGuardEnvBlocksEveryPush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	remote, work := filepath.Join(dir, "remote.git"), filepath.Join(dir, "work")
	env := map[string]string{}
	GuardEnv(env, dir)
	git := func(guarded bool, args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if guarded {
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if err := exec.Command("git", "init", "-q", "--bare", remote).Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "x"}, {"remote", "add", "origin", remote}} {
		if out, err := git(false, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	for _, target := range []string{"origin", "file://" + remote, "https://github.com/acme/web.git", "git@github.com:acme/web.git", "ssh://git@github.com/acme/web.git"} {
		out, err := git(true, "push", target, "HEAD:refs/heads/leak")
		if err == nil || !strings.Contains(out, "litmus-blocks-push") {
			t.Errorf("push to %s was not blocked: %v %s", target, err, out)
		}
	}
	if refs, err := exec.Command("git", "-C", remote, "for-each-ref").Output(); err != nil || len(refs) != 0 {
		t.Fatalf("remote got refs %q (%v)", refs, err)
	}
	if out, err := git(true, "fetch", "-q", "origin"); err != nil {
		t.Fatalf("fetch was blocked: %v %s", err, out)
	}
	if out, err := git(false, "push", "-q", "origin", "HEAD:refs/heads/ok"); err != nil {
		t.Fatalf("an unguarded push failed, so the test proves nothing: %v %s", err, out)
	}
}
