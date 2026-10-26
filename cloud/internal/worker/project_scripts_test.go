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

func scriptEnvironment(dir string) SessionScriptEnvironment {
	return SessionScriptEnvironment{
		SessionID: "session-1", SessionKind: "worker", Branch: "ao/session-1", WorkspaceDir: dir,
		RepositoryURL: "https://github.com/acme/app", ProjectID: "project-1", ProjectName: "app",
	}
}

// Setup runs every command in order in the workspace with the session
// environment, and runs once per session: a later worker start skips it.
func TestSetupRunsCommandsInOrderOncePerSession(t *testing.T) {
	t.Parallel()
	dataDir, workspace := t.TempDir(), t.TempDir()
	commands := []string{
		`echo "one $AO_SESSION_ID $AO_SESSION_KIND" >> trace`,
		"  ",
		`echo "two $(pwd) $AO_WORKTREE_PATH $AO_SOURCE_TREE_PATH $AO_PROJECT_NAME" >> trace`,
	}
	event, ran := SetupRunner{DataDir: dataDir, FirstStart: true}.Run(context.Background(), commands, workspace, scriptEnvironment(workspace))
	if !ran || event.Status != SetupSucceeded || event.Step != 0 {
		t.Fatalf("event = %+v, ran = %v", event, ran)
	}
	trace, err := os.ReadFile(filepath.Join(workspace, "trace"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(workspace)
	want := "one session-1 worker\ntwo " + resolved + " " + workspace + " " + workspace + " app\n"
	if string(trace) != want {
		t.Fatalf("trace = %q, want %q", trace, want)
	}
	if _, ran := (SetupRunner{DataDir: dataDir, FirstStart: true}).Run(context.Background(), commands, workspace, scriptEnvironment(workspace)); ran {
		t.Fatal("setup ran twice for the session")
	}
	if _, ran := (SetupRunner{DataDir: t.TempDir(), FirstStart: true}).Run(context.Background(), nil, workspace, scriptEnvironment(workspace)); ran {
		t.Fatal("ran with no commands")
	}
}

// A failing step stops Setup at that step with its exit status and output and
// is not recorded, so the next session open runs Setup again.
func TestSetupStopsAtTheFailingStepAndRetries(t *testing.T) {
	t.Parallel()
	dataDir, workspace := t.TempDir(), t.TempDir()
	commands := []string{"true", "echo installing; echo broken >&2; exit 3", "touch never-ran"}
	event, ran := SetupRunner{DataDir: dataDir, FirstStart: true}.Run(context.Background(), commands, workspace, scriptEnvironment(workspace))
	if !ran || event.Status != SetupFailed || event.Step != 2 || event.ExitCode != 3 ||
		!strings.Contains(event.Output, "broken") {
		t.Fatalf("event = %+v", event)
	}
	if _, err := os.Stat(filepath.Join(workspace, "never-ran")); err == nil {
		t.Fatal("a step after the failure ran")
	}
	if message := event.Message(); !strings.HasPrefix(message, "The project's Setup script failed at step 2 (exit status 3): ") {
		t.Fatalf("message = %q", message)
	}
	if _, ran := (SetupRunner{DataDir: dataDir, FirstStart: true}).Run(context.Background(), commands, workspace, scriptEnvironment(workspace)); !ran {
		t.Fatal("a failed setup was recorded as done")
	}
}

// A step over its deadline is stopped together with what it started. Its
// message names the limit it ran with (StepTimeout's 200ms reads as 1 minute;
// a step without its own limit says Setup's 30-minute default, see
// TestSetupTimeoutSummaryNamesTheStepsLimit).
func TestSetupStepTimeout(t *testing.T) {
	t.Parallel()
	event, _ := SetupRunner{DataDir: t.TempDir(), FirstStart: true, StepTimeout: 200 * time.Millisecond}.Run(
		context.Background(), []string{"sleep 30 & wait"}, t.TempDir(), scriptEnvironment(t.TempDir()))
	if event.Status != SetupTimedOut || event.Step != 1 || !strings.Contains(event.Message(), "stopped at step 1 after 1 minute") {
		t.Fatalf("event = %+v", event)
	}
}

func TestSetupPublishesNoOutputOnSuccess(t *testing.T) {
	t.Parallel()
	event, ran := SetupRunner{DataDir: t.TempDir(), FirstStart: true}.Run(context.Background(),
		[]string{"echo DB_PASS=hunter2"}, t.TempDir(), scriptEnvironment(t.TempDir()))
	if !ran || event.Status != SetupSucceeded || event.Output != "" {
		t.Fatalf("event = %+v", event)
	}
}

func TestSetupKeepsTheTailOfLongOutput(t *testing.T) {
	t.Parallel()
	event, _ := SetupRunner{DataDir: t.TempDir(), FirstStart: true}.Run(context.Background(),
		[]string{"head -c 20000 /dev/zero | tr '\\0' a; echo; echo the-end; exit 1"}, t.TempDir(), scriptEnvironment(t.TempDir()))
	if len(event.Output) > ProjectScriptOutputLimit || !strings.HasSuffix(event.Output, "the-end") {
		t.Fatalf("output (%d bytes) ends %q", len(event.Output), event.Output[max(0, len(event.Output)-20):])
	}
}

// The Cleanup hook runs the commands in the workspace with the session
// environment, stops at the first failure, runs them only once however often
// it is invoked, and stays installed (doing nothing) when the project has no
// Cleanup commands, so Cleanup added later can still run.
func TestCleanupHookRunsCommandsOnce(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dataDir, workspace := t.TempDir(), t.TempDir()
	commands := []string{`echo "down $AO_SESSION_ID $AO_WORKTREE_PATH" >> trace`, "echo cleaning; exit 5", "touch never-ran"}
	if err := WriteCleanupHook(dataDir, commands, workspace, scriptEnvironment(workspace)); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dataDir, CleanupHookFile)
	run := func() (string, int) {
		output, err := exec.Command(hook).CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("run hook: %v", err)
		}
		return string(output), code
	}
	output, code := run()
	if code != 5 || !strings.Contains(output, "cleaning") || !strings.Contains(output, "cleanup step 2 failed") {
		t.Fatalf("first run: code = %d, output = %q", code, output)
	}
	if again, code := run(); code != 5 || !strings.Contains(again, "cleaning") {
		t.Fatalf("second run: code = %d, output = %q", code, again)
	}
	trace, err := os.ReadFile(filepath.Join(workspace, "trace"))
	if err != nil || string(trace) != "down session-1 "+workspace+"\n" {
		t.Fatalf("trace = %q (%v): the commands must run exactly once", trace, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "never-ran")); err == nil {
		t.Fatal("a step after the failure ran")
	}
	emptyDir := t.TempDir()
	if err := WriteCleanupHook(emptyDir, []string{" "}, workspace, scriptEnvironment(workspace)); err != nil {
		t.Fatal(err)
	}
	output, err = func() (string, error) {
		out, err := exec.Command(filepath.Join(emptyDir, CleanupHookFile)).CombinedOutput()
		return string(out), err
	}()
	if err != nil || strings.TrimSpace(output) != "" {
		t.Fatalf("hook with no Cleanup commands: output = %q, err = %v; want a silent success", output, err)
	}
}

// Setup runs when the session's workspace is first prepared. A restarted
// worker never runs a Setup the project gained later inside the session's
// live workspace, but it does retry one that failed.
func TestSetupRunsOnlyForTheSessionsFirstPreparation(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	env := scriptEnvironment(workspace)
	marker := "touch " + filepath.Join(workspace, "ran")

	// Started without Setup, then the project gains one.
	dataDir := t.TempDir()
	if _, ran := (SetupRunner{DataDir: dataDir, FirstStart: true}).Run(context.Background(), nil, workspace, env); ran {
		t.Fatal("ran with no commands")
	}
	if _, ran := (SetupRunner{DataDir: dataDir}).Run(context.Background(), []string{marker}, workspace, env); ran {
		t.Fatal("a Setup added later ran in the prepared workspace")
	}

	// A session from before Setup existed, restarted under this worker.
	if _, ran := (SetupRunner{DataDir: t.TempDir()}).Run(context.Background(), []string{marker}, workspace, env); ran {
		t.Fatal("a pre-Setup session ran Setup on restart")
	}

	// A failed Setup keeps running on later starts until it succeeds.
	dataDir = t.TempDir()
	if event, ran := (SetupRunner{DataDir: dataDir, FirstStart: true}).Run(context.Background(), []string{"exit 3"}, workspace, env); !ran || event.Status == SetupSucceeded {
		t.Fatalf("first run = %+v, %v", event, ran)
	}
	if event, ran := (SetupRunner{DataDir: dataDir}).Run(context.Background(), []string{marker}, workspace, env); !ran || event.Status != SetupSucceeded {
		t.Fatalf("retry after restart = %+v, %v", event, ran)
	}
	if _, err := os.Stat(filepath.Join(workspace, "ran")); err != nil {
		t.Fatal("the retried Setup did not run")
	}
	if _, ran := (SetupRunner{DataDir: dataDir}).Run(context.Background(), []string{marker}, workspace, env); ran {
		t.Fatal("Setup ran again after it succeeded")
	}
}

// Setup and Cleanup of a project with additional repositories reach every
// checkout through AO_REPOS_ROOT, the folder that holds them all.
func TestScriptEnvironmentNamesTheReposRoot(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "repository")
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	environment := scriptEnvironment(workspace)
	if got := environment.Map()["AO_REPOS_ROOT"]; got != root {
		t.Fatalf("AO_REPOS_ROOT = %q, want the workspace's parent %q", got, root)
	}
	environment.ReposRoot = root
	dataDir := t.TempDir()
	event, ran := SetupRunner{DataDir: dataDir, FirstStart: true}.Run(context.Background(),
		[]string{`ls "$AO_REPOS_ROOT" > "$AO_REPOS_ROOT/api/listing"`}, workspace, environment)
	if !ran || event.Status != SetupSucceeded {
		t.Fatalf("event = %+v, ran = %v", event, ran)
	}
	listing, err := os.ReadFile(filepath.Join(root, "api", "listing"))
	if err != nil || !strings.Contains(string(listing), "api") || !strings.Contains(string(listing), "repository") {
		t.Fatalf("listing = %q (%v), want both checkouts", listing, err)
	}
	if err := WriteCleanupHook(dataDir, nil, workspace, environment); err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(filepath.Join(dataDir, CleanupHookFile))
	if err != nil || !strings.Contains(string(hook), "export AO_REPOS_ROOT=") {
		t.Fatalf("cleanup hook does not export AO_REPOS_ROOT: %s (%v)", hook, err)
	}
}

// Cleanup sees the HOME Setup ran with (the worker's), not the one its runner
// would set: sudo -H gives the hook owner's account home instead.
func TestCleanupHookKeepsTheWorkersHome(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dataDir, workspace, workerHome := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", workerHome)
	if err := WriteCleanupHook(dataDir, []string{`echo "home=$HOME" > home-seen`}, workspace, scriptEnvironment(workspace)); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(dataDir, CleanupHookFile))
	command.Env = append(os.Environ(), "HOME=/home/someone-else")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run hook: %v: %s", err, output)
	}
	seen, err := os.ReadFile(filepath.Join(workspace, "home-seen"))
	if err != nil || strings.TrimSpace(string(seen)) != "home="+workerHome {
		t.Fatalf("Cleanup saw %q (%v), want home=%s", seen, err, workerHome)
	}
}
