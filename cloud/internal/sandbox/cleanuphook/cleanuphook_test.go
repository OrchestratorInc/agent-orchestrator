package cleanuphook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// rootShims puts stand-ins for stat and runuser first on PATH, so a hook
// command made for root runs as an ordinary test user: stat names owner as
// the hook's owner, and runuser prints the user it was asked for and runs
// the command.
func rootShims(t *testing.T, owner string) []string {
	t.Helper()
	bin := t.TempDir()
	shims := map[string]string{
		"stat":    "#!/bin/sh\necho " + shellQuote(owner) + "\n",
		"runuser": "#!/bin/sh\n[ \"$1\" = -u ] && [ \"$3\" = -- ] || { echo bad runuser call: \"$@\"; exit 90; }\necho \"runuser:$2\"\nshift 3\nexec \"$@\"\n",
	}
	for name, content := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runHookCommand(t *testing.T, command string, environment []string) string {
	t.Helper()
	run := exec.Command("sh", "-c", command)
	run.Env = environment
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("hook command: %v: %s", err, output)
	}
	return string(output)
}

// Run as root, the hook command runs the hook as its owner through runuser,
// never as root, and reports skipped when there is no hook.
func TestCommandAsRootRunsTheHookAsItsOwner(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	hook := filepath.Join(t.TempDir(), "worker", worker.CleanupHookFile)
	command := Command(HookWord(hook), 10*time.Second, "", Options{Runner: AsRoot})
	if strings.Contains(command, "sudo") || strings.Contains(command, "[ -O") {
		t.Fatalf("a root hook command must reach the owner only through runuser: %s", command)
	}
	if output := runHookCommand(t, command, rootShims(t, "ao-worker")); !strings.Contains(output, Marker+":"+Skipped) {
		t.Fatalf("no hook: %q", output)
	}
	if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho stopping stack\nexit 4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := runHookCommand(t, command, rootShims(t, "ao-worker"))
	if !strings.Contains(output, "runuser:ao-worker\nstopping stack") || !strings.Contains(output, Marker+":4") {
		t.Fatalf("hook output: %q", output)
	}
	for _, owner := range []string{"root", ""} {
		if output := runHookCommand(t, command, rootShims(t, owner)); !strings.Contains(output, Marker+":"+Skipped) ||
			strings.Contains(output, "stopping stack") {
			t.Fatalf("a hook owned by %q ran: %q", owner, output)
		}
	}
}

// With the project's current Cleanup, the root hook command installs it as
// the hook's owner and the installed hook runs it in the session's workspace.
func TestCommandAsRootRunsTheCurrentCleanup(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	for _, tool := range []string{"base64", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	dataDir, workspace := t.TempDir(), t.TempDir()
	environment := worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}
	if err := worker.WriteCleanupHook(dataDir, []string{"echo installed-cleanup"}, workspace, environment); err != nil {
		t.Fatal(err)
	}
	current := []string{`echo "current-cleanup $AO_SESSION_ID"`}
	nonce := worker.NewScriptMarkerNonce()
	payload, fits := CommandsPayload(current, nonce)
	if !fits {
		t.Fatal("a one-line Cleanup does not fit the payload limit")
	}
	command := Command(HookWord(filepath.Join(dataDir, worker.CleanupHookFile)), 10*time.Second, payload, Options{Runner: AsRoot})
	output := runHookCommand(t, command, rootShims(t, "ao-worker"))
	printed, status, ok := Split(output)
	if !ok || status != "0" || strings.Contains(printed, "installed-cleanup") {
		t.Fatalf("hook output = %q", output)
	}
	events := worker.CleanupStepEvents(printed, worker.CleanupRun{RunID: "r", Nonce: nonce, Commands: current})
	if len(events) != 1 || events[0].Status != worker.SetupSucceeded || events[0].Output != "current-cleanup session-1" {
		t.Fatalf("events = %+v", events)
	}
}

func TestSplitReadsTheStatusAfterTheMarker(t *testing.T) {
	t.Parallel()
	printed, status, ok := Split("tearing down\n\n" + Marker + ":3\n")
	if !ok || printed != "tearing down\n\n" || status != "3" || ExitCode(status) != 3 {
		t.Fatalf("split = %q %q %v", printed, status, ok)
	}
	if _, status, ok := Split("x\r\n" + Marker + ":" + Skipped + "\r\n"); !ok || status != Skipped || ExitCode(status) != -1 {
		t.Fatalf("skipped status = %q %v", status, ok)
	}
	if printed, _, ok := Split("cut off"); ok || printed != "cut off" {
		t.Fatalf("no marker = %q %v", printed, ok)
	}
}

func TestNewPlanFollowsTheProjectsCurrentCleanup(t *testing.T) {
	t.Parallel()
	if _, ok := NewPlan(sandbox.WithCleanupCommands(context.Background(), nil)); ok {
		t.Fatal("a project without Cleanup now planned a run")
	}
	plan, ok := NewPlan(context.Background())
	if !ok || plan.Override != "" || plan.Steps.RunID == "" || plan.Steps.Nonce != "" {
		t.Fatalf("installed-commands plan = %+v", plan)
	}
	plan, ok = NewPlan(sandbox.WithCleanupCommands(context.Background(), []string{"make down"}))
	if !ok || plan.Override == "" || plan.Steps.Nonce == "" || len(plan.Steps.Commands) != 1 {
		t.Fatalf("current-Cleanup plan = %+v", plan)
	}
	huge := make([]string, 20)
	for index := range huge {
		var step strings.Builder
		for range 256 {
			step.WriteString(worker.NewScriptMarkerNonce())
		}
		huge[index] = step.String()
	}
	plan, ok = NewPlan(sandbox.WithCleanupCommands(context.Background(), huge))
	if !ok || plan.Override != "" || plan.Steps.Commands != nil {
		t.Fatalf("an oversized Cleanup must fall back to the installed commands: override %d bytes", len(plan.Override))
	}
}

// A run's budget covers the time limits of the steps it may run: the current
// Cleanup's, and the worker's installed ones the hook falls back to.
func TestNewPlanBudgetCoversTheStepsTimeLimits(t *testing.T) {
	t.Parallel()
	plan, _ := NewPlan(context.Background())
	if plan.Budget != Budget {
		t.Fatalf("unknown commands budget = %s, want %s", plan.Budget, Budget)
	}
	current := []string{"# ao-timeout: 45m\nmake down", "echo done"}
	plan, _ = NewPlan(sandbox.WithCleanupCommands(context.Background(), current))
	if plan.Budget != 55*time.Minute+20*time.Second {
		t.Fatalf("current budget = %s", plan.Budget)
	}
	installed := []string{"# ao-timeout: 90m\nmake down"}
	plan, _ = NewPlan(sandbox.WithInstalledCleanupCommands(sandbox.WithCleanupCommands(context.Background(), current), installed))
	if plan.Budget != 90*time.Minute+10*time.Second {
		t.Fatalf("installed budget = %s, want the installed commands' larger one", plan.Budget)
	}
}

// Tracker runs a key's Cleanup once, Await holds a Delete only while the run
// is going, and a run that never reports stops holding it after its limit.
func TestTrackerRunsOnceAndNeverBlocksForever(t *testing.T) {
	t.Parallel()
	var tracker Tracker
	var calls atomic.Int32
	release := make(chan struct{})
	noSkip := func(reason string) { t.Errorf("skipped: %s", reason) }
	run := tracker.Start(context.Background(), "vm-1", "org-1", 0, time.Minute, func() { calls.Add(1); <-release }, noSkip)
	if again := tracker.Start(context.Background(), "vm-1", "org-1", 0, time.Minute, func() { calls.Add(1) }, noSkip); again != run {
		t.Fatal("a second Start for the key started another run")
	}
	if run.Await(context.Background(), 20*time.Millisecond) {
		t.Fatal("Await reported a running Cleanup finished")
	}
	close(release)
	if !run.Await(context.Background(), time.Second) || calls.Load() != 1 {
		t.Fatalf("finished run: calls = %d", calls.Load())
	}
	if found, ok := tracker.Lookup("vm-1"); !ok || found != run {
		t.Fatal("a finished run is forgotten before RecordTTL")
	}

	wedged := make(chan struct{})
	defer close(wedged)
	stale := tracker.Start(context.Background(), "vm-2", "org-1", 0, -time.Hour, func() { <-wedged }, noSkip)
	if !stale.Await(context.Background(), 10*time.Millisecond) {
		t.Fatal("a run past its limit still holds the deletion")
	}
}

type recorder struct {
	mu     sync.Mutex
	events []worker.ScriptStepEvent
	stages []sandbox.StageReport
}

func (r *recorder) context() context.Context {
	ctx := sandbox.WithStageObserver(context.Background(), func(_ context.Context, report sandbox.StageReport) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.stages = append(r.stages, report)
	})
	return sandbox.WithSessionEventRecorder(ctx, func(_ context.Context, eventType string, payload any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if event, ok := payload.(worker.ScriptStepEvent); ok && eventType == worker.ScriptStepEventType {
			r.events = append(r.events, event)
		}
	})
}

// Report appends a run's steps (phase cleanup) and reports a failure with
// its output and hint; a run with no hook or no status appends nothing.
func TestReportAppendsStepsAndFinishesTheStage(t *testing.T) {
	t.Parallel()
	report := func(printed, status string, err error, steps worker.CleanupRun) *recorder {
		r := &recorder{}
		ctx := r.context()
		steps.StartedAt = time.Now()
		Report(ctx, sandbox.BeginStage(ctx, Stage), map[string]string{"provider_id": "vm-1"}, steps, printed, status, err, "test")
		return r
	}
	commands := []string{"make down", "docker compose down"}
	failed := report("@@ao-step-00000000000000aa 1 2 start 0 / make down\nstopped\n@@ao-step-00000000000000aa 1 2 end 0 10\n"+
		"@@ao-step-00000000000000aa 2 2 start 0 / docker compose down\nno such service\n"+
		"cleanup step 2 failed: exit status 3\n@@ao-step-00000000000000aa 2 2 end 3 20\n", "3", nil,
		worker.CleanupRun{RunID: "run-1", Nonce: "00000000000000aa", Commands: commands})
	if len(failed.events) != 2 || failed.events[0].Status != worker.SetupSucceeded || failed.events[1].Status != worker.SetupFailed {
		t.Fatalf("events = %+v", failed.events)
	}
	for _, event := range failed.events {
		if event.Phase != worker.ScriptPhaseCleanup || event.RunID != worker.CleanupRunID("00000000000000aa") || !worker.ValidScriptStepEvent(event) {
			t.Fatalf("event = %+v", event)
		}
	}
	if stage := failed.stages[0]; stage.Stage != Stage || stage.Err == nil || !strings.Contains(stage.Err.Error(), "exited 3") ||
		!strings.Contains(stage.Attrs["output"], "no such service") || stage.Attrs["provider_id"] != "vm-1" {
		t.Fatalf("stage = %+v", stage)
	}

	skipped := report("", Skipped, nil, worker.CleanupRun{RunID: "run-2"})
	if len(skipped.events) != 0 || skipped.stages[0].Err != nil || skipped.stages[0].Attrs["result"] != Skipped {
		t.Fatalf("skipped run: events %+v, stage %+v", skipped.events, skipped.stages[0])
	}
	// A run the hook command skipped with a reason says so on every step it
	// knows, whether the current Cleanup or the installed commands.
	for name, steps := range map[string]worker.CleanupRun{
		"current":   {RunID: "run-5", Commands: commands},
		"installed": {RunID: "run-5", KnownCommands: commands},
	} {
		noSudo := report("", Skipped+" "+skipNoSudo, nil, steps)
		if len(noSudo.events) != 2 || noSudo.stages[0].Err != nil || noSudo.stages[0].Attrs["result"] != Skipped ||
			noSudo.stages[0].Attrs["skip_reason"] != skipNoSudo {
			t.Fatalf("%s: skipped run with a reason: events %+v, stage %+v", name, noSudo.events, noSudo.stages[0])
		}
		for index, event := range noSudo.events {
			if event.Status != worker.ScriptStepSkipped || event.Command != commands[index] || event.Phase != worker.ScriptPhaseCleanup ||
				event.Hint != SkippedReason(skipNoSudo) || !strings.Contains(event.Hint, "sudo") || !worker.ValidScriptStepEvent(event) {
				t.Fatalf("%s: skipped step %d = %+v", name, index+1, event)
			}
		}
	}
	lost := report("partial", "", errors.New(`freestyle api returned 400: "timeout_ms cannot exceed 300000" token=abc123`),
		worker.CleanupRun{RunID: "run-3", Commands: commands})
	if lost.stages[0].Err == nil || !strings.Contains(lost.stages[0].Err.Error(), "did not report") || len(lost.events) != 2 {
		t.Fatalf("lost run: events %+v, stage %+v", lost.events, lost.stages[0])
	}
	for index, event := range lost.events {
		if event.Status != worker.ScriptStepSkipped || event.Command != commands[index] || event.RunID != "run-3" ||
			!strings.Contains(event.Hint, "could not run") || !strings.Contains(event.Hint, "timeout_ms cannot exceed 300000") ||
			strings.Contains(event.Hint, "abc123") || !worker.ValidScriptStepEvent(event) {
			t.Fatalf("lost run step %d = %+v", index+1, event)
		}
	}
	// Output that came back in part reports the steps it shows.
	cut := report("@@ao-step-00000000000000aa 1 2 start 0 / make down\nstopped\n@@ao-step-00000000000000aa 1 2 end 0 10\n"+
		"@@ao-step-00000000000000aa 2 2 start 0 / docker compose down\nstopp", "", errors.New("connection reset"),
		worker.CleanupRun{RunID: "run-4", Nonce: "00000000000000aa", Commands: commands})
	if len(cut.events) != 2 || cut.events[0].Status != worker.SetupSucceeded || cut.events[1].Status != worker.SetupFailed {
		t.Fatalf("cut run: events %+v", cut.events)
	}
}

// ReportNotRun reports each of the current Cleanup's steps as skipped, with
// the reason, and the stage as skipped and failed.
func TestReportNotRunSkipsEveryStep(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	ctx := r.context()
	steps := worker.CleanupRun{RunID: "run-1", Commands: []string{"make down", domain.TimedScriptCommand(45, domain.RepoScriptCommand("api", "npm run clean"))}}
	ReportNotRun(ctx, sandbox.BeginStage(ctx, Stage), map[string]string{}, steps, "The sandbox did not resume.", errors.New("resume failed"))
	if len(r.events) != 2 {
		t.Fatalf("events = %+v", r.events)
	}
	for index, event := range r.events {
		if !worker.ValidScriptStepEvent(event) || event.Status != worker.ScriptStepSkipped || event.Phase != worker.ScriptPhaseCleanup ||
			event.Step != index+1 || event.Total != 2 || event.Hint != "The sandbox did not resume." {
			t.Fatalf("event %d = %+v", index, event)
		}
	}
	if r.events[1].Repository != "api" || r.events[1].Command != "npm run clean" || r.events[1].TimeoutMinutes != 45 ||
		r.events[0].TimeoutMinutes != 10 {
		t.Fatalf("repository step = %+v (first %+v)", r.events[1], r.events[0])
	}
	if stage := r.stages[0]; stage.Err == nil || stage.Attrs["result"] != Skipped {
		t.Fatalf("stage = %+v", stage)
	}
}

// A failed run's stage output and its step events are redacted alike.
func TestReportRedactsTheStageOutputLikeTheSteps(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	ctx := r.context()
	printed := "@@ao-step-00000000000000aa 1 1 start 0 / make down\nAPI_KEY: abc123\n{\\\"password\\\":\\\"hunter2\\\"}\n" +
		"cleanup step 1 failed: exit status 3\n@@ao-step-00000000000000aa 1 1 end 3 20\n"
	Report(ctx, sandbox.BeginStage(ctx, Stage), map[string]string{}, worker.CleanupRun{
		RunID: "run-1", Nonce: "00000000000000aa", StartedAt: time.Now(),
	}, printed, "3", nil, "test")
	stage := r.stages[0].Attrs["output"]
	if len(r.events) != 1 || strings.Contains(r.events[0].Output, "abc123") || strings.Contains(r.events[0].Output, "hunter2") ||
		strings.Contains(stage, "abc123") || strings.Contains(stage, "hunter2") || !strings.Contains(stage, "[REDACTED]") {
		t.Fatalf("stage output %q, events %+v", stage, r.events)
	}
}

// At most MaxRuns runs go at once for the provider and MaxOrgRuns for one
// organization; the rest wait for a slot, and one that waits past QueueWait
// is skipped with QueueFullReason. A waiting run holds its Delete.
func TestTrackerBoundsConcurrentRuns(t *testing.T) {
	t.Parallel()
	tracker := Tracker{MaxRuns: 3, MaxOrgRuns: 2, QueueWait: time.Hour}
	var mu sync.Mutex
	running, peak, peakOrg := map[string]int{}, 0, 0
	release := make(chan struct{})
	started := make(chan string, 10)
	start := func(key, org string) *Run {
		return tracker.Start(context.Background(), key, org, 0, time.Minute, func() {
			mu.Lock()
			running[org]++
			total := 0
			for _, count := range running {
				total += count
			}
			peak, peakOrg = max(peak, total), max(peakOrg, running[org])
			mu.Unlock()
			started <- key
			<-release
			mu.Lock()
			running[org]--
			mu.Unlock()
		}, func(reason string) { t.Errorf("%s skipped: %s", key, reason) })
	}
	runs := []*Run{start("a1", "a"), start("a2", "a"), start("a3", "a"), start("b1", "b"), start("c1", "c")}
	seen := map[string]bool{}
	for range 3 {
		select {
		case key := <-started:
			seen[key] = true
		case <-time.After(5 * time.Second):
			t.Fatal("the first runs did not start")
		}
	}
	select {
	case key := <-started:
		t.Fatalf("%s started beyond the provider's 3 slots", key)
	case <-time.After(100 * time.Millisecond):
	}
	if seen["a3"] && seen["a1"] && seen["a2"] {
		t.Fatal("three runs of one organization went at once")
	}
	if runs[2].Await(context.Background(), 10*time.Millisecond) && !seen["a3"] {
		t.Fatal("a waiting run let its Delete go ahead")
	}
	close(release)
	for _, run := range runs {
		if !run.Await(context.Background(), 5*time.Second) {
			t.Fatal("a queued run never ran")
		}
	}
	if peak != 3 || peakOrg != 2 {
		t.Fatalf("peak runs = %d (want 3), peak for one organization = %d (want 2)", peak, peakOrg)
	}

	full := Tracker{MaxRuns: 1, QueueWait: 50 * time.Millisecond}
	hold := make(chan struct{})
	defer close(hold)
	holding := make(chan struct{})
	full.Start(context.Background(), "x1", "", 0, time.Minute, func() { close(holding); <-hold }, func(string) {})
	<-holding
	skipped := make(chan string, 1)
	queued := full.Start(context.Background(), "x2", "", 0, time.Minute, func() { t.Error("x2 ran without a slot") }, func(reason string) { skipped <- reason })
	select {
	case reason := <-skipped:
		if reason != QueueFullReason || !queued.Await(context.Background(), time.Second) {
			t.Fatalf("skip reason %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a run that waited past QueueWait was not skipped")
	}
}

// A run waiting behind its organization's own runs waits as long as they may
// still take, not just its own queue wait, and tells the deletion how much
// longer that is (sandbox.ReportCleanupQueueHold); one that still gets no
// slot says its organization's share was what it waited for.
func TestTrackerWaitsForTheOrganizationsRunsAhead(t *testing.T) {
	t.Parallel()
	tracker := Tracker{MaxRuns: 16, MaxOrgRuns: 2, QueueWait: 50 * time.Millisecond}
	release := make(chan struct{})
	holding := make(chan struct{}, 2)
	noSkip := func(reason string) { t.Errorf("skipped: %s", reason) }
	for _, key := range []string{"a1", "a2"} {
		tracker.Start(context.Background(), key, "a", 0, 60*time.Minute, func() { holding <- struct{}{}; <-release }, noSkip)
	}
	for range 2 {
		select {
		case <-holding:
		case <-time.After(5 * time.Second):
			t.Fatal("the organization's first runs did not start")
		}
	}
	holdOf := func(key string, limit time.Duration, fn func(), skip func(string)) (*Run, time.Duration) {
		var hold time.Duration
		ctx := sandbox.WithCleanupQueueHold(context.Background(), func(d time.Duration) { hold = d })
		return tracker.Start(ctx, key, "a", 0, limit, fn, skip), hold
	}
	ran := make(chan string, 2)
	third, hold := holdOf("a3", 30*time.Minute, func() { ran <- "a3" }, noSkip)
	near := func(got, want time.Duration) bool { return got <= want && got > want-time.Minute }
	if !near(hold, 60*time.Minute) {
		t.Fatalf("a3 hold = %s, want the 60 minutes its organization's runs may still take", hold)
	}
	// a3 takes the first slot to free and a4 the second; each later run waits
	// for the slot that frees first after the runs ahead of it, at most
	// sandbox.MaxCleanupBudget.
	for _, next := range []struct {
		key         string
		limit, hold time.Duration
	}{
		{"a4", 10 * time.Minute, 60 * time.Minute},
		{"a5", 120 * time.Minute, 70 * time.Minute},
		{"a6", 120 * time.Minute, 90 * time.Minute},
		{"a7", 10 * time.Minute, sandbox.MaxCleanupBudget},
	} {
		fn := func() {}
		if next.key == "a4" {
			fn = func() { ran <- "a4" }
		}
		if _, hold = holdOf(next.key, next.limit, fn, func(string) {}); !near(hold, next.hold) {
			t.Fatalf("%s hold = %s, want %s", next.key, hold, next.hold)
		}
	}
	// Another organization's run never waits for this one's.
	if _, other := (func() (*Run, time.Duration) {
		var got time.Duration
		ctx := sandbox.WithCleanupQueueHold(context.Background(), func(d time.Duration) { got = d })
		return tracker.Start(ctx, "b1", "b", 0, time.Minute, func() {}, noSkip), got
	})(); other != 0 {
		t.Fatalf("b1 hold = %s, want none", other)
	}
	// Past its own 50ms queue wait, a3 is still waiting and still holds its Delete.
	if third.Await(context.Background(), 200*time.Millisecond) {
		t.Fatal("a run waiting behind its organization's runs was skipped after its own queue wait")
	}
	close(release)
	for _, want := range []string{"a3", "a4"} {
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never ran once a slot freed", want)
		}
	}

	// A run whose organization still holds its share when it gives up says so.
	short := Tracker{MaxRuns: 16, MaxOrgRuns: 2, QueueWait: 50 * time.Millisecond}
	hold2 := make(chan struct{})
	defer close(hold2)
	started := make(chan struct{}, 2)
	for _, key := range []string{"c1", "c2"} {
		short.Start(context.Background(), key, "c", 0, time.Millisecond, func() { started <- struct{}{}; <-hold2 }, func(string) {})
	}
	<-started
	<-started
	skipped := make(chan string, 1)
	short.Start(context.Background(), "c3", "c", 0, time.Minute, func() { t.Error("c3 ran without a slot") }, func(reason string) { skipped <- reason })
	select {
	case reason := <-skipped:
		if reason != OrgQueueFullReason(2) || reason != "This organization already has 2 Cleanups running, so this Cleanup step did not run before the sandbox was deleted." {
			t.Fatalf("skip reason %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a run that waited past its queue wait was not skipped")
	}
}

// Skipper reports a run that never ran: each step skipped with the reason.
func TestSkipperReportsTheRunAsNotRun(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	Skipper(r.context(), map[string]string{"provider_id": "vm-1"}, worker.CleanupRun{RunID: "run-1", Commands: []string{"make down"}})(QueueFullReason)
	if len(r.events) != 1 || r.events[0].Status != worker.ScriptStepSkipped || r.events[0].Hint != QueueFullReason ||
		r.stages[0].Attrs["result"] != Skipped || r.stages[0].Err == nil {
		t.Fatalf("events %+v, stages %+v", r.events, r.stages)
	}
}

// Freestyle and Docker stop the hook owner's processes (the worker and its
// agent) before the hook runs; Coder, whose login user may run the Coder
// agent carrying the command, stops nothing.
func TestCommandStopsTheOwnersProcessesFirst(t *testing.T) {
	t.Parallel()
	for _, runner := range []Runner{AsRoot, AsLoginUser} {
		stopping := Command(HookWord("/workspace/.ao/worker/hook"), time.Minute, "", Options{Runner: runner, Stop: StopOwnerProcesses})
		stop, run := strings.Index(stopping, `ao_stop "$ao_who" TERM`), strings.Index(stopping, `ao_out=$(`)
		if stop < 0 || run < stop || !strings.Contains(stopping, `ao_stop "$ao_who" KILL`) ||
			!strings.Contains(stopping, `1|"$$") continue`) || !strings.Contains(stopping, "ao_who=${ao_owner:-$(id -un)}") {
			t.Fatalf("runner %d: the owner's processes must be stopped, sparing PID 1 and the command, before the hook runs:\n%s", runner, stopping)
		}
		if keeping := Command(HookWord("/workspace/.ao/worker/hook"), time.Minute, "", Options{Runner: runner}); strings.Contains(keeping, "ao_stop") {
			t.Fatalf("runner %d: KeepOwnerProcesses stops processes:\n%s", runner, keeping)
		}
	}
}

// The stop sends SIGTERM, waits, then SIGKILLs a process that ignores it, and
// touches only the owner's processes. Run where /proc lists processes, with id
// and stat shimmed so the owner's are only the two children started here.
func TestCommandStopEndsOnlyTheOwnersProcesses(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc to list processes")
	}
	polite := exec.Command("sleep", "60")
	stubborn := exec.Command("sh", "-c", `trap "" TERM; while :; do sleep 0.2; done`)
	bystander := exec.Command("sleep", "60")
	for _, process := range []*exec.Cmd{polite, stubborn, bystander} {
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = bystander.Process.Kill(); _ = bystander.Wait() })
	exited := func(process *exec.Cmd) chan struct{} {
		done := make(chan struct{})
		go func() { _ = process.Wait(); close(done) }()
		return done
	}
	politeDone, stubbornDone := exited(polite), exited(stubborn)
	bin := t.TempDir()
	owned := "/proc/" + strconv.Itoa(polite.Process.Pid) + "|/proc/" + strconv.Itoa(stubborn.Process.Pid)
	shims := map[string]string{
		"id":   "#!/bin/sh\ncase \"$1\" in -u) echo 4242 ;; -un) echo owner ;; *) exit 1 ;; esac\n",
		"stat": "#!/bin/sh\ncase \"$3\" in " + owned + ") echo 4242 ;; *) echo 0 ;; esac\n",
	}
	for name, content := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hook := filepath.Join(t.TempDir(), worker.CleanupHookFile)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho hook ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	output := runHookCommand(t, Command(HookWord(hook), 10*time.Second, "", Options{Runner: AsLoginUser, Stop: StopOwnerProcesses}),
		append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH")))
	if !strings.Contains(output, "hook ran") || !strings.Contains(output, Marker+":0") {
		t.Fatalf("output = %q", output)
	}
	for name, done := range map[string]chan struct{}{"polite": politeDone, "stubborn": stubbornDone} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("the %s owner process is still running", name)
		}
	}
	if elapsed := time.Since(started); elapsed < OwnerStopWait-time.Second {
		t.Fatalf("the stubborn process was killed after %s, before its SIGTERM wait", elapsed)
	}
	if err := bystander.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("a process the owner does not own was signalled: %v", err)
	}
}

// Run as root with a pinned owner, the hook runs as that account whoever owns
// the file (an agent could point the hook at another account's program), and
// is skipped when the account does not exist.
func TestCommandAsRootRunsTheHookAsThePinnedOwner(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	hook := filepath.Join(t.TempDir(), worker.CleanupHookFile)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho hook ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := Command(HookWord(hook), 10*time.Second, "", Options{Runner: AsRoot, Owner: "ao-worker"})
	if strings.Contains(command, "stat -c %U") {
		t.Fatalf("a pinned owner must not be read from the hook's file:\n%s", command)
	}
	// stat would name another account; id knows ao-worker.
	environment := rootShims(t, "postgres")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "id"), []byte("#!/bin/sh\ncase \"$1\" in -un) echo root ;; -u) [ \"$2\" = ao-worker ] && echo 1000 ;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			environment[index] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
		}
	}
	if output := runHookCommand(t, command, environment); !strings.Contains(output, "runuser:ao-worker") || !strings.Contains(output, "hook ran") {
		t.Fatalf("output = %q", output)
	}
	missing := Command(HookWord(hook), 10*time.Second, "", Options{Runner: AsRoot, Owner: "no-such-user"})
	if output := runHookCommand(t, missing, environment); !strings.Contains(output, Marker+":"+Skipped) {
		t.Fatalf("missing account: output = %q", output)
	}
}

// The project's Cleanup changed after the session started, and the hook ran
// the commands the worker was given at launch (the current ones could not be
// passed in, or could not be read). One of those old commands sets a secret
// and prints it with nothing naming it: with the launch-time commands stored
// on the sandbox (domain.InstalledCleanupKey, carried by the reconciler as
// sandbox.WithInstalledCleanupCommands), the reported steps and the stage's
// output mask it.
func TestCleanupOfCommandsGivenAtLaunchMasksTheirSecrets(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	launched := []string{
		`export DEPLOY_TOKEN=FAKEVAL98x; printf 'token %s\n' "$DEPLOY_TOKEN"`,
		`printf '%s\n' "$DEPLOY_TOKEN"; exit 4`,
	}
	huge := make([]string, 20)
	for index := range huge {
		var step strings.Builder
		step.WriteString("echo ")
		for range 256 {
			step.WriteString(worker.NewScriptMarkerNonce())
		}
		huge[index] = step.String()
	}
	for name, ctx := range map[string]context.Context{
		// The project's current Cleanup is too large to pass to the sandbox.
		"current too large": sandbox.WithCleanupCommands(context.Background(), huge),
		// It could not be read.
		"current unknown": context.Background(),
	} {
		dataDir, workspace := t.TempDir(), t.TempDir()
		environment := worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}
		if err := worker.WriteCleanupHook(dataDir, launched, workspace, environment); err != nil {
			t.Fatal(err)
		}
		stored, err := json.Marshal(map[string]any{domain.InstalledCleanupKey: launched, "providerFailureId": "build-1"})
		if err != nil {
			t.Fatal(err)
		}
		installed, ok := domain.InstalledCleanupCommands(stored)
		if !ok {
			t.Fatal("the stored commands were not read back")
		}
		plan, ok := NewPlan(sandbox.WithInstalledCleanupCommands(ctx, installed))
		if !ok || plan.Override != "" || strings.Join(plan.Steps.KnownCommands, "\n") != strings.Join(launched, "\n") {
			t.Fatalf("%s: plan = %+v", name, plan)
		}
		output := runHookCommand(t, Command(HookWord(filepath.Join(dataDir, worker.CleanupHookFile)), 10*time.Second, plan.Override, Options{}), os.Environ())
		printed, status, ok := Split(output)
		if !ok || status != "4" || !strings.Contains(printed, "FAKEVAL98x") {
			t.Fatalf("%s: the launch-time commands did not run: %q", name, output)
		}
		r := &recorder{}
		reportCtx := r.context()
		steps := plan.Steps
		steps.StartedAt = time.Now()
		Report(reportCtx, sandbox.BeginStage(reportCtx, Stage), map[string]string{}, steps, printed, status, nil, "test")
		if len(r.events) != 2 || r.events[1].Status != worker.SetupFailed {
			t.Fatalf("%s: events = %+v", name, r.events)
		}
		published := ""
		for _, event := range r.events {
			published += event.Command + "\n" + event.Output + "\n" + worker.SanitizeScriptStepEvent(event).Output + "\n"
		}
		for _, stage := range r.stages {
			published += stage.Attrs["output"] + "\n"
		}
		if strings.Contains(published, "FAKEVAL98x") || !strings.Contains(published, "token [REDACTED]") {
			t.Fatalf("%s: published %q", name, published)
		}
	}

	// Without the stored commands, the current Cleanup is the best guess
	// left; one the control plane installs for the run counts too.
	plan, _ := NewPlan(sandbox.WithCleanupCommands(context.Background(), []string{"make down"}))
	if strings.Join(plan.Steps.KnownCommands, "|") != "make down" || strings.Join(plan.Steps.Commands, "|") != "make down" {
		t.Fatalf("current-only plan = %+v", plan.Steps)
	}
	plan, _ = NewPlan(sandbox.WithInstalledCleanupCommands(sandbox.WithCleanupCommands(context.Background(), []string{"make down"}), launched))
	if plan.Override == "" || strings.Join(plan.Steps.Commands, "|") != "make down" || len(plan.Steps.KnownCommands) != 2 {
		t.Fatalf("current and stored plan = %+v", plan.Steps)
	}
}

// sudoPolicy is what the sudo stand-in allows the login user.
type sudoPolicy int

const (
	// sudoAny: sudo -n as any user.
	sudoAny sudoPolicy = iota
	// sudoRootOnly: sudo -n only as root, with a runuser stand-in that works.
	sudoRootOnly
	// sudoRootOnlyNoRunuser: sudo -n only as root, and runuser fails.
	sudoRootOnlyNoRunuser
	// sudoNone: no passwordless sudo at all.
	sudoNone
)

// loginShims puts stand-ins for sudo, runuser and id first on PATH. Each runs
// the command as the test user, saying on stderr whom it was asked to run as.
// Asked to run test(1) as root, sudo fails, as uutils coreutils (Ubuntu 25.10
// and later) does for a file only its owner may run. id knows ao-worker.
func loginShims(t *testing.T, policy sudoPolicy) []string {
	t.Helper()
	realID, err := exec.LookPath("id")
	if err != nil {
		t.Skip("id is unavailable")
	}
	bin := t.TempDir()
	sudo := "#!/bin/sh\n[ \"$1\" = -n ] || { echo sudo without -n >&2; exit 91; }\nshift\n"
	if policy == sudoNone {
		sudo += "echo 'sudo: a password is required' >&2\nexit 1\n"
	}
	sudo += "user=root\nwhile :; do case \"$1\" in -H) shift ;; -u) user=$2; shift 2 ;; *) break ;; esac; done\n"
	if policy == sudoRootOnly || policy == sudoRootOnlyNoRunuser {
		sudo += "[ \"$user\" = root ] || { echo \"sudo: not allowed as $user\" >&2; exit 1; }\n"
	}
	sudo += "if [ \"$user\" = root ] && [ \"$1\" = test ]; then exit 1; fi\n" +
		"[ \"$user\" = root ] || echo \"sudo-as:$user\" >&2\n" +
		"exec \"$@\"\n"
	runuser := "#!/bin/sh\n[ \"$1\" = -u ] && [ \"$3\" = -- ] || { echo bad runuser call: \"$@\" >&2; exit 90; }\necho \"runuser:$2\" >&2\nshift 3\nexec \"$@\"\n"
	if policy == sudoRootOnlyNoRunuser {
		runuser = "#!/bin/sh\nexit 1\n"
	}
	shims := map[string]string{
		"sudo":    sudo,
		"runuser": runuser,
		"id":      "#!/bin/sh\nif [ \"$1\" = -u ] && [ -n \"$2\" ]; then [ \"$2\" = ao-worker ] && echo 1001 && exit 0; exit 1; fi\nexec " + shellQuote(realID) + " \"$@\"\n",
	}
	for name, content := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeExecutable writes a script the test user owns.
func writeExecutable(t *testing.T, file, content string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return file
}

// skipReasonOf runs a hook command and returns the reason it skipped the hook
// for, failing the test if it did not skip.
func skipReasonOf(t *testing.T, name, command string, environment []string) string {
	t.Helper()
	output := runHookCommand(t, command, environment)
	_, status, ok := Split(output)
	reason, skipped := SkipReason(status)
	if !ok || !skipped || strings.Contains(output, "hook ran") || strings.Contains(output, "pwned") {
		t.Fatalf("%s: output = %q, want the hook skipped", name, output)
	}
	return reason
}

// Run as a login user that is not the worker's account (Coder's agent
// terminal, the worker running as ao-worker), the hook command runs the hook
// as that account through sudo, or runuser from root when the login user may
// use sudo only as root, and never as the login user or root. Whether the hook
// can run is asked as the owner, not as root: prod's Ubuntu 26.04 Coder image
// skipped every Cleanup on a root test -x. Each reason it cannot run is
// reported after Skipped.
func TestCommandAsLoginUserRunsTheHookAsTheWorkersAccount(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dir := t.TempDir()
	hook := writeExecutable(t, filepath.Join(dir, "worker", worker.CleanupHookFile), "#!/bin/sh\necho hook ran\n", 0o700)
	command := Command(HookWord(hook), 10*time.Second, "", Options{Runner: AsLoginUser, Owner: "ao-worker"})
	if strings.Contains(command, "sudo -n test") {
		t.Fatalf("the hook command checks the hook as root:\n%s", command)
	}
	output := runHookCommand(t, command, loginShims(t, sudoAny))
	if !strings.Contains(output, "sudo-as:ao-worker") || !strings.Contains(output, "hook ran") || !strings.Contains(output, Marker+":0") {
		t.Fatalf("sudo: hook output = %q", output)
	}
	output = runHookCommand(t, command, loginShims(t, sudoRootOnly))
	if !strings.Contains(output, "runuser:ao-worker") || strings.Contains(output, "sudo-as:") ||
		!strings.Contains(output, "hook ran") || !strings.Contains(output, Marker+":0") {
		t.Fatalf("root-only sudo: hook output = %q", output)
	}

	// A link to a program the login user owns never runs, whatever sudo allows.
	pwned := writeExecutable(t, filepath.Join(t.TempDir(), "login-users-program"), "#!/bin/sh\necho pwned\n", 0o755)
	link := filepath.Join(t.TempDir(), "worker", worker.CleanupHookFile)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pwned, link); err != nil {
		t.Fatal(err)
	}
	notRunnable := writeExecutable(t, filepath.Join(t.TempDir(), "worker", worker.CleanupHookFile), "#!/bin/sh\necho hook ran\n", 0o600)
	missing := filepath.Join(dir, "worker", "missing-hook.sh")
	for name, test := range map[string]struct {
		hook, owner string
		policy      sudoPolicy
		reason      string
	}{
		"symlink":                 {hook: link, owner: "ao-worker", policy: sudoAny, reason: skipLink},
		"symlink without sudo":    {hook: link, owner: "ao-worker", policy: sudoNone, reason: skipLink},
		"no hook":                 {hook: missing, owner: "ao-worker", policy: sudoAny, reason: skipNoHook},
		"no hook without sudo":    {hook: missing, owner: "ao-worker", policy: sudoNone, reason: skipNoHook},
		"hook without sudo":       {hook: hook, owner: "ao-worker", policy: sudoNone, reason: skipNoSudo},
		"not runnable":            {hook: notRunnable, owner: "ao-worker", policy: sudoAny, reason: skipNotRunnable},
		"no account":              {hook: hook, owner: "postgres-x", policy: sudoAny, reason: skipNoAccount},
		"sudo as root, no runner": {hook: hook, owner: "ao-worker", policy: sudoRootOnlyNoRunuser, reason: skipSudoRootOnly},
	} {
		command := Command(HookWord(test.hook), 10*time.Second, "", Options{Runner: AsLoginUser, Owner: test.owner})
		if reason := skipReasonOf(t, name, command, loginShims(t, test.policy)); reason != test.reason {
			t.Fatalf("%s: skipped %q, want %q", name, reason, test.reason)
		}
	}
}

// Run as the worker's account, the project's current Cleanup is installed and
// run through sudo as that account.
func TestCommandAsLoginUserInstallsTheCurrentCleanupAsTheOwner(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"sh", "base64", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	dataDir, workspace := t.TempDir(), t.TempDir()
	if err := worker.WriteCleanupHook(dataDir, []string{"echo installed-cleanup"}, workspace,
		worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}); err != nil {
		t.Fatal(err)
	}
	payload, _ := CommandsPayload([]string{"echo current-cleanup"}, worker.NewScriptMarkerNonce())
	command := Command(HookWord(filepath.Join(dataDir, worker.CleanupHookFile)), 10*time.Second, payload,
		Options{Runner: AsLoginUser, Owner: "ao-worker"})
	output := runHookCommand(t, command, loginShims(t, sudoAny))
	if !strings.Contains(output, "current-cleanup") || strings.Contains(output, "installed-cleanup") ||
		!strings.Contains(output, "sudo-as:ao-worker") || !strings.Contains(output, Marker+":0") {
		t.Fatalf("output = %q", output)
	}
}

// Without an owner (a Docker container's user, a Coder workspace-user worker)
// only the login user's own hook runs, and never through a link.
func TestCommandAsLoginUserRunsOnlyItsOwnHook(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	hook := writeExecutable(t, filepath.Join(t.TempDir(), worker.CleanupHookFile), "#!/bin/sh\necho hook ran\n", 0o700)
	command := Command(HookWord(hook), 10*time.Second, "", Options{Runner: AsLoginUser})
	if output := runHookCommand(t, command, os.Environ()); !strings.Contains(output, "hook ran") || !strings.Contains(output, Marker+":0") {
		t.Fatalf("own hook: output = %q", output)
	}
	pwned := writeExecutable(t, filepath.Join(t.TempDir(), "program"), "#!/bin/sh\necho pwned\n", 0o755)
	link := filepath.Join(t.TempDir(), worker.CleanupHookFile)
	if err := os.Symlink(pwned, link); err != nil {
		t.Fatal(err)
	}
	cases := map[string][2]string{
		"symlink":      {link, skipLink},
		"no hook":      {filepath.Join(t.TempDir(), worker.CleanupHookFile), skipNoHook},
		"not runnable": {writeExecutable(t, filepath.Join(t.TempDir(), worker.CleanupHookFile), "#!/bin/sh\necho hook ran\n", 0o600), skipNotRunnable},
	}
	if os.Geteuid() != 0 {
		if other, err := exec.LookPath("true"); err == nil {
			if other, err = filepath.EvalSymlinks(other); err == nil {
				cases["someone else's"] = [2]string{other, skipOwner}
			}
		}
	}
	for name, test := range cases {
		command := Command(HookWord(test[0]), 10*time.Second, "", Options{Runner: AsLoginUser})
		if reason := skipReasonOf(t, name, command, os.Environ()); reason != test[1] {
			t.Fatalf("%s: skipped %q, want %q", name, reason, test[1])
		}
	}
}

// Run as root, a hook that is a link never runs, pinned owner or not.
func TestCommandAsRootRefusesALinkedHook(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	pwned := writeExecutable(t, filepath.Join(t.TempDir(), "program"), "#!/bin/sh\necho pwned\n", 0o755)
	link := filepath.Join(t.TempDir(), worker.CleanupHookFile)
	if err := os.Symlink(pwned, link); err != nil {
		t.Fatal(err)
	}
	for _, options := range []Options{{Runner: AsRoot}, {Runner: AsRoot, Owner: "ao-worker"}} {
		command := Command(HookWord(link), 10*time.Second, "", options)
		if reason := skipReasonOf(t, "root", command, rootShims(t, "ao-worker")); reason != skipLink {
			t.Fatalf("%+v: skipped %q, want %q", options, reason, skipLink)
		}
	}
}

// SkipReason reads the reason after Skipped, and none from an exit status.
func TestSkipReasonReadsTheHookCommandsStatus(t *testing.T) {
	t.Parallel()
	for status, want := range map[string][2]string{
		Skipped:                    {"", "skipped"},
		Skipped + " " + skipNoHook: {skipNoHook, "skipped"},
		" skipped no_sudo \r":      {skipNoSudo, "skipped"},
		"0":                        {"", ""},
		"":                         {"", ""},
		"skippedx":                 {"", ""},
	} {
		reason, skipped := SkipReason(status)
		if reason != want[0] || skipped != (want[1] == "skipped") {
			t.Fatalf("SkipReason(%q) = %q, %v", status, reason, skipped)
		}
	}
	if SkippedReason("bogus") == "" || SkippedReason(skipNoHook) == SkippedReason("bogus") {
		t.Fatal("every reason needs words for the user")
	}
}

// SkipOnce reports a Cleanup that cannot run because the sandbox is not
// running: each step skipped with NotRunningReason, the stage as skipped and
// not failed, and only once for the sandbox.
func TestSkipOnceReportsANotRunningSandboxOnce(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	ctx := r.context()
	var tracker Tracker
	steps := worker.CleanupRun{RunID: "run-1", Commands: []string{"make down", "docker compose down"}}
	for range 2 {
		tracker.SkipOnce(ctx, "vm-1", map[string]string{"provider_id": "vm-1"}, steps, NotRunningReason)
	}
	if len(r.events) != 2 || len(r.stages) != 1 {
		t.Fatalf("events = %+v, stages = %+v", r.events, r.stages)
	}
	for _, event := range r.events {
		if event.Status != worker.ScriptStepSkipped || event.Hint != NotRunningReason || !worker.ValidScriptStepEvent(event) {
			t.Fatalf("event = %+v", event)
		}
	}
	if stage := r.stages[0]; stage.Err != nil || stage.Attrs["result"] != Skipped {
		t.Fatalf("stage = %+v", stage)
	}
	// The report is not a run: a later Delete that finds the sandbox running
	// still runs its Cleanup.
	if _, ok := tracker.Lookup("vm-1"); ok {
		t.Fatal("a not-running report must not look like a finished Cleanup run")
	}
}

// A session that never got its working folder runs no Cleanup there, even
// with the project's current Cleanup passed in: the hook command reports the
// run as skipped, saying why.
func TestCommandSkipsTheCleanupOfASessionWithoutItsWorkingFolder(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	dataDir := t.TempDir()
	if err := worker.WriteSkippedCleanupHook(dataDir, worker.CleanupSkipNoWorkingFolder); err != nil {
		t.Fatal(err)
	}
	payload, fits := CommandsPayload([]string{"echo pwned"}, worker.NewScriptMarkerNonce())
	if !fits {
		t.Fatal("payload does not fit")
	}
	command := Command(HookWord(filepath.Join(dataDir, worker.CleanupHookFile)), 10*time.Second, payload, Options{Runner: AsLoginUser})
	if reason := skipReasonOf(t, "no working folder", command, os.Environ()); reason != skipNoWorkingFolder {
		t.Fatalf("skipped %q, want %q", reason, skipNoWorkingFolder)
	}
	// sudo may print to stderr before the hook's own line, and ao_out
	// captures both.
	noisy := filepath.Join(t.TempDir(), worker.CleanupHookFile)
	writeExecutable(t, noisy, "#!/bin/sh\necho 'sudo: unable to resolve host ws-1: Name or service not known' >&2\n"+
		"printf '%s\\n' "+shellQuote(worker.CleanupHookSkippedPrefix+worker.CleanupSkipNoWorkingFolder)+"\n", 0o700)
	command = Command(HookWord(noisy), 10*time.Second, "", Options{Runner: AsLoginUser})
	if reason := skipReasonOf(t, "no working folder after sudo noise", command, os.Environ()); reason != skipNoWorkingFolder {
		t.Fatalf("skipped %q after sudo noise, want %q", reason, skipNoWorkingFolder)
	}
	if got := SkippedReason(skipNoWorkingFolder); got != "Cleanup did not run: this session never got its working folder." {
		t.Fatalf("reason = %q", got)
	}
}

// A run is remembered for RecordTTL after it ends, not after it starts: a run
// that waited for a slot and took its budget may end hours after it started,
// and a Delete re-issued then must find it instead of running Cleanup again.
// RecordTTL outlasts the longest a run may wait and take.
func TestTrackerRemembersARunForRecordTTLAfterItEnds(t *testing.T) {
	t.Parallel()
	if longest := sandbox.CleanupQueueWait(sandbox.MaxCleanupBudget) + sandbox.MaxCleanupBudget + sandbox.MaxCleanupBudget; RecordTTL < longest {
		t.Fatalf("RecordTTL = %s, shorter than the longest wait and budget %s", RecordTTL, longest)
	}
	var tracker Tracker
	run := tracker.Start(context.Background(), "vm-1", "org-1", Budget, Budget, func() {}, func(string) {})
	<-run.done
	// Started long ago, ended just now.
	run.started = time.Now().Add(-2 * RecordTTL)
	tracker.prune()
	if _, ok := tracker.Lookup("vm-1"); !ok {
		t.Fatal("a run that just ended was forgotten because it started long ago")
	}
	run.ended = time.Now().Add(-RecordTTL - time.Second)
	tracker.prune()
	if _, ok := tracker.Lookup("vm-1"); ok {
		t.Fatal("a run that ended more than RecordTTL ago is still remembered")
	}
}
