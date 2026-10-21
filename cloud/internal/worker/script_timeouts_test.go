package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// A Setup step runs with the time limit its marker line sets, without the
// marker, and its events carry the limit; a step without one has Setup's
// 30-minute default.
func TestSetupRunsEachStepWithItsOwnTimeLimit(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var events []ScriptStepEvent
	runner := SetupRunner{
		DataDir: t.TempDir(), FirstStart: true, FinalWait: time.Second,
		Publish: func(_ context.Context, event ScriptStepEvent) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
			return nil
		},
	}
	workspace := t.TempDir()
	event, ran := runner.Run(context.Background(), []string{"# ao-timeout: 45m\necho first", "echo second", "# ao-timeout: 1m\nexit 4", "# ao-timeout: 120m\ntrue"},
		workspace, scriptEnvironment(workspace))
	if !ran || event.Status != SetupFailed || event.Step != 3 || event.TimeoutMinutes != 1 {
		t.Fatalf("event = %+v", event)
	}
	if strings.Contains(event.Command, "ao-timeout") {
		t.Fatalf("the failed step's command kept its marker: %q", event.Command)
	}
	mu.Lock()
	defer mu.Unlock()
	limits := map[int]int{}
	for _, published := range events {
		if strings.Contains(published.Command, "ao-timeout") {
			t.Fatalf("step %d command kept its marker: %q", published.Step, published.Command)
		}
		if published.Status != ScriptStepRunning {
			limits[published.Step] = published.TimeoutMinutes
		}
	}
	if want := map[int]int{1: 45, 2: 30, 3: 1, 4: 120}; !reflect.DeepEqual(limits, want) {
		t.Fatalf("limits = %v, want %v (events %+v)", limits, want, events)
	}
}

// A timed-out step says its own limit.
func TestSetupTimeoutSummaryNamesTheStepsLimit(t *testing.T) {
	t.Parallel()
	for minutes, want := range map[int]string{
		0:  "The project's Setup script stopped at step 2 after 30 minutes",
		1:  "The project's Setup script stopped at step 2 after 1 minute",
		90: "The project's Setup script stopped at step 2 after 90 minutes",
	} {
		event := SetupEvent{Status: SetupTimedOut, Step: 2, ExitCode: -1, TimeoutMinutes: minutes}
		if got := event.Summary(); got != want {
			t.Errorf("TimeoutMinutes %d: %q, want %q", minutes, got, want)
		}
	}
	event, _ := SetupRunner{DataDir: t.TempDir(), FirstStart: true, StepTimeout: 200 * time.Millisecond}.Run(
		context.Background(), []string{"# ao-timeout: 30m\nsleep 30"}, t.TempDir(), scriptEnvironment(t.TempDir()))
	if event.Status != SetupTimedOut || event.TimeoutMinutes != 1 || event.Summary() != "The project's Setup script stopped at step 1 after 1 minute" {
		t.Fatalf("event = %+v (%q)", event, event.Summary())
	}
}

// The Cleanup commands script bounds each step by its own limit and reports
// it on the step's end marker; the events carry it.
func TestCleanupScriptRunsEachStepWithItsOwnTimeLimit(t *testing.T) {
	t.Parallel()
	commands := []string{"# ao-timeout: 45m\necho first", "echo second", "# ao-timeout: 2m\n(\ncd -- '../web' || exit 1\necho third\n)"}
	nonce := NewScriptMarkerNonce()
	script := CleanupCommandsScript(commands, nonce)
	for _, want := range []string{
		`ao_timeout="timeout -k 5"`,
		"${ao_timeout:+$ao_timeout 2700} sh -c 'echo first'",
		"${ao_timeout:+$ao_timeout 600} sh -c 'echo second'",
		"${ao_timeout:+$ao_timeout 120} sh -c '(",
		" end $ao_code $(($(ao_ms) - $ao_started)) 2700\"",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "sh -c '# ao-timeout") {
		t.Fatalf("a step runs with its marker:\n%s", script)
	}
	workspace := t.TempDir()
	output, code := runCleanupScript(t, script, workspace)
	if code != 1 {
		// The third step cds into a folder that is not there.
		t.Fatalf("exit status = %d, output %q", code, output)
	}
	events := CleanupStepEvents(output, CleanupRun{RunID: "r", Nonce: nonce, Commands: commands, ExitCode: code, StartedAt: time.Now(), Duration: time.Second})
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	for index, want := range []struct {
		minutes int
		command string
	}{{45, "echo first"}, {10, "echo second"}, {2, "echo third"}} {
		if events[index].TimeoutMinutes != want.minutes || events[index].Command != want.command {
			t.Fatalf("step %d = %+v, want limit %d and command %q", index+1, events[index], want.minutes, want.command)
		}
	}
}

// A step that ends with timeout(1)'s status is timed out only once it ran for
// its own limit: under a 45-minute limit, exit 124 after 11 minutes is the
// command's own failure. A script from before per-step limits reports no
// limit, and its steps had the default.
func TestCleanupStepTimeoutClassificationFollowsTheStepsLimit(t *testing.T) {
	t.Parallel()
	nonce := "0123456789abcdef"
	marker := "@@ao-step-" + nonce + " "
	output := marker + "1 3 start 1700000000000 / sleep long\n" +
		marker + "1 3 end 124 660000 2700\n" +
		marker + "2 3 start 1700000660000 / sleep longer\n" +
		marker + "2 3 end 124 2700000 2700\n"
	events := CleanupStepEvents(output, CleanupRun{RunID: "r", ExitCode: 124, StartedAt: time.Now(), Duration: time.Hour})
	if len(events) != 3 || events[0].Status != SetupFailed || events[0].TimeoutMinutes != 45 ||
		events[1].Status != SetupTimedOut || events[1].TimeoutMinutes != 45 || events[2].Status != ScriptStepSkipped {
		t.Fatalf("events = %+v", events)
	}
	legacy := marker + "1 1 start 1700000000000 / sleep long\n" + marker + "1 1 end 124 600000\n"
	events = CleanupStepEvents(legacy, CleanupRun{RunID: "r", ExitCode: 124, StartedAt: time.Now(), Duration: 10 * time.Minute})
	if len(events) != 1 || events[0].Status != SetupTimedOut || events[0].TimeoutMinutes != 0 {
		t.Fatalf("legacy events = %+v", events)
	}
}

// A launch carries each step's limit beside its plain commands; a worker joins
// them back into the step commands it runs.
func TestLaunchContextCarriesStepTimeouts(t *testing.T) {
	t.Parallel()
	launch := LaunchContext{
		PostCreate: []string{"make up", "npm ci"}, PostCreateTimeoutMinutes: []int{0, 45},
		PreRemove: []string{"make down"},
	}
	postCreate, preRemove := launch.ProjectScripts()
	if !reflect.DeepEqual(postCreate, []string{"make up", "# ao-timeout: 45m\nnpm ci"}) || !reflect.DeepEqual(preRemove, []string{"make down"}) {
		t.Fatalf("scripts = %q / %q", postCreate, preRemove)
	}
	encoded, _ := json.Marshal(LaunchContext{PostCreate: []string{"make up"}})
	if strings.Contains(string(encoded), "TimeoutMinutes") || strings.Contains(string(encoded), "timeoutMinutes") {
		t.Fatalf("a launch with default limits names them: %s", encoded)
	}
	// An older worker decodes the launch into a struct without the fields.
	var older struct {
		PostCreate []string `json:"postCreate"`
	}
	encoded, _ = json.Marshal(launch)
	if err := json.Unmarshal(encoded, &older); err != nil || !reflect.DeepEqual(older.PostCreate, []string{"make up", "npm ci"}) {
		t.Fatalf("older worker reads %+v (%v)", older, err)
	}
}

// A worker's step event names a valid limit; the control plane drops one out
// of range.
func TestSanitizeScriptStepEventBoundsTheLimit(t *testing.T) {
	t.Parallel()
	zero := 0
	for minutes, want := range map[int]int{45: 45, 0: 0, 500: 0, -3: 0} {
		event := SanitizeScriptStepEvent(ScriptStepEvent{Phase: ScriptPhaseSetup, RunID: "r", Step: 1, Total: 1, Status: SetupSucceeded, ExitCode: &zero, TimeoutMinutes: minutes})
		if event.TimeoutMinutes != want {
			t.Errorf("TimeoutMinutes %d sanitized to %d, want %d", minutes, event.TimeoutMinutes, want)
		}
	}
}
