package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A failed step says what it ran, as whom, where, and where its full output
// is, so the person need not open the sandbox to see what failed.
func TestSetupFailureReportsItsCommandUserDirectoryAndLog(t *testing.T) {
	t.Parallel()
	dataDir, parent := t.TempDir(), t.TempDir()
	workspace := filepath.Join(parent, "app")
	if err := os.MkdirAll(filepath.Join(parent, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	commands := []string{
		"echo first-step-output",
		domain.RepoScriptCommand("lib", "echo building lib\necho 'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock' >&2; exit 1"),
	}
	event, ran := SetupRunner{DataDir: dataDir, FirstStart: true}.Run(context.Background(), commands, workspace, scriptEnvironment(workspace))
	if !ran || event.Status != SetupFailed || event.Step != 2 || event.ExitCode != 1 {
		t.Fatalf("event = %+v", event)
	}
	identity := CurrentScriptIdentity()
	if event.Command != "echo building lib echo 'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock' >&2; exit 1" ||
		event.Repository != "lib" || event.Dir != filepath.Join(parent, "lib") ||
		event.User != identity.User || len(event.Groups) == 0 {
		t.Fatalf("event = %+v", event)
	}
	if !strings.Contains(event.Hint, "cannot use Docker") || !strings.Contains(event.Hint, identity.User) {
		t.Fatalf("hint = %q", event.Hint)
	}
	logged, err := os.ReadFile(event.LogPath)
	if err != nil || !strings.Contains(string(logged), "building lib") || !strings.Contains(string(logged), "docker.sock") {
		t.Fatalf("log %s = %q (%v)", event.LogPath, logged, err)
	}
	if info, err := os.Stat(event.LogPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %v (%v), want 0600", info.Mode(), err)
	}
	// A successful step's output stays in its log, never in the event.
	first, err := os.ReadFile(filepath.Join(dataDir, setupLogsDir, "step-1.log"))
	if err != nil || strings.TrimSpace(string(first)) != "first-step-output" {
		t.Fatalf("step 1 log = %q (%v)", first, err)
	}
	described := event.Context()
	for _, want := range []string{"Step 2: echo building lib", "Ran as " + identity.User + " (groups: ", " in " + filepath.Join(parent, "lib"), "Full output: " + event.LogPath} {
		if !strings.Contains(described, want) {
			t.Fatalf("context %q lacks %q", described, want)
		}
	}
	// A retry keeps only its own logs.
	if _, ran := (SetupRunner{DataDir: dataDir, FirstStart: true}).Run(context.Background(), []string{"exit 2"}, workspace, scriptEnvironment(workspace)); !ran {
		t.Fatal("retry did not run")
	}
	if _, err := os.Stat(filepath.Join(dataDir, setupLogsDir, "step-2.log")); err == nil {
		t.Fatal("a retry kept the previous run's step 2 log")
	}
}

func TestScriptFailureHint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		output, runAs, want string
		code                int
	}{
		{output: "Got permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock", runAs: "ao-worker", want: "ao-worker, which cannot use Docker", code: 1},
		{output: "sudo: a password is required", runAs: "ao-worker", want: "cannot use sudo", code: 1},
		{output: "ao-worker is not in the sudoers file.  This incident will be reported.\nsudo: unable", runAs: "ao-worker", want: "cannot use sudo", code: 1},
		{output: "mkdir: cannot create directory '/opt/x': Permission denied", runAs: "ao-worker", want: "refused access to a file or directory", code: 1},
		{output: "sh: 1: pnpm: not found", runAs: "ao-worker", want: "not installed in the sandbox", code: 127},
		{output: "cleanup step 1 failed: exit status 1\nran as ao-worker (groups: ao-worker) in /w\nmkdir: Permission denied", want: "ran as ao-worker, which was refused", code: 1},
		{output: "tests failed", runAs: "ao-worker", want: "", code: 1},
		// ssh refusing a key is not a file permission.
		{output: "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", runAs: "ao-worker", want: "could not sign in with an SSH key", code: 128},
	} {
		got := ScriptFailureHint(tc.output, tc.code, tc.runAs)
		if tc.want == "" && got != "" || !strings.Contains(strings.ToLower(got), strings.ToLower(tc.want)) {
			t.Errorf("ScriptFailureHint(%q) = %q, want it to contain %q", tc.output, got, tc.want)
		}
		if strings.Contains(tc.output, "publickey") && strings.Contains(got, "file or directory") {
			t.Errorf("ScriptFailureHint(%q) = %q, gave the file permission hint", tc.output, got)
		}
	}
}

// A secret that straddles the length limit is redacted whole, not cut into a
// fragment redaction no longer recognizes.
func TestScriptCommandSummaryRedactsBeforeItCuts(t *testing.T) {
	t.Parallel()
	token := "ghp_" + strings.Repeat("A", 36)
	command := strings.Repeat("x", scriptCommandLimit-10) + " " + token + " && make"
	summary, _ := scriptCommandSummary(command, scriptRedactor{})
	if strings.Contains(summary, "ghp_") || strings.Contains(summary, "AAAA") {
		t.Fatalf("summary leaks part of the token: %q", summary)
	}
	if !strings.Contains(summary, "[REDACTED]") && !strings.HasSuffix(summary, "...") {
		t.Fatalf("summary = %q, want the token redacted", summary)
	}
}

// A failed Cleanup step names its command, user, groups and directory, and
// the hook says where its full output is.
func TestCleanupFailureNamesItsCommandUserAndLog(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dataDir, workspace := t.TempDir(), t.TempDir()
	if err := WriteCleanupHook(dataDir, []string{"echo tearing down; exit 4"}, workspace, scriptEnvironment(workspace)); err != nil {
		t.Fatal(err)
	}
	output, _ := exec.Command(filepath.Join(dataDir, CleanupHookFile)).CombinedOutput()
	identity := CurrentScriptIdentity()
	for _, want := range []string{
		"cleanup step 1 failed: exit status 4",
		"cleanup step 1: echo tearing down; exit 4",
		"ran as " + identity.User + " (groups: ",
		"full cleanup output: " + filepath.Join(dataDir, cleanupStateDir, "output"),
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("hook output %q lacks %q", output, want)
		}
	}
}
