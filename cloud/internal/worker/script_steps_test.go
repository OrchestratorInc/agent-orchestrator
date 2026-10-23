package worker

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// stepRecorder collects the events a Setup run publishes.
type stepRecorder struct {
	mu     sync.Mutex
	events []ScriptStepEvent
}

func (r *stepRecorder) publish(_ context.Context, event ScriptStepEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *stepRecorder) all() []ScriptStepEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ScriptStepEvent(nil), r.events...)
}

// finals returns each step's last event, by step.
func finals(events []ScriptStepEvent) map[int]ScriptStepEvent {
	last := map[int]ScriptStepEvent{}
	for _, event := range events {
		last[event.Step] = event
	}
	return last
}

func runSetupWithSteps(t *testing.T, runner SetupRunner, commands []string) (SetupEvent, []ScriptStepEvent) {
	t.Helper()
	recorder := &stepRecorder{}
	if runner.Publish == nil {
		runner.Publish = recorder.publish
	}
	if runner.DataDir == "" {
		runner.DataDir = t.TempDir()
	}
	runner.FirstStart = true
	workspace := t.TempDir()
	event, ran := runner.Run(context.Background(), commands, workspace, scriptEnvironment(workspace))
	if !ran {
		t.Fatal("setup did not run")
	}
	return event, recorder.all()
}

// Every step reports its start and its outcome with its output, all under one
// run, in order; a successful step's output is published too.
func TestSetupStepEventsReportEachStep(t *testing.T) {
	t.Parallel()
	summary, events := runSetupWithSteps(t, SetupRunner{UpdateInterval: 20 * time.Millisecond},
		[]string{"echo one", " ", "echo   two >&2"})
	if summary.Status != SetupSucceeded || summary.Output != "" {
		t.Fatalf("summary = %+v; the workspace.setup event must not change", summary)
	}
	if len(events) < 4 {
		t.Fatalf("events = %+v, want a start and an outcome per step", events)
	}
	runID := events[0].RunID
	previousStep := 0
	finished := map[int]bool{}
	for _, event := range events {
		if !ValidScriptStepEvent(event) {
			t.Fatalf("invalid event %+v", event)
		}
		if event.Phase != ScriptPhaseSetup || event.RunID != runID || runID == "" || event.Total != 2 {
			t.Fatalf("event %+v does not belong to the run", event)
		}
		if event.Step < previousStep || finished[event.Step] {
			t.Fatalf("event %+v out of order in %+v", event, events)
		}
		previousStep = event.Step
		if event.Status != ScriptStepRunning {
			finished[event.Step] = true
		}
	}
	if events[0].Status != ScriptStepRunning || events[0].Output != "" || events[0].StartedAt == "" {
		t.Fatalf("first event = %+v, want step 1 running with no output", events[0])
	}
	last := finals(events)
	for step, want := range map[int]string{1: "one", 2: "two"} {
		event := last[step]
		if event.Status != SetupSucceeded || *event.ExitCode != 0 || event.DurationMS == nil ||
			event.Output != want || event.OutputTruncated || event.LogPath == "" {
			t.Fatalf("step %d = %+v", step, event)
		}
		if _, err := time.Parse(time.RFC3339, event.StartedAt); err != nil {
			t.Fatalf("startedAt %q: %v", event.StartedAt, err)
		}
		if event.User != "" || event.Hint != "" {
			t.Fatalf("a successful step carries failure context: %+v", event)
		}
	}
	if last[2].Command != "echo two >&2" || last[2].Repository != "" {
		t.Fatalf("step 2 command = %q (%q)", last[2].Command, last[2].Repository)
	}
}

// A failed step reports its exit status, output and context, and every step
// after it is reported once as skipped, never as running.
func TestSetupStepEventsFailureSkipsTheRest(t *testing.T) {
	t.Parallel()
	summary, events := runSetupWithSteps(t, SetupRunner{},
		[]string{"true", "echo installing; echo broken >&2; exit 3", "touch never-ran", "echo later"})
	if summary.Status != SetupFailed || summary.Step != 2 || summary.ExitCode != 3 {
		t.Fatalf("summary = %+v", summary)
	}
	counts := map[int]int{}
	for _, event := range events {
		counts[event.Step]++
		if event.Step > 2 && event.Status != ScriptStepSkipped {
			t.Fatalf("step %d reported %q after the failure", event.Step, event.Status)
		}
	}
	if counts[3] != 1 || counts[4] != 1 {
		t.Fatalf("skipped steps reported %d and %d times, want once each", counts[3], counts[4])
	}
	last := finals(events)
	failed := last[2]
	if failed.Status != SetupFailed || *failed.ExitCode != 3 || failed.Output != "installing\nbroken" ||
		failed.User == "" || failed.Dir == "" || failed.LogPath == "" {
		t.Fatalf("failed step = %+v", failed)
	}
	for _, step := range []int{3, 4} {
		skipped := last[step]
		if skipped.ExitCode != nil || skipped.DurationMS != nil || skipped.StartedAt != "" ||
			skipped.Command == "" || skipped.Total != 4 {
			t.Fatalf("skipped step %d = %+v", step, skipped)
		}
	}
	if events[len(events)-1].Step != 4 {
		t.Fatalf("the skipped steps must come last: %+v", events)
	}
}

func TestSetupStepEventsTimeout(t *testing.T) {
	t.Parallel()
	_, events := runSetupWithSteps(t, SetupRunner{StepTimeout: 300 * time.Millisecond},
		[]string{"echo started; sleep 30 & wait", "true"})
	last := finals(events)
	if timedOut := last[1]; timedOut.Status != SetupTimedOut || *timedOut.ExitCode != -1 || timedOut.Output != "started" {
		t.Fatalf("timed out step = %+v", timedOut)
	}
	if last[2].Status != ScriptStepSkipped {
		t.Fatalf("step after the timeout = %+v", last[2])
	}
}

// A running step sends updates as its output grows; each carries the whole
// tail so far, and none follows the step's final event.
func TestSetupStepEventsLiveUpdates(t *testing.T) {
	t.Parallel()
	_, events := runSetupWithSteps(t, SetupRunner{UpdateInterval: 50 * time.Millisecond},
		[]string{"for i in 1 2 3 4 5 6; do echo line$i; sleep 0.15; done"})
	var updates []string
	for _, event := range events {
		if event.Status == ScriptStepRunning && event.Output != "" {
			updates = append(updates, event.Output)
		}
	}
	if len(updates) < 2 {
		t.Fatalf("live updates = %q, want several while the step ran", updates)
	}
	for index := 1; index < len(updates); index++ {
		if !strings.HasPrefix(updates[index], updates[index-1]) || updates[index] == updates[index-1] {
			t.Fatalf("updates %q: each must extend the previous one and only be sent on a change", updates)
		}
	}
	if final := events[len(events)-1]; final.Status != SetupSucceeded || !strings.HasSuffix(final.Output, "line6") {
		t.Fatalf("last event = %+v", final)
	}
}

// A step that keeps writing output without finishing a new line (a progress
// line redrawn with \r) still sends live updates at least every heartbeat,
// each saying how long ago it last wrote a byte, so it never reads as silent.
func TestSetupStepEventsHeartbeatWhileOutputKeepsArriving(t *testing.T) {
	t.Parallel()
	// Keep the progress line unfinished: a final echo would complete it and
	// legitimately allow a live update before the process exits.
	_, events := runSetupWithSteps(t, SetupRunner{UpdateInterval: 10 * time.Millisecond, LiveHeartbeat: 100 * time.Millisecond},
		[]string{"echo start; i=0; while [ $i -lt 12 ]; do printf 'progress %s\\r' $i; sleep 0.05; i=$((i+1)); done"})
	heartbeats := 0
	for _, event := range events {
		if event.Status != ScriptStepRunning || event.OutputAgeMS == nil {
			continue
		}
		heartbeats++
		if *event.OutputAgeMS < 0 || *event.OutputAgeMS > 1000 {
			t.Fatalf("output age = %dms while the step kept writing", *event.OutputAgeMS)
		}
		if strings.Contains(event.Output, "progress") {
			t.Fatalf("an unfinished line was published: %q", event.Output)
		}
	}
	if heartbeats < 2 {
		t.Fatalf("live updates with an output age = %d, want several while the step kept writing: %+v", heartbeats, events)
	}
}

// The control plane stamps OutputAt from the worker's output age with its own
// clock, never trusting a time the worker sent, and only on a running step.
func TestStampScriptStepOutputAt(t *testing.T) {
	t.Parallel()
	received := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	age := int64(90_000)
	stamped := StampScriptStepOutputAt(ScriptStepEvent{Status: ScriptStepRunning, OutputAgeMS: &age, OutputAt: "2001-01-01T00:00:00Z"}, received)
	if stamped.OutputAt != "2026-10-20T11:58:30.000Z" || stamped.OutputAgeMS != nil {
		t.Fatalf("stamped = %q, age %v", stamped.OutputAt, stamped.OutputAgeMS)
	}
	if forged := StampScriptStepOutputAt(ScriptStepEvent{Status: ScriptStepRunning, OutputAt: "2099-01-01T00:00:00Z"}, received); forged.OutputAt != "" {
		t.Fatalf("a worker's own OutputAt was kept: %q", forged.OutputAt)
	}
	if finished := StampScriptStepOutputAt(ScriptStepEvent{Status: SetupSucceeded, OutputAgeMS: &age}, received); finished.OutputAt != "" || finished.OutputAgeMS != nil {
		t.Fatalf("a finished step got %q", finished.OutputAt)
	}
	huge := int64(48 * time.Hour / time.Millisecond)
	if capped := StampScriptStepOutputAt(ScriptStepEvent{Status: ScriptStepRunning, OutputAgeMS: &huge}, received); capped.OutputAt != "2026-10-19T12:00:00.000Z" {
		t.Fatalf("capped = %q", capped.OutputAt)
	}
	// An age that would overflow a Duration is capped, not wrapped into the future.
	overflow := int64(math.MaxInt64)
	if capped := StampScriptStepOutputAt(ScriptStepEvent{Status: ScriptStepRunning, OutputAgeMS: &overflow}, received); capped.OutputAt != "2026-10-19T12:00:00.000Z" {
		t.Fatalf("overflowing age = %q", capped.OutputAt)
	}
}

// Publishing never holds up Setup: a control plane that takes seconds per
// event leaves the steps' timing alone, live updates coalesce to the newest,
// and the run waits only briefly for the rest to be sent.
func TestSetupStepEventsNeverWaitOnPublishing(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	slow := func(ctx context.Context, _ ScriptStepEvent) error {
		mu.Lock()
		calls++
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	started := time.Now()
	summary, _ := runSetupWithSteps(t, SetupRunner{Publish: slow, UpdateInterval: 10 * time.Millisecond, FinalWait: time.Second},
		[]string{"for i in 1 2 3 4 5; do echo $i; sleep 0.05; done", "echo two", "echo three"})
	elapsed := time.Since(started)
	mu.Lock()
	stalledCalls := calls
	mu.Unlock()
	close(release)
	if summary.Status != SetupSucceeded {
		t.Fatalf("summary = %+v", summary)
	}
	if elapsed > time.Second+2*time.Second {
		t.Fatalf("setup took %s with a stalled control plane", elapsed)
	}
	if stalledCalls != 1 {
		t.Fatalf("publish calls while the first was stalled = %d, want 1 (the rest wait, coalesced)", stalledCalls)
	}
}

// The publisher coalesces live updates to the newest, drops one that fails,
// retries a step's final event, and sends nothing more once the control plane
// refuses the event type.
func TestScriptStepPublisherCoalescesAndRetries(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	var mu sync.Mutex
	var sent []string
	failures := map[string]int{"final-1": 2, "live-2b": 1}
	publish := func(_ context.Context, event ScriptStepEvent) error {
		if event.Output == "live-1a" {
			<-gate
		}
		mu.Lock()
		defer mu.Unlock()
		if failures[event.Output] > 0 {
			failures[event.Output]--
			sent = append(sent, "failed:"+event.Output)
			return errors.New("network")
		}
		sent = append(sent, event.Output)
		return nil
	}
	publisher := newScriptStepPublisher(context.Background(), publish, time.Millisecond, time.Second)
	publisher.update(ScriptStepEvent{Step: 1, Output: "live-1a"})
	time.Sleep(20 * time.Millisecond)
	publisher.update(ScriptStepEvent{Step: 1, Output: "live-1b"})
	publisher.update(ScriptStepEvent{Step: 1, Output: "live-1c"})
	publisher.final(ScriptStepEvent{Step: 1, Output: "final-1"})
	publisher.update(ScriptStepEvent{Step: 2, Output: "live-2a"})
	publisher.update(ScriptStepEvent{Step: 2, Output: "live-2b"})
	close(gate)
	time.Sleep(50 * time.Millisecond)
	publisher.final(ScriptStepEvent{Step: 2, Output: "final-2"})
	publisher.close()
	mu.Lock()
	got := strings.Join(sent, " ")
	mu.Unlock()
	want := "live-1a failed:final-1 failed:final-1 final-1 failed:live-2b final-2"
	if got != want {
		t.Fatalf("sent %q, want %q", got, want)
	}

	calls := 0
	refused := newScriptStepPublisher(context.Background(), func(context.Context, ScriptStepEvent) error {
		calls++
		return ErrScriptStepsUnsupported
	}, time.Millisecond, time.Second)
	refused.update(ScriptStepEvent{Step: 1})
	time.Sleep(20 * time.Millisecond)
	refused.final(ScriptStepEvent{Step: 1})
	refused.final(ScriptStepEvent{Step: 2, Status: ScriptStepSkipped})
	refused.close()
	if calls != 1 {
		t.Fatalf("calls after the control plane refused the event type = %d, want 1", calls)
	}
}

// A control plane from before live logs refuses the event type: Setup runs
// and reports its outcome exactly as before, after one refused send.
func TestSetupWithAControlPlaneThatRefusesStepEvents(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	refuse := func(context.Context, ScriptStepEvent) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return ErrScriptStepsUnsupported
	}
	summary, _ := runSetupWithSteps(t, SetupRunner{Publish: refuse}, []string{"true", "exit 2", "true"})
	if summary.Status != SetupFailed || summary.Step != 2 || summary.ExitCode != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("sends = %d, want 1", calls)
	}
}

// A Setup that already ran (or has nothing to run) sends no step events.
func TestSetupStepEventsNotSentWhenSetupIsSkipped(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	runSetupWithSteps(t, SetupRunner{DataDir: dataDir}, []string{"true"})
	recorder := &stepRecorder{}
	workspace := t.TempDir()
	if _, ran := (SetupRunner{DataDir: dataDir, FirstStart: true, Publish: recorder.publish}).Run(
		context.Background(), []string{"true"}, workspace, scriptEnvironment(workspace)); ran {
		t.Fatal("setup ran twice")
	}
	if events := recorder.all(); len(events) != 0 {
		t.Fatalf("events for a skipped setup: %+v", events)
	}
}

// Credentials never leave the sandbox in a step's output or command: token
// shapes, values of secret-named variables (including AO's own token) and
// secret key/value pairs; terminal escapes are removed.
func TestSetupStepEventsRedactSecrets(t *testing.T) {
	secrets := map[string]string{
		"github":     "ghp_" + strings.Repeat("a1B2", 9),
		"github_pat": "github_pat_" + strings.Repeat("Z9y8", 8),
		"openai":     "sk-" + strings.Repeat("q7W", 9),
		"aws":        "AKIA" + strings.Repeat("ABCD", 4),
		"slack":      "xo" + "xb-" + strings.Repeat("12345-", 3) + "abcdef",
		"jwt":        "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"env_token":  "plain-opaque-token-value",
		"env_pass":   "hunter2hunter2",
		"env_key":    "0123456789abcdef-key",
		"worker":     "aow1.eyJvcmdJZCI6Im8ifQ.c2lnbmF0dXJlLXZhbHVl",
		"extra":      "session-scoped-opaque-42",
	}
	t.Setenv("MY_SERVICE_TOKEN", secrets["env_token"])
	t.Setenv("DB_PASSWORD", secrets["env_pass"])
	t.Setenv("STRIPE_KEY", secrets["env_key"])
	commands := []string{
		"echo " + secrets["github"] + " >/dev/null; printf '\\033[31mred\\033[0m\\n'",
		strings.Join([]string{
			"echo " + secrets["github_pat"], "echo " + secrets["openai"], "echo " + secrets["aws"],
			"echo " + secrets["slack"], "echo " + secrets["jwt"], `echo "$MY_SERVICE_TOKEN"`,
			`echo "pw=$DB_PASSWORD"`, `echo "$STRIPE_KEY"`, "echo " + secrets["worker"],
			"echo " + secrets["extra"], "echo api_key=visible-only-to-the-sandbox", "exit 1",
		}, "; "),
	}
	_, events := runSetupWithSteps(t, SetupRunner{
		UpdateInterval: 10 * time.Millisecond,
		Secrets:        func() []string { return []string{secrets["extra"]} },
	}, commands)
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for name, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("%s secret %q published in %s", name, secret, encoded)
		}
	}
	if strings.Contains(string(encoded), "visible-only-to-the-sandbox") {
		t.Fatal("a secret key/value pair was published")
	}
	last := finals(events)
	if last[1].Output != "red" || strings.Contains(string(encoded), `\u001b`) {
		t.Fatalf("step 1 output = %q; terminal escapes must be removed", last[1].Output)
	}
	if !strings.Contains(last[1].Command, "[REDACTED]") || !strings.Contains(last[2].Output, "[REDACTED]") {
		t.Fatalf("redaction markers missing: command %q, output %q", last[1].Command, last[2].Output)
	}
}

// A long output is cut to the last 16 KiB on a line start, never splitting a
// character, and marked truncated; progress redraws keep their last state.
func TestSetupStepEventsKeepTheTailOfLongOutput(t *testing.T) {
	t.Parallel()
	_, events := runSetupWithSteps(t, SetupRunner{},
		[]string{"i=0; while [ $i -lt 2000 ]; do echo \"ligne $i éèà\"; i=$((i+1)); done; printf '10%%\\r50%%\\r100%%\\n'; echo the-end"})
	final := finals(events)[1]
	if !final.OutputTruncated || len(final.Output) > ScriptStepOutputLimit || !utf8.ValidString(final.Output) {
		t.Fatalf("output (%d bytes, truncated %v) not bounded", len(final.Output), final.OutputTruncated)
	}
	if !strings.HasPrefix(final.Output, "ligne ") || !strings.HasSuffix(final.Output, "éèà\n100%\nthe-end") {
		t.Fatalf("output starts %q and ends %q", final.Output[:20], final.Output[len(final.Output)-40:])
	}
}

// Escaping can grow an output severalfold in JSON; the event still fits the
// control plane's body limit.
func TestScriptStepEventFitsTheEventBodyLimit(t *testing.T) {
	t.Parallel()
	event := fitScriptStepEvent(ScriptStepEvent{
		Phase: ScriptPhaseSetup, RunID: "run", Step: 1, Total: 1, Status: ScriptStepRunning,
		Output: strings.Repeat("<&>\"", ScriptStepOutputLimit/4),
	})
	encoded, _ := json.Marshal(EventRequest{Type: ScriptStepEventType, Payload: event})
	if len(encoded) > 24<<10 || !event.OutputTruncated || event.Output == "" {
		t.Fatalf("encoded %d bytes (truncated %v, %d bytes of output)", len(encoded), event.OutputTruncated, len(event.Output))
	}
}

// A secret cut in half where the raw tail starts is not published.
func TestScriptStepOutputDropsAPartialFirstLine(t *testing.T) {
	t.Parallel()
	output, truncated := scriptRedactor{}.scriptStepOutput("1B2a1B2a1B2a1B2a1B2a1B2\nnext line\n", true)
	if output != "next line" || !truncated {
		t.Fatalf("output = %q (truncated %v)", output, truncated)
	}
}

func TestValidScriptStepEvent(t *testing.T) {
	t.Parallel()
	zero, duration := 0, int64(5)
	valid := ScriptStepEvent{Phase: ScriptPhaseSetup, RunID: "r", Step: 1, Total: 2, Status: SetupSucceeded, ExitCode: &zero, DurationMS: &duration}
	if !ValidScriptStepEvent(valid) {
		t.Fatal("a finished step was refused")
	}
	for name, mutate := range map[string]func(*ScriptStepEvent){
		"phase":           func(e *ScriptStepEvent) { e.Phase = "teardown" },
		"run":             func(e *ScriptStepEvent) { e.RunID = " " },
		"step":            func(e *ScriptStepEvent) { e.Step = 0 },
		"total":           func(e *ScriptStepEvent) { e.Total = 0 },
		"status":          func(e *ScriptStepEvent) { e.Status = "done" },
		"no exit code":    func(e *ScriptStepEvent) { e.ExitCode = nil },
		"running exit":    func(e *ScriptStepEvent) { e.Status = ScriptStepRunning },
		"started at":      func(e *ScriptStepEvent) { e.StartedAt = "yesterday" },
		"negative length": func(e *ScriptStepEvent) { negative := int64(-1); e.DurationMS = &negative },
	} {
		event := valid
		mutate(&event)
		if ValidScriptStepEvent(event) {
			t.Fatalf("%s: %+v accepted", name, event)
		}
	}
}

// A running step's unfinished last line is never published: a credential
// printed part way is not yet whole for any pattern to recognize. The
// finished step's last line is published with a credential's start masked.
func TestScriptStepOutputHoldsBackAnUnfinishedLine(t *testing.T) {
	t.Parallel()
	redactor := newScriptRedactor([]string{"DB_PASSWORD=supersecret-value"}, nil)
	for _, raw := range []string{
		"DB_PASSWORD=supersecr",
		"ghp_abcdefghijklmno",
		"export TOKEN\nready\nDB_PASSWORD=supersecr",
		"ready\nghp_abcdefghijklmno",
	} {
		live, _ := redactor.liveStepOutput(raw, false)
		if strings.Contains(live, "supersecr") || strings.Contains(live, "ghp_") {
			t.Fatalf("live output of %q = %q; the unfinished line leaked", raw, live)
		}
		if strings.Contains(raw, "ready\n") && !strings.Contains(live, "ready") {
			t.Fatalf("live output of %q = %q; the finished lines are missing", raw, live)
		}
		final, _ := redactor.scriptStepOutput(raw, false)
		if strings.Contains(final, "supersecr") || strings.Contains(final, "ghp_") || !strings.HasSuffix(final, "[REDACTED]") {
			t.Fatalf("final output of %q = %q; the fragment must be masked", raw, final)
		}
	}
	// A value's start printed on its own, with nothing naming it a secret.
	if final, _ := redactor.scriptStepOutput("value: supersec", false); final != "value: [REDACTED]" {
		t.Fatalf("final output = %q", final)
	}
	// Ordinary unfinished lines are kept whole.
	for _, raw := range []string{"done", "50% complete", "risk-free"} {
		if final, _ := redactor.scriptStepOutput(raw, false); final != raw {
			t.Fatalf("final output of %q = %q", raw, final)
		}
	}
}

// End to end: a step that prints a credential part way and pauses never has
// the part published live, and its final event masks what it printed.
func TestSetupStepEventsNeverPublishAPartialSecret(t *testing.T) {
	t.Setenv("DB_PASSWORD", "supersecret-value")
	_, events := runSetupWithSteps(t, SetupRunner{UpdateInterval: 10 * time.Millisecond}, []string{
		"echo started; printf 'DB_PASSWORD=supersecr'; sleep 0.3; printf 'et-value\\n'; echo done",
		"echo started; printf 'ghp_abcdefghijklmno'; sleep 0.3",
	})
	sawStarted := false
	for _, event := range events {
		if strings.Contains(event.Output, "supersecr") || strings.Contains(event.Output, "ghp_") {
			t.Fatalf("event %+v published a partial secret", event)
		}
		if event.Status == ScriptStepRunning && strings.Contains(event.Output, "started") {
			sawStarted = true
		}
	}
	if !sawStarted {
		t.Fatalf("events %+v: the finished lines must still be published live", events)
	}
	last := finals(events)
	if last[1].Output != "started\nDB_PASSWORD=[REDACTED]\ndone" || last[2].Output != "started\n[REDACTED]" {
		t.Fatalf("final outputs = %q, %q", last[1].Output, last[2].Output)
	}
}

// The redaction every Setup and Cleanup step gets, on the worker and the
// control plane alike: secret-named pairs in every common form, token shapes,
// and private key bodies, without masking values that are not secrets.
func TestScriptRedactionPatterns(t *testing.T) {
	t.Parallel()
	privateKeyBody := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\nVJTUt9Us8cKjMzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDu\n"
	cases := []struct {
		name, text, want string
	}{
		{"env assignment", "DB_PASSWORD=hunter2", "DB_PASSWORD=[REDACTED]"},
		{"export", "export MY_SERVICE_TOKEN='abc def'", "export MY_SERVICE_TOKEN=[REDACTED]"},
		{"colon", "Api-Key: abc123", "Api-Key: [REDACTED]"},
		{"yaml", "  client_secret: s3cr3t", "  client_secret: [REDACTED]"},
		{"aws secret key", "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG", "AWS_SECRET_ACCESS_KEY=[REDACTED]"},
		{"short pass", "MYSQL_PASS=pw", "MYSQL_PASS=[REDACTED]"},
		{"auth", "NPM_AUTH=dXNlcjpwYXNz", "NPM_AUTH=[REDACTED]"},
		{"credential", "GOOGLE_CREDENTIALS={x}", "GOOGLE_CREDENTIALS=[REDACTED]"},
		{"private", "PRIVATE_THING=abc", "PRIVATE_THING=[REDACTED]"},
		{"json", `{"user": "admin", "password": "hunter2"}`, `{"user": "admin", "password": [REDACTED]}`},
		{"json escaped", `"{\"user\":\"admin\",\"password\":\"hunter2\"}"`, `"{\"user\":\"admin\",\"password\":\"[REDACTED]\"}"`},
		{"secrets manager", `"SecretString": "{\"username\":\"admin\",\"password\":\"p4ss\"}",`, `"SecretString": [REDACTED],`},
		{"secrets manager text", `{"username":"admin","password":"p4ss"}`, `{"username":"admin","password":[REDACTED]}`},
		{"npm token", "npm_" + strings.Repeat("a1B2", 9), "[REDACTED]"},
		{"google key", "key AIza" + strings.Repeat("Sy0_", 9), "key [REDACTED]"},
		{"sendgrid", "SG." + strings.Repeat("ab_cd", 5) + "." + strings.Repeat("Ef-gh", 9), "[REDACTED]"},
		{"slack bot", "xo" + "xb-" + strings.Repeat("1234-", 3) + "abcdef", "[REDACTED]"},
		{"slack user", "xo" + "xp-" + strings.Repeat("1234-", 3) + "abcdef", "[REDACTED]"},
		{"gitlab", "glpat-" + strings.Repeat("x1Y2", 6), "[REDACTED]"},
		{"github oauth", "gho_" + strings.Repeat("a1B2", 9), "[REDACTED]"},
		{"private key", "-----BEGIN RSA PRIV" + "ATE KEY-----\n" + privateKeyBody + "-----END RSA PRIV" + "ATE KEY-----\nnext", "[REDACTED]\nnext"},
		{"colour codes around a pair", "\x1b[1mNPM_AUTH\x1b[0m=\x1b[32mabc123\x1b[0m done", "NPM_AUTH=[REDACTED] done"},
		{"private key end only", "...\n" + privateKeyBody + "-----END PRIVATE KEY-----\nnext", "...\n[REDACTED]\nnext"},
		{"docker login -p", "docker login -u ci -p s3cret registry.io", "docker login -u ci -p [REDACTED] registry.io"},
		{"sshpass then ssh -p", "sshpass -p s3cret ssh -p 22 host", "sshpass -p [REDACTED] ssh -p 22 host"},
		{"mysql -p attached", "mysql -uroot -ps3cret db", "mysql -uroot -p[REDACTED] db"},
		{"redis-cli -a", "redis-cli -a s3cret ping", "redis-cli -a [REDACTED] ping"},
		{"curl -u", "curl -u admin:s3cret https://x", "curl -u admin:[REDACTED] https://x"},
		{"yaml block", "private_key: |\n  abc\n\n  def\nnext: 1", "private_key: [REDACTED]\n  [REDACTED]\nnext: 1"},
		{"kubernetes secret", "kind: Secret\ndata:\n  host: ZGI=\n  port: NTQzMg==\ntype: Opaque",
			"kind: Secret\ndata:\n  host: [REDACTED]\n  port: [REDACTED]\ntype: Opaque"},
		{"token user", "https://abcdef0123456789abcd@github.com/o/r", "https://[REDACTED]@github.com/o/r"},
		{"sas", "b?sv=2022&sig=abc%3D&se=1", "b?sv=2022&sig=[REDACTED]&se=1"},
		{"argument list", `["--token","abc","--verbose"]`, `["--token","[REDACTED]","--verbose"]`},
		{"argument list flag after", `["--token","--verbose"]`, `["--token","--verbose"]`},
		{"python string", `{'password': u'abc'}`, `{'password': u'[REDACTED]'}`},
		{"value to the blank", "DB_PASSWORD=ab;cd next", "DB_PASSWORD=[REDACTED] next"},
		{"flag after an assignment", "ARGS=--password abc", "ARGS=--password [REDACTED]"},
		// Not secrets.
		{"docker run -p", "docker run -p 8080:80 -u 1000:1000 nginx", "docker run -p 8080:80 -u 1000:1000 nginx"},
		{"ssh -p", "ssh -p 22 deploy@host", "ssh -p 22 deploy@host"},
		{"scp -P", "scp -P 2222 f host:", "scp -P 2222 f host:"},
		{"mysql -P", "mysql -h db -P 3306 -u root", "mysql -h db -P 3306 -u root"},
		{"docker login from stdin", "docker login --password-stdin -u ci registry.io", "docker login --password-stdin -u ci registry.io"},
		{"curl -u without a password", "curl -u admin https://x", "curl -u admin https://x"},
		{"url users", "git clone https://git@github.com/o/r https://deploy@host/x", "git clone https://git@github.com/o/r https://deploy@host/x"},
		{"config map", "kind: ConfigMap\ndata:\n  host: db", "kind: ConfigMap\ndata:\n  host: db"},
		{"sas without signature", "b?sv=2022-11-02&sp=r", "b?sv=2022-11-02&sp=r"},
		{"stripe publishable", "pk_live_abcdefghijklmnop", "pk_live_abcdefghijklmnop"},
		{"ssh agent socket", "SSH_AUTH_SOCK=/tmp/ssh-x/agent.1", "SSH_AUTH_SOCK=/tmp/ssh-x/agent.1"},
		{"key path", "KEY_PATH=/etc/ssl/key.pem", "KEY_PATH=/etc/ssl/key.pem"},
		{"pass file", "PASS_FILE=/run/pass.txt", "PASS_FILE=/run/pass.txt"},
		{"auth dir", "AUTH_DIR=/var/lib/auth", "AUTH_DIR=/var/lib/auth"},
		{"auth url", "AUTH_URL=https://auth.example.com", "AUTH_URL=https://auth.example.com"},
		{"git author", "GIT_AUTHOR_NAME=Jane", "GIT_AUTHOR_NAME=Jane"},
		{"tests passed", "Tests passed: 42", "Tests passed: 42"},
		{"plain", "HOME=/root ready", "HOME=/root ready"},
	}
	for _, test := range cases {
		if got := RedactScriptOutput(test.text); got != test.want {
			t.Errorf("%s: RedactScriptOutput(%q) = %q, want %q", test.name, test.text, got, test.want)
		}
	}
}

// The control plane applies the same redaction to a worker's step event and
// to a Cleanup hook's output, which reach it without the sandbox's values.
func TestControlPlaneRedactsLikeTheWorker(t *testing.T) {
	t.Parallel()
	text := `DB_PASSWORD=hunter2 {\"api_token\":\"abc\"} npm_` + strings.Repeat("a1B2", 9)
	sanitized := SanitizeScriptStepEvent(ScriptStepEvent{Output: text, Command: "echo DB_PASSWORD=hunter2"})
	if strings.Contains(sanitized.Output, "hunter2") || strings.Contains(sanitized.Output, `abc\"`) ||
		strings.Contains(sanitized.Output, "a1B2") || strings.Contains(sanitized.Command, "hunter2") {
		t.Fatalf("sanitized = %+v", sanitized)
	}
	nonce := NewScriptMarkerNonce()
	output, code := runCleanupScript(t, CleanupCommandsScript([]string{`echo "DB_PASSWORD=hunter2"; echo '{"password": "x"}'`}, nonce), t.TempDir())
	events := CleanupStepEvents(output, CleanupRun{RunID: "cleanup-1", Nonce: nonce, ExitCode: code, StartedAt: time.Now()})
	if len(events) != 1 || events[0].Output != "DB_PASSWORD=[REDACTED]\n{\"password\": [REDACTED]}" {
		t.Fatalf("events = %+v", events)
	}
}

// Live updates go every two seconds, and every five once a step has run two
// minutes.
func TestLiveUpdateInterval(t *testing.T) {
	t.Parallel()
	for elapsed, want := range map[time.Duration]time.Duration{
		0: 2 * time.Second, 119 * time.Second: 2 * time.Second, 2 * time.Minute: 5 * time.Second, time.Hour: 5 * time.Second,
	} {
		if got := liveUpdateInterval(elapsed); got != want {
			t.Fatalf("liveUpdateInterval(%s) = %s, want %s", elapsed, got, want)
		}
	}
}

// A worker that is stopping still reports how its steps ended: its queued
// final events are sent on a context that is not cancelled with the worker's,
// live updates are dropped, and close waits for the finals, but no longer
// than the final wait.
func TestScriptStepPublisherSendsFinalsAfterTheWorkerStops(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	var mu sync.Mutex
	var sent []string
	publish := func(ctx context.Context, event ScriptStepEvent) error {
		if event.Output == "live-1" {
			<-gate
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, event.Output)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	publisher := newScriptStepPublisher(ctx, publish, time.Millisecond, 5*time.Second)
	publisher.update(ScriptStepEvent{Step: 1, Output: "live-1"})
	time.Sleep(20 * time.Millisecond)
	publisher.final(ScriptStepEvent{Step: 1, Output: "final-1"})
	publisher.update(ScriptStepEvent{Step: 2, Output: "live-2"})
	publisher.final(ScriptStepEvent{Step: 2, Output: "final-2"})
	publisher.final(ScriptStepEvent{Step: 3, Status: ScriptStepSkipped, Output: "skipped-3"})
	cancel()
	close(gate)
	publisher.close()
	mu.Lock()
	got := strings.Join(sent, " ")
	mu.Unlock()
	// live-1 was in flight on the worker's context and ended with it; live-2
	// was dropped.
	if got != "final-1 final-2 skipped-3" {
		t.Fatalf("sent %q after the worker stopped; want every final event and no live update", got)
	}

	// A control plane that never answers holds close only for the final wait.
	stuck := newScriptStepPublisher(context.Background(), func(ctx context.Context, _ ScriptStepEvent) error {
		<-ctx.Done()
		return ctx.Err()
	}, time.Millisecond, 200*time.Millisecond)
	stuck.final(ScriptStepEvent{Step: 1})
	stuck.final(ScriptStepEvent{Step: 2})
	started := time.Now()
	stuck.close()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("close waited %s for a control plane that never answers", elapsed)
	}
}
