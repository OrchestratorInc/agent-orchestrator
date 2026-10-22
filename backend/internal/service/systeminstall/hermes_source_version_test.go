package systeminstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHermesTaglessSourceBannerReportsCheckoutTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "release")
	git("tag", "-a", "v0.22.2", "-m", "v0.22.2")
	launcher := filepath.Join(root, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	head := git("rev-parse", "HEAD")
	// Hermes prints vgit.<sha> when its own tag lookup comes up empty.
	output := "Hermes Agent vgit." + head[:7] + " (2026.9.24) · stable · local " + head[:12] + "\nPython 3.11.9\n"
	if got := installedVersionText(TargetHermes, launcher, output); got != "v0.22.2" {
		t.Fatalf("installedVersionText = %q, want v0.22.2", got)
	}
}
