package litmus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// fakeSys records every process action in order; started maps live pids to start times.
type fakeSys struct {
	log     []string
	started map[int]time.Time
	cmds    map[int]string
	busy    int // port polls that still report busy
}

func (f *fakeSys) ops() sysOps {
	rec := func(verb string) func(int) {
		return func(pid int) { f.log = append(f.log, fmt.Sprint(verb, " ", pid)) }
	}
	return sysOps{
		startTime: func(pid int) (time.Time, bool) { t, ok := f.started[pid]; return t, ok },
		procs:     func() map[int]string { return f.cmds },
		killGroup: rec("killGroup"), kill: rec("kill"), rmBrowser: rec("rm"),
		portBusy: func(int) bool { f.busy--; return f.busy >= 0 },
	}
}

func TestLegEnvIsolatesTheTarget(t *testing.T) {
	t.Setenv("AO_PORT", "3002") // the real AO's variables must never reach a target
	t.Setenv("AO_SESSION_ID", "real")
	t.Setenv("ELECTRON_RUN_AS_NODE", "1")
	r := Recipe{Isolation: true, Env: map[string]string{"FEATURE_FLAG": "1", "AO_PORT": "1", "AO_TELEMETRY_REMOTE": "on"}}
	env := legEnv(r, "run_s1", 6394, "head", "/run/head", "/clone", "/node/bin", 3200, 9400)
	for k, want := range map[string]string{
		"AO_PORT": "3200", "AO_RUN_FILE": "/run/head/running.json", "AO_DATA_DIR": "/run/head/data", "AO_DEV_ELECTRON_DIR": "/run/head/electron",
		"AO_TMUX_SOCKET_NAME": "litmus-run_s1-head", "AO_APP_RUN_ID": "run_s1-head", "AO_TELEMETRY_REMOTE": "off", "FEATURE_FLAG": "1",
		"LITMUS_DEBUG_PORT": "9400", "LITMUS_DAEMON_PORT": "3200", "LITMUS_ROOT": "/clone", "LITMUS_LEG": "head", "LITMUS_RUN_ID": "run_s1", "LITMUS_PR": "6394", "NODE_OPTIONS": "--require /run/head/litmus-window.cjs", "LITMUS_ICON": "/run/head/litmus-icon.png", "LITMUS_RUN_NUM": "1", "AO_SESSION_ID": "", "ELECTRON_RUN_AS_NODE": "",
	} {
		if got := envValue(env, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(envValue(env, "PATH"), "/node/bin"+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q", envValue(env, "PATH"))
	}
	if got := envValue(legEnv(Recipe{}, "run_s1", 0, "base", "/run/base", "/clone", "", 1, 2), "AO_RUN_FILE"); got != "" {
		t.Errorf("a recipe without isolation got AO_RUN_FILE=%q", got)
	}
	if got := envValue(env, "HOME"); got != os.Getenv("HOME") {
		t.Errorf("HOME changed to %q", got)
	}
	ports, err := freePorts(2)
	if err != nil || len(ports) != 2 || ports[0] == ports[1] || ports[0] == 0 || ports[1] == 0 {
		t.Fatalf("ports %v err %v", ports, err)
	}
}

func TestReadinessStages(t *testing.T) {
	var daemonUp, debugUp atomic.Bool
	var list atomic.Value
	list.Store(`[]`)
	serve := func(h http.HandlerFunc) string { s := httptest.NewServer(h); t.Cleanup(s.Close); return s.URL }
	rc := readyCheck{ready: Ready{DaemonPath: "/readyz", DebugPath: "/json/version", PageTargets: 1}}
	rc.daemon = serve(func(w http.ResponseWriter, _ *http.Request) {
		if !daemonUp.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	rc.debug = serve(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case !debugUp.Load():
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/json/list":
			_, _ = io.WriteString(w, list.Load().(string))
		}
	})
	for _, step := range []struct {
		set  func()
		want string // the stage that must be reported failing; "" = ready
	}{
		{func() {}, "daemon /readyz"},
		{func() { daemonUp.Store(true) }, "debug port /json/version"},
		{func() { debugUp.Store(true) }, "0 page targets"},
		{func() { list.Store(`[{"type":"page"},{"type":"page"}]`) }, "2 page targets"},
		{func() { list.Store(`[{"type":"background_page"},{"type":"page"}]`) }, ""},
	} {
		step.set()
		if err := rc.check(ctx); (step.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), step.want)) {
			t.Fatalf("want %q, got err %v", step.want, err)
		}
	}

	defer func(d time.Duration) { pollEvery = d }(pollEvery)
	pollEvery = 5 * time.Millisecond
	daemonUp.Store(false)
	time.AfterFunc(40*time.Millisecond, func() { daemonUp.Store(true) })
	if err := waitReady(ctx, rc, 5*time.Second, nil); err != nil {
		t.Fatalf("never became ready: %v", err)
	}
	daemonUp.Store(false)
	if err := waitReady(ctx, rc, 50*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "not ready after") || !strings.Contains(err.Error(), "daemon /readyz") {
		t.Fatalf("timeout err = %v", err)
	}
	exited := make(chan struct{})
	close(exited)
	if err := waitReady(ctx, rc, 5*time.Second, exited); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("exit err = %v", err)
	}
}

func TestSeedStepsRunInOrderAndRetryWaitFor(t *testing.T) {
	defer func(d time.Duration) { pollEvery = d }(pollEvery)
	pollEvery = time.Millisecond
	svc, _ := newSvc(t)
	f, waits := &fakeRunner{}, 0
	svc.runner = f
	f.sh = func(script string) error {
		if script == "ready?" {
			if waits++; waits < 3 {
				return os.ErrNotExist
			}
		}
		return nil
	}
	legDir := t.TempDir()
	is(t, "")(nil, os.MkdirAll(filepath.Join(legDir, "logs"), 0o750))
	steps := []SeedStep{{Cmd: "one"}, {Cmd: "two", WaitFor: "ready?"}}
	if err := svc.seed(ctx, steps, nil, "/root", legDir); err != nil || waits != 3 || f.count("sh -c one") != 1 || f.calls[0] != "/root|sh -c one" {
		t.Fatalf("err %v waits %d calls %q", err, waits, f.calls)
	}
	f.sh, f.shOut = func(string) error { return os.ErrPermission }, "BRANCH_CHECKED_OUT_ELSEWHERE: ao/litmus-head-1/root\n"+strings.Repeat("x", 400)
	err := svc.seed(ctx, steps, nil, "/root", legDir)
	if err == nil || !strings.HasPrefix(err.Error(), "seed step 1 (one): BRANCH_CHECKED_OUT_ELSEWHERE") || len(err.Error()) > 340 { // the step's own words, first 300 chars
		t.Fatalf("err = %v", err)
	}
	gap, ok := seedGapOf(ctx, nil, "head", err)
	if !ok || !strings.HasPrefix(gap, "The PR build started without its sample content (seed step 1 failed: BRANCH_CHECKED_OUT_ELSEWHERE: ao/litmus-head-1/root xxx") || !strings.HasSuffix(gap, "...).") || strings.Contains(gap, "\n") {
		t.Fatalf("gap %q ok %t", gap, ok)
	}
	if _, ok := seedGapOf(ctx, nil, "head", errors.New("target not ready")); ok {
		t.Fatal("a failure that is not a seed step became a gap")
	}
}

func TestSeedFailureIsFatalAfterTargetExitOrContextExpiry(t *testing.T) {
	err := &seedError{step: 2, reason: "no project"}
	exited := make(chan struct{})
	close(exited)
	expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	for _, c := range []struct {
		name   string
		ctx    context.Context
		exited <-chan struct{}
	}{
		{"target exited", ctx, exited},
		{"context expired", expired, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if gap, ok := seedGapOf(c.ctx, c.exited, "head", err); ok || gap != "" {
				t.Fatalf("fatal seed failure became gap %q", gap)
			}
		})
	}
	if gap, ok := seedGapOf(ctx, nil, "head", err); !ok || gap == "" {
		t.Fatalf("a running target lost its seed gap: %q, %t", gap, ok)
	}
}

func TestSeedGapsNameBothBuildsAndScrubPaths(t *testing.T) {
	svc, _ := newSvc(t)
	home, err := os.UserHomeDir()
	is(t, "")(nil, err)
	path := filepath.Join(home, ".ao", "litmus", "runs", "run_private", "head", "data")
	run := commentRun
	run.Gaps = nil
	for _, leg := range []string{"head", "base"} {
		gap, ok := seedGapOf(ctx, nil, leg, &seedError{step: 2, reason: "could not open " + path})
		if !ok {
			t.Fatal("seed failure did not become a gap")
		}
		run.Gaps = append(run.Gaps, svc.scrub(gap))
	}
	if run.Gaps[0] == run.Gaps[1] || !strings.HasPrefix(run.Gaps[0], "The PR build ") || !strings.HasPrefix(run.Gaps[1], "The main build ") {
		t.Fatalf("builds were not named separately: %q", run.Gaps)
	}
	comment := renderComment(run, commentVerdict(commentChecks(t)), evidence{}, run.CreatedAt.Add(time.Minute))
	if strings.Contains(comment, home) || strings.Contains(strings.Join(run.Gaps, "\n"), home) || !strings.Contains(comment, "could not open ~/.ao/") {
		t.Fatalf("seed path was not scrubbed in gaps or comment: %q\n%s", run.Gaps, comment)
	}
}

type seedReadyTransport struct{}

func (seedReadyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[{"type":"page"}]`)), Header: http.Header{}}, nil
}

func TestLaunchScrubsSeedGapsAndKeepsBothBuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a stopped POSIX shell")
	}
	t.Setenv("HOME", t.TempDir())
	oldClient := httpClient
	httpClient = &http.Client{Transport: seedReadyTransport{}}
	t.Cleanup(func() { httpClient = oldClient })
	svc := New(NewStore(t.TempDir()), nil)
	sys := (&fakeSys{}).ops()
	sys.killGroup = sigkillGroup
	svc.sys = sys
	svc.binary = "fake-agent-browser"
	svc.engExec = func(context.Context, string, []string, []string) (string, error) { return "", nil }
	svc.closeEngine = func(string) {}
	home, err := os.UserHomeDir()
	is(t, "")(nil, err)
	path := filepath.Join(home, ".ao", "litmus", "runs", "run_private", "head", "data")
	svc.runner = &fakeRunner{sh: func(string) error { return os.ErrPermission }, shOut: "could not open " + path}
	// Keep $$ in a script: runArgs expands recipe arguments before the shell sees them.
	// The stopped shell stays alive until cleanup kills its process group.
	script := filepath.Join(t.TempDir(), "target.sh")
	is(t, "")(nil, os.WriteFile(script, []byte("kill -STOP $$\n"), 0o600))
	r := Recipe{Kind: "cli", Isolation: true, Run: Step{Cmd: "sh", Args: []string{script}}, Ready: Ready{PageTargets: 1}, Seed: []SeedStep{{Cmd: "sample"}}}
	text, err := yaml.Marshal(r)
	is(t, "")(nil, err)
	recipePath := svc.store.recipePath("o/r")
	is(t, "")(nil, os.MkdirAll(filepath.Dir(recipePath), 0o750))
	is(t, "")(nil, os.WriteFile(recipePath, text, 0o600))
	run := Run{ID: "run_s1", SessionID: "s1", Repo: "o/r", State: RunPrepared, Deadline: time.Now().Add(RunBudget)}
	for _, name := range []string{"head", "base"} {
		root := t.TempDir()
		is(t, "")(nil, writeManifest(root, Manifest{SHA: "abc"}))
		run.Legs = append(run.Legs, Leg{Name: name, State: "prepared", Worktree: root})
	}
	is(t, "")(nil, svc.store.Save(run))
	is(t, "")(nil, svc.store.SaveChecks(run.ID, []Check{{ID: "c1"}, {ID: "c2"}}))
	t.Cleanup(func() {
		for _, e := range svc.engines {
			e.Close(sys)
		}
		for _, name := range []string{"head", "base"} {
			is(t, "")(nil, cleanLeg(ctx, sys, svc.store.legDir(run.ID, name), false))
		}
	})
	for _, name := range []string{"head", "base"} {
		run, err = svc.Launch(ctx, "s1", name)
		is(t, "")(nil, err)
	}
	var seedGaps []string
	for _, gap := range run.Gaps {
		if strings.Contains(gap, "started without its sample content (seed step ") {
			seedGaps = append(seedGaps, gap)
		}
	}
	if len(seedGaps) != 2 {
		t.Fatalf("identical seed failures merged across builds: %q", seedGaps)
	}
	for i, build := range []string{"PR", "main"} {
		want := "The " + build + " build started without its sample content (seed step 1 failed: could not open ~/.ao/litmus/runs/run_private/head/data)."
		if seedGaps[i] != want {
			t.Errorf("gap %q, want %q", seedGaps[i], want)
		}
	}
	comment := renderComment(run, commentVerdict(commentChecks(t)), evidence{}, time.Now())
	if strings.Contains(comment, home) || strings.Contains(strings.Join(run.Gaps, "\n"), home) || !strings.Contains(comment, "could not open ~/.ao/") {
		t.Fatalf("seed path was not scrubbed in gaps or comment: %q\n%s", run.Gaps, comment)
	}
}

func TestLaunchFailureSaysWhy(t *testing.T) {
	err := launchError("head", errors.New("seed step 2 (ao spawn): BRANCH_CHECKED_OUT_ELSEWHERE"))
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "TESTING_LAUNCH_FAILED" || ae.Kind != apierr.KindConflict || !strings.Contains(ae.Message, "BRANCH_CHECKED_OUT_ELSEWHERE") {
		t.Fatalf("launch error %+v", err)
	}
	kept := conflict("TESTING_NOT_PREPARED", "x")
	if got := launchError("head", kept); !errors.Is(got, kept) {
		t.Fatalf("an API error was rewrapped: %v", got)
	}
}

func TestCleanLegOrderAndStartTimeGuard(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	newLeg := func(started map[int]time.Time, cmds map[int]string, busy int) (string, *fakeSys) {
		dir := t.TempDir()
		for _, sub := range []string{"electron", "data", "logs"} {
			is(t, "")(nil, os.MkdirAll(filepath.Join(dir, sub), 0o750))
		}
		o := Owned{RunID: "run_s1", Leg: "head", PGID: Proc{100, t0}, Daemon: Proc{200, t0}, Electron: Proc{300, t0}, DaemonPort: 3200, DebugPort: 9400, State: "running"}
		is(t, "")(nil, writeOwned(dir, o))
		if got, err := readOwned(dir); err != nil || !reflect.DeepEqual(got, o) {
			t.Fatalf("owned round trip: %+v err %v", got, err)
		}
		return dir, &fakeSys{started: started, cmds: cmds, busy: busy}
	}
	run := func(dir string, f *fakeSys) string {
		t.Helper()
		f.log = nil
		is(t, "")(nil, cleanLeg(ctx, f.ops(), dir, true))
		return strings.Join(f.log, "|")
	}

	// pid 300 was recycled by another process, so only 100, 200 and what the command lines name die, in that order.
	dir, f := newLeg(map[int]time.Time{100: t0, 200: t0, 300: t0.Add(time.Hour)}, map[int]string{500: "/usr/bin/other", 600: "tmux -L litmus-run_s1-head"}, 2) // ports busy for two polls
	f.cmds[400] = "/x/Electron --user-data-dir=" + dir + "/electron"
	if got, want := run(dir, f), "killGroup 100|kill 200|rm 200|kill 400|rm 400|kill 600|rm 600"; got != want {
		t.Fatalf("kills = %s, want %s", got, want)
	}
	for sub, want := range map[string]bool{"electron": false, "data": false, "logs": true, "owned.json": true} {
		if _, err := os.Stat(filepath.Join(dir, sub)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", sub, err == nil, want)
		}
	}
	if o, _ := readOwned(dir); o.State != "cleaned" || run(dir, f) != "" {
		t.Fatalf("second cleanup acted: state %q", o.State)
	}

	dir, f = newLeg(map[int]time.Time{100: t0.Add(time.Minute), 200: t0, 300: t0.Add(time.Hour)}, nil, 0) // the group leader's pid was recycled: leave it alone
	if got := run(dir, f); got != "kill 200|rm 200" {
		t.Fatalf("recycled leader: %s", got)
	}
	dir, f = newLeg(nil, nil, 0) // everything already dead: a leaderless group may still hold children
	if got := run(dir, f); got != "killGroup 100|rm 200|rm 300" {
		t.Fatalf("dead target: %s", got)
	}

	defer func(d time.Duration) { portWait = d }(portWait)
	portWait = 100 * time.Millisecond
	dir, f = newLeg(nil, nil, 1000) // something still listens: keep the state so cleanup can be retried
	if err := cleanLeg(ctx, f.ops(), dir, true); err == nil || !strings.Contains(err.Error(), "still in use") {
		t.Fatalf("err = %v", err)
	}
	if o, _ := readOwned(dir); o.State != "running" {
		t.Fatalf("state %q", o.State)
	}
}

func TestCleanupVerbAndStaleSweep(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	data := t.TempDir()
	svc := New(NewStore(data), nil)
	f := &fakeSys{started: map[int]time.Time{100: t0, 101: t0}}
	svc.sys = f.ops()
	svc.closeEngine = func(id string) { f.log = append(f.log, "engine "+id) }
	leave := func(runID string, pid int, state string) string { // a run with one head leg and its owned.json
		is(t, "")(nil, svc.store.Save(Run{ID: runID, State: RunRunning, Legs: []Leg{{Name: "head", State: "running"}}}))
		dir := svc.store.legDir(runID, "head")
		is(t, "")(nil, os.MkdirAll(dir, 0o750))
		is(t, "")(nil, writeOwned(dir, Owned{RunID: runID, Leg: "head", PGID: Proc{pid, t0}, State: state}))
		return dir
	}

	leave("run_s1", 100, "running")
	out, err := svc.Cleanup(ctx, "s1")
	if err != nil || out.State != RunCleaned || out.Legs[0].State != "cleaned" || strings.Join(f.log, "|") != "engine run_s1|killGroup 100" {
		t.Fatalf("out %+v err %v log %q", out, err, f.log)
	}
	is(t, "TESTING_RUN_NOT_FOUND")(svc.Cleanup(ctx, "nobody"))

	f.log = nil
	leave("run_s2", 101, "running")
	leave("run_s3", 100, "cleaned") // already cleaned: never touched again
	is(t, "")(nil, os.WriteFile(filepath.Join(leave("run_s4", 100, "running"), "owned.json"), []byte("{torn"), 0o600))
	sweepStale(data, f.ops(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := strings.Join(f.log, "|"); got != "killGroup 101" {
		t.Fatalf("sweep acted on %q", got)
	}
	if run, _ := svc.store.Get("run_s2"); run.Legs[0].State != "cleaned" {
		t.Fatalf("run_s2 leg %+v", run.Legs)
	}
	if o, _ := readOwned(svc.store.legDir("run_s2", "head")); o.State != "cleaned" {
		t.Fatalf("owned %+v", o)
	}
}

func TestElectronLaunchArgsKeepHiddenWindowsPainting(t *testing.T) {
	def, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	env := legEnv(def, "run_s1", 0, "head", "/run/head", "/clone", "", 3200, 9400)
	occ, bg := "--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding"
	port := "--remote-debugging-port=9400"
	for _, c := range []struct {
		name string
		r    Recipe
		goos string
		want []string
	}{
		{"default recipe", def, "darwin", []string{"start", "--", port, occ, bg}},
		{"windows adds the occlusion feature", def, "windows", []string{"start", "--", port, occ, bg, winOcclusion}},
		{"a listed flag is not repeated", Recipe{Kind: "electron", Run: Step{Args: []string{port, bg, "--inspect"}}}, "darwin", []string{port, occ, bg, "--inspect"}},
		{"both listed", Recipe{Kind: "electron", Run: Step{Args: []string{occ, port, bg + "=true"}}}, "darwin", []string{occ, port, bg + "=true"}},
		{"the recipe owns --disable-features", Recipe{Kind: "electron", Run: Step{Args: []string{port, "--disable-features=X"}}}, "windows", []string{port, occ, bg, "--disable-features=X"}},
		{"not electron", Recipe{Kind: "web", Run: Step{Args: []string{port}}}, "darwin", []string{port}},
		{"no debug port argument", Recipe{Kind: "electron", Run: Step{Args: []string{"."}}}, "darwin", []string{"."}},
	} {
		if got := runArgs(c.r, env, c.goos); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if len(def.Run.Args) != 3 {
		t.Errorf("expanding mutated the recipe: %q", def.Run.Args)
	}
}

func TestDefaultLaunchBudgetMatchesRecipe(t *testing.T) {
	def, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	if len(def.Seed) != 2 || launchBudget(len(def.Seed)) != DefaultLaunchBudget {
		t.Fatalf("default recipe has %d seed steps and launch budget %s, want two steps and %s", len(def.Seed), launchBudget(len(def.Seed)), DefaultLaunchBudget)
	}
}

func TestElectronProcPicksTheBrowserNotAHelper(t *testing.T) {
	const flag = "--remote-debugging-port=9400"
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	f := &fakeSys{started: map[int]time.Time{310: t0}, cmds: map[int]string{
		300: "npm exec electron . " + flag,                                                           // wrapper: loses to the Electron binary
		310: "/x/Electron.app/Contents/MacOS/Electron . " + flag,                                     // the browser
		320: "/x/Electron Helper (Renderer) --type=renderer " + flag,                                 // helpers have higher pids
		330: "/x/Electron Helper (GPU) --type=gpu-process --remote-debugging-port=9400 --lang=en-US", // and --type=
		340: "/x/Electron . --remote-debugging-port=94001",                                           // another leg's port
	}}
	svc := &Service{sys: f.ops()}
	if got := svc.electronProc(flag); got != (Proc{PID: 310, Start: t0}) {
		t.Fatalf("electronProc = %+v, want pid 310", got)
	}
	if got := svc.electronProc("--remote-debugging-port=1"); got.PID != 0 {
		t.Fatalf("no match = %+v", got)
	}
}
