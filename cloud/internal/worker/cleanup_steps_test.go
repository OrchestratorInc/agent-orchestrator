package worker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runCleanupScript runs a Cleanup commands script the way the hook does
// (sh <script>, output and errors together, in the workspace) and returns its
// output and exit status.
func runCleanupScript(t *testing.T, script, workspace string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, CleanupCommandsFile)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", path)
	command.Dir = workspace
	output, err := command.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(output), code
}

// The markers add lines only: the same commands run in order with the same
// exit status, and the control plane splits the output into one final event
// per step, with the steps after a failure skipped.
func TestCleanupMarkersSplitTheRunIntoSteps(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	commands := []string{
		"echo one >> trace; echo first",
		" ",
		"printf 'no newline'",
		"echo cleaning; echo 'permission denied' >&2; exit 5",
		"touch never-ran",
	}
	nonce := NewScriptMarkerNonce()
	output, code := runCleanupScript(t, CleanupCommandsScript(commands, nonce), workspace)
	if code != 5 {
		t.Fatalf("exit status = %d, want the failed step's 5; output %q", code, output)
	}
	if _, err := os.Stat(filepath.Join(workspace, "never-ran")); err == nil {
		t.Fatal("a step after the failure ran")
	}
	if trace, _ := os.ReadFile(filepath.Join(workspace, "trace")); string(trace) != "one\n" {
		t.Fatalf("trace = %q", trace)
	}
	started := time.Now()
	events := CleanupStepEvents(output, CleanupRun{
		RunID: "cleanup-1", Nonce: nonce, Commands: nonEmptyCommands(commands), ExitCode: code,
		StartedAt: started, Duration: time.Second,
	})
	if len(events) != 4 {
		t.Fatalf("events = %+v, want 4 steps", events)
	}
	for index, event := range events {
		if !ValidScriptStepEvent(event) || event.Phase != ScriptPhaseCleanup || event.RunID != CleanupRunID(nonce) ||
			event.Step != index+1 || event.Total != 4 {
			t.Fatalf("event %d = %+v", index, event)
		}
	}
	for index, want := range []string{"first", "no newline"} {
		event := events[index]
		if event.Status != SetupSucceeded || *event.ExitCode != 0 || event.DurationMS == nil ||
			event.StartedAt == "" || event.Output != want || event.Command != nonEmptyCommands(commands)[index] {
			t.Fatalf("step %d = %+v", index+1, event)
		}
		if !strings.HasSuffix(event.LogPath, filepath.Join(cleanupStateDir, "output")) {
			t.Fatalf("step %d log = %q", index+1, event.LogPath)
		}
	}
	failed := events[2]
	identity := CurrentScriptIdentity()
	if failed.Status != SetupFailed || *failed.ExitCode != 5 || failed.User != identity.User ||
		len(failed.Groups) == 0 || failed.Dir == "" || failed.Hint == "" ||
		!strings.HasPrefix(failed.Output, "cleaning\npermission denied\ncleanup step 3 failed: exit status 5") {
		t.Fatalf("failed step = %+v", failed)
	}
	if skipped := events[3]; skipped.Status != ScriptStepSkipped || skipped.Command != "touch never-ran" ||
		skipped.ExitCode != nil || skipped.LogPath != "" {
		t.Fatalf("skipped step = %+v", skipped)
	}
}

// Through the installed hook, whose commands script carries its own nonce,
// the control plane reads the nonce from the output.
func TestCleanupHookOutputCarriesItsMarkers(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dataDir, workspace := t.TempDir(), t.TempDir()
	if err := WriteCleanupHook(dataDir, []string{"echo bye $AO_SESSION_ID"}, workspace, scriptEnvironment(workspace)); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(filepath.Join(dataDir, CleanupHookFile)).CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v (%s)", err, output)
	}
	events := CleanupStepEvents(string(output), CleanupRun{RunID: "r", StartedAt: time.Now()})
	if len(events) != 1 || events[0].Status != SetupSucceeded || events[0].Output != "bye session-1" ||
		events[0].Command != "echo bye $AO_SESSION_ID" || events[0].LogPath != filepath.Join(dataDir, cleanupStateDir, "output") {
		t.Fatalf("events = %+v", events)
	}
}

// A command that prints lines like markers (another nonce, the bare form, a
// half marker) is read as output, never as a step.
func TestCleanupMarkersIgnoreLookalikes(t *testing.T) {
	t.Parallel()
	nonce := NewScriptMarkerNonce()
	other := strings.Repeat("0", 16)
	commands := []string{
		"echo '@@ao-step-" + other + " 1 1 end 0 5'; echo '@@ao-step 1 1 end 0 5'; echo '@@ao-step-" + nonce + "'; echo done",
		"true",
	}
	output, code := runCleanupScript(t, CleanupCommandsScript(commands, nonce), t.TempDir())
	events := CleanupStepEvents(output, CleanupRun{RunID: "r", Nonce: nonce, Commands: commands, ExitCode: code})
	if len(events) != 2 || events[0].Status != SetupSucceeded || events[1].Status != SetupSucceeded {
		t.Fatalf("events = %+v", events)
	}
	want := "@@ao-step-" + other + " 1 1 end 0 5\n@@ao-step 1 1 end 0 5\n@@ao-step-" + nonce + "\ndone"
	if events[0].Output != want {
		t.Fatalf("step 1 output = %q, want %q", events[0].Output, want)
	}
	// A run whose commands script printed another nonce than the one the
	// control plane installed (its install failed) is read with that nonce.
	installed := NewScriptMarkerNonce()
	output, code = runCleanupScript(t, CleanupCommandsScript([]string{"echo kept"}, installed), t.TempDir())
	events = CleanupStepEvents(output, CleanupRun{RunID: "r", Nonce: nonce, Commands: []string{"echo current"}, ExitCode: code})
	if len(events) != 1 || events[0].Output != "kept" || events[0].Command != "echo kept" {
		t.Fatalf("events with the installed script = %+v", events)
	}
}

// An old commands script without markers is reported as one step with the
// run's status and output; a silent success (no Cleanup) as nothing.
func TestCleanupWithoutMarkers(t *testing.T) {
	t.Parallel()
	output := "tearing down\ncleanup step 1 failed: exit status 4\ncleanup step 1: make down\n" +
		"ran as ao-worker (groups: ao-worker docker) in /home/coder/app\nfull cleanup output: /home/ao-worker/.ao/worker/cleanup-state/output\n"
	started := time.Now()
	events := CleanupStepEvents(output, CleanupRun{RunID: "r", ExitCode: 4, StartedAt: started, Duration: 3 * time.Second})
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	event := events[0]
	if !ValidScriptStepEvent(event) || event.Step != 1 || event.Total != 1 || event.Status != SetupFailed ||
		*event.ExitCode != 4 || *event.DurationMS != 3000 || event.User != "ao-worker" ||
		strings.Join(event.Groups, ",") != "ao-worker,docker" || event.Dir != "/home/coder/app" ||
		event.LogPath != "/home/ao-worker/.ao/worker/cleanup-state/output" || !strings.HasPrefix(event.Output, "tearing down") {
		t.Fatalf("event = %+v", event)
	}
	if events := CleanupStepEvents(" \n", CleanupRun{RunID: "r"}); len(events) != 0 {
		t.Fatalf("silent success reported %+v", events)
	}
	if events := CleanupStepEvents("all good\n", CleanupRun{RunID: "r"}); len(events) != 1 || events[0].Status != SetupSucceeded {
		t.Fatalf("successful run without markers = %+v", events)
	}
}

// Output that lost its beginning (an older hook prints only its last 8 KiB)
// still reports every step: those before the first shown succeeded, the one
// whose start is cut keeps the output it still has.
func TestCleanupMarkersWithPartialOutput(t *testing.T) {
	t.Parallel()
	nonce := NewScriptMarkerNonce()
	commands := []string{"seq 1 4000", "seq 1 3000; echo two-done", "echo three; exit 7", "echo four"}
	full, code := runCleanupScript(t, CleanupCommandsScript(commands, nonce), t.TempDir())
	tail := full[len(full)-8192:]
	events := CleanupStepEvents(tail, CleanupRun{RunID: "r", Nonce: nonce, Commands: commands, ExitCode: code})
	if len(events) != 4 {
		t.Fatalf("events = %+v", events)
	}
	if first := events[0]; first.Status != SetupSucceeded || *first.ExitCode != 0 || first.DurationMS != nil ||
		first.Output != "" || !first.OutputTruncated || first.Command != "seq 1 4000" {
		t.Fatalf("step 1 = %+v", first)
	}
	if second := events[1]; second.Status != SetupSucceeded || !second.OutputTruncated ||
		!strings.HasSuffix(second.Output, "3000\ntwo-done") || second.DurationMS == nil || second.StartedAt != "" {
		t.Fatalf("step 2 = %+v", second)
	}
	if third := events[2]; third.Status != SetupFailed || *third.ExitCode != 7 || !strings.HasPrefix(third.Output, "three") {
		t.Fatalf("step 3 = %+v", third)
	}
	if events[3].Status != ScriptStepSkipped {
		t.Fatalf("step 4 = %+v", events[3])
	}
}

// A hook stopped during a step (its budget ran out) reports that step as
// timed out and the rest as skipped.
func TestCleanupStoppedDuringAStep(t *testing.T) {
	t.Parallel()
	nonce := NewScriptMarkerNonce()
	started := time.Now().Add(-time.Minute)
	output := "@@ao-step-" + nonce + " log /data/cleanup-state/output\r\n" +
		"@@ao-step-" + nonce + " 1 3 start " + itoa64(started.UnixMilli()) + " / make down\r\n" +
		"stopping\r\n" +
		"@@ao-step-" + nonce + " 1 3 end 0 1500\r\n" +
		"@@ao-step-" + nonce + " 2 3 start " + itoa64(started.Add(2*time.Second).UnixMilli()) + " api docker compose down\r\n" +
		"\x1b[32mwaiting\x1b[0m for ghp_" + strings.Repeat("x", 36) + "\r\n"
	events := CleanupStepEvents(output, CleanupRun{RunID: "r", Nonce: nonce, ExitCode: 124, StartedAt: started, Duration: time.Minute})
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	if first := events[0]; first.Status != SetupSucceeded || *first.DurationMS != 1500 || first.Output != "stopping" ||
		first.Command != "make down" || first.Repository != "" || first.LogPath != "/data/cleanup-state/output" {
		t.Fatalf("step 1 = %+v", first)
	}
	second := events[1]
	if second.Status != SetupTimedOut || *second.ExitCode != -1 || second.Repository != "api" ||
		second.Command != "docker compose down" || second.Output != "waiting for [REDACTED]" {
		t.Fatalf("step 2 = %+v", second)
	}
	if *second.DurationMS < 57000 || *second.DurationMS > 59000 {
		t.Fatalf("step 2 duration = %d ms, want about 58 s", *second.DurationMS)
	}
	if events[2].Status != ScriptStepSkipped {
		t.Fatalf("step 3 = %+v", events[2])
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "ghp_") {
		t.Fatalf("token published: %s", encoded)
	}
}

// A step stopped before the commands script could say whom it ran as is
// reported as run by the account the control plane ran the hook as, as a
// failed Setup step names its user; a "ran as" line in the output wins.
func TestCleanupFailedStepNamesTheHookUser(t *testing.T) {
	t.Parallel()
	nonce := NewScriptMarkerNonce()
	started := time.Now().Add(-time.Minute)
	stopped := "@@ao-step-" + nonce + " 1 1 start " + itoa64(started.UnixMilli()) + " / make down\n" + "stopping\n"
	events := CleanupStepEvents(stopped, CleanupRun{RunID: "r", Nonce: nonce, User: "ao-worker", ExitCode: 124, StartedAt: started, Duration: time.Minute})
	if len(events) != 1 || events[0].Status != SetupTimedOut || events[0].User != "ao-worker" {
		t.Fatalf("stopped step = %+v", events)
	}
	reported := "@@ao-step-" + nonce + " 1 1 start " + itoa64(started.UnixMilli()) + " / make down\n" +
		"cleanup step 1 failed: exit status 2\nran as coder (groups: coder) in /home/coder/app\n" +
		"@@ao-step-" + nonce + " 1 1 end 2 1500\n"
	events = CleanupStepEvents(reported, CleanupRun{RunID: "r", Nonce: nonce, User: "ao-worker", ExitCode: 2, StartedAt: started, Duration: time.Minute})
	if len(events) != 1 || events[0].Status != SetupFailed || events[0].User != "coder" || events[0].Dir != "/home/coder/app" {
		t.Fatalf("reported step = %+v", events)
	}
	// A step that succeeded names no user, as a Setup step that succeeded does.
	succeeded := "@@ao-step-" + nonce + " 1 1 start " + itoa64(started.UnixMilli()) + " / make down\n" +
		"@@ao-step-" + nonce + " 1 1 end 0 1500\n"
	events = CleanupStepEvents(succeeded, CleanupRun{RunID: "r", Nonce: nonce, User: "ao-worker", StartedAt: started, Duration: time.Minute})
	if len(events) != 1 || events[0].Status != SetupSucceeded || events[0].User != "" {
		t.Fatalf("succeeded step = %+v", events)
	}
}

func itoa64(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// A Cleanup step that ends part way through printing a token has the token's
// start masked, as a Setup step's final event does.
func TestCleanupStepMasksAnUnfinishedToken(t *testing.T) {
	t.Parallel()
	nonce := NewScriptMarkerNonce()
	output, code := runCleanupScript(t, CleanupCommandsScript([]string{
		"echo stopping; printf 'ghp_abcdefghijklmno'", "printf 'DB_PASSWORD=supersecr'",
	}, nonce), t.TempDir())
	events := CleanupStepEvents(output, CleanupRun{RunID: "cleanup-1", Nonce: nonce, ExitCode: code, StartedAt: time.Now()})
	if len(events) != 2 || events[0].Output != "stopping\n[REDACTED]" || events[1].Output != "DB_PASSWORD=[REDACTED]" {
		t.Fatalf("events = %+v", events)
	}
}

// The hook runs its commands once and prints that run's output again when run
// again (a control plane that restarted mid-deletion plans a new run with a
// new nonce): the steps keep the first run's id, so the session shows one run.
func TestCleanupRunKeepsItsIDWhenTheHookIsRunAgain(t *testing.T) {
	t.Parallel()
	nonce := NewScriptMarkerNonce()
	output, code := runCleanupScript(t, CleanupCommandsScript([]string{"echo stopping"}, nonce), t.TempDir())
	first := CleanupStepEvents(output, CleanupRun{RunID: CleanupRunID(nonce), Nonce: nonce, ExitCode: code, StartedAt: time.Now()})
	retried := NewScriptMarkerNonce()
	again := CleanupStepEvents(output, CleanupRun{RunID: CleanupRunID(retried), Nonce: retried, ExitCode: code, StartedAt: time.Now()})
	if len(first) != 1 || len(again) != 1 || first[0].RunID != CleanupRunID(nonce) || again[0].RunID != first[0].RunID {
		t.Fatalf("first %+v, again %+v", first, again)
	}
}
