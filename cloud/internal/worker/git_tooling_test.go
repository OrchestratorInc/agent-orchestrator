package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gh pr create is never run with a GitHub token: the wrapper hands it to
// `ao pr-create`, which has the control plane open the pull request as the
// session's owner. Other gh commands name the repository from origin's GitHub
// URL, since the remote git shows is the git proxy.
func TestGitHubCLIWrapperRoutesPullRequestCreationThroughAO(t *testing.T) {
	requireGit(t)
	dataDir := t.TempDir()
	if err := WriteWorkerGitTooling(dataDir, "https://cp.example.com", "sess-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "worker-token"), []byte("WT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := t.TempDir()
	record := filepath.Join(stubs, "record")
	writeStub(t, filepath.Join(stubs, "ao"), "#!/bin/sh\nprintf 'ao %s\\n' \"$*\" >> "+shellQuote(record)+"\n")
	writeStub(t, filepath.Join(stubs, "gh"),
		"#!/bin/sh\nprintf 'gh %s GH_REPO=%s GH_TOKEN=%s\\n' \"$*\" \"${GH_REPO:-}\" \"${GH_TOKEN:-}\" >> "+shellQuote(record)+"\n")
	writeStub(t, filepath.Join(stubs, "curl"), "#!/bin/sh\nprintf '{\"token\":\"ghs_cli\"}'\n")

	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/acme/app.git")
	gitRun(t, repo, "config", "url.https://cp.example.com/api/cloud/v1/worker/git/sess-1/.insteadOf", "https://github.com/")

	wrapper := filepath.Join(ToolingBinDir(dataDir), "gh")
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("sh", append([]string{wrapper}, args...)...)
		command.Dir = repo
		command.Env = append(os.Environ(),
			"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
			"AO_GH_REAL_BINARY="+filepath.Join(stubs, "gh"),
			"GH_REPO=", "GH_TOKEN=", "GITHUB_TOKEN=")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("gh %v: %v\n%s", args, err, out)
		}
	}
	run("pr", "create", "--title", "Fix it", "--body", "Details")
	run("pr", "view", "--json", "state")
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := "ao pr-create --title Fix it --body Details\n" +
		"gh pr view --json state GH_REPO=acme/app GH_TOKEN=ghs_cli\n"
	if string(got) != want {
		t.Fatalf("wrapper ran:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(string(got), "gh pr create") {
		t.Fatal("gh pr create reached the real GitHub CLI")
	}
}
