package litmus

import (
	"context"
	"encoding/hex"
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

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"gopkg.in/yaml.v3"
)

// fakeSys records every process action in order; started maps live pids to start times.
type fakeSys struct {
	log     []string
	started map[int]time.Time
	cmds    map[int]string
	envs    map[int][]string
	busy    int // port polls that still report busy
}

func (f *fakeSys) ops() sysOps {
	rec := func(verb string) func(int) {
		return func(pid int) { f.log = append(f.log, fmt.Sprint(verb, " ", pid)) }
	}
	return sysOps{
		startTime: func(pid int) (time.Time, bool) { t, ok := f.started[pid]; return t, ok },
		procs:     func() map[int]string { return f.cmds },
		environ: func(pid int) ([]string, error) {
			if env, ok := f.envs[pid]; ok {
				return env, nil
			}
			return nil, os.ErrPermission
		},
		killGroup: rec("killGroup"), kill: rec("kill"),
		portBusy: func(int) bool { f.busy--; return f.busy >= 0 },
	}
}

func TestLegEnvIsolatesTheTarget(t *testing.T) {
	t.Setenv("AO_PORT", "3002") // the real AO's variables must never reach a target
	t.Setenv("AO_SESSION_ID", "real")
	t.Setenv("ELECTRON_RUN_AS_NODE", "1")
	t.Setenv("LITMUS_LEG_MARK", "owner-marker")
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	r.Env = map[string]string{"FEATURE_FLAG": "1", "AO_PORT": "1", "AO_TELEMETRY_REMOTE": "on", "LITMUS_LEG_MARK": "recipe-marker"}
	env, err := legEnv(r, Run{ID: "run_s1", PRNumber: 6394}, "head", "/run/head", "/clone", "/node/bin", 3200, 9400, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	for k, want := range map[string]string{
		"AO_PORT": "3200", "AO_RUN_FILE": "/run/head/running.json", "AO_DATA_DIR": "/run/head/data", "AO_DEV_ELECTRON_DIR": "/run/head/electron",
		"AO_TMUX_SOCKET_NAME": "litmus-run_s1-head", "AO_APP_RUN_ID": "run_s1-head", "AO_TELEMETRY_REMOTE": "off", "FEATURE_FLAG": "1",
		"LITMUS_DEBUG_PORT": "9400", "LITMUS_APP_PORT": "3200", "LITMUS_ROOT": "/clone", "LITMUS_LEG_DIR": "/run/head", "LITMUS_LEG": "head", "LITMUS_RUN_ID": "run_s1", "NODE_OPTIONS": "--require /run/head/litmus-window.cjs", "LITMUS_ICON": "/run/head/litmus-icon.png", "LITMUS_RUN_NUM": "1", "AO_SESSION_ID": "", "ELECTRON_RUN_AS_NODE": "",
	} {
		if got := envValue(env, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(envValue(env, "PATH"), "/node/bin"+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q", envValue(env, "PATH"))
	}
	plain, err := legEnv(Recipe{}, Run{ID: "run_s1"}, "base", "/run/base", "/clone", "", 1, 2, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	if got := envValue(plain, "AO_RUN_FILE"); got != "" {
		t.Errorf("a recipe without isolation got AO_RUN_FILE=%q", got)
	}
	for _, values := range [][]string{env, plain} {
		marker := envValue(values, "LITMUS_LEG_MARK")
		if data, err := hex.DecodeString(marker); err != nil || len(data) != 16 {
			t.Fatalf("leg marker %q is not 128-bit hex: %v", marker, err)
		}
	}
	if envValue(env, "LITMUS_LEG_MARK") == envValue(plain, "LITMUS_LEG_MARK") {
		t.Fatal("legs reused the same marker")
	}
	if got := envValue(env, "HOME"); got != os.Getenv("HOME") {
		t.Errorf("HOME changed to %q", got) // the AO recipe shares the owner's HOME (home: false)
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
	gap, ok := (Run{}).seedGapOf(ctx, nil, "head", err)
	if !ok || !strings.HasPrefix(gap, "The PR build started without its sample content (seed step 1 failed: BRANCH_CHECKED_OUT_ELSEWHERE: ao/litmus-head-1/root xxx") || !strings.HasSuffix(gap, "...).") || strings.Contains(gap, "\n") {
		t.Fatalf("gap %q ok %t", gap, ok)
	}
	if _, ok := (Run{}).seedGapOf(ctx, nil, "head", errors.New("target not ready")); ok {
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
			if gap, ok := (Run{}).seedGapOf(c.ctx, c.exited, "head", err); ok || gap != "" {
				t.Fatalf("fatal seed failure became gap %q", gap)
			}
		})
	}
	if gap, ok := (Run{}).seedGapOf(ctx, nil, "head", err); !ok || gap == "" {
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
		gap, ok := (Run{}).seedGapOf(ctx, nil, leg, &seedError{step: 2, reason: "could not open " + path})
		if !ok {
			t.Fatal("seed failure did not become a gap")
		}
		run.Gaps = append(run.Gaps, svc.scrub(gap))
	}
	if run.Gaps[0] == run.Gaps[1] || !strings.HasPrefix(run.Gaps[0], "The PR build ") || !strings.HasPrefix(run.Gaps[1], "The main build ") {
		t.Fatalf("builds were not named separately: %q", run.Gaps)
	}
	comment := renderVerdict(run, Verdict{Result: "partial"}, nil, t.TempDir(), "", evidence{})
	if strings.Contains(comment, home) || strings.Contains(strings.Join(run.Gaps, "\n"), home) || !strings.Contains(comment, "could not open ~/.ao/") {
		t.Fatalf("seed path was not scrubbed in gaps or comment: %q\n%s", run.Gaps, comment)
	}
}

type seedReadyTransport struct{}

func (seedReadyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[{"type":"page"}]`)), Header: http.Header{}}, nil
}

type envReadyTransport struct{ path string }

func (t envReadyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := (seedReadyTransport{}).RoundTrip(req)
	if !pathExists(t.path) {
		response.StatusCode = http.StatusServiceUnavailable
	}
	return response, err
}

func TestPrivateHomeReachesTheLaunchedProcessAndSeeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a stopped POSIX shell")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PROVIDER_API_KEY", "owner-secret")
	svc := New(NewStore(t.TempDir()), nil)
	sys := (&fakeSys{}).ops()
	sys.killGroup = sigkillGroup
	svc.sys = sys
	svc.closeEngine = func(string) {}
	run := Run{ID: "run_env", Repo: "other/app", State: RunPrepared}
	is(t, "")(nil, svc.store.Save(run))
	shortRoot, err := os.MkdirTemp("/tmp", "lt-root-")
	is(t, "")(nil, err)
	t.Cleanup(func() { is(t, "")(nil, os.RemoveAll(shortRoot)) })
	root := filepath.Join(shortRoot, strings.Repeat("r", 90-len(shortRoot)-1))
	is(t, "")(nil, os.MkdirAll(root, 0o700))
	is(t, "")(nil, writeManifest(root, Manifest{SHA: "abc"}))
	script := filepath.Join(root, "fixture.sh")
	writeFile(t, script, "env > \"$LITMUS_LEG_DIR/app-env.txt\"\nkill -STOP $$\n")
	dir := svc.store.legDir(run.ID, "head")
	appEnvFile := filepath.Join(dir, "app-env.txt")
	oldClient := httpClient
	httpClient = &http.Client{Transport: envReadyTransport{appEnvFile}}
	t.Cleanup(func() { httpClient = oldClient })
	t.Cleanup(func() { is(t, "")(nil, svc.teardown(ctx, run)) })
	runner := &scriptEnvRunner{Runner: &fakeRunner{}}
	svc.runner = runner
	r := Recipe{Kind: "cli", Run: Step{Cmd: "sh", Args: []string{script}}, Ready: Ready{PageTargets: 1}, Seed: []SeedStep{{Cmd: "seed"}}, Isolation: Isolation{Home: boolPtr(true), Dirs: []string{"$LITMUS_LEG_DIR/profile", "$LITMUS_LEG_DIR/state/nested"}, Env: map[string]string{"TEST_CONFIG": "$LITMUS_HOME/provider"}}}
	_, err = svc.startLeg(ctx, run, r, Leg{Name: "head", Worktree: root}, newLaps(), nil)
	is(t, "")(nil, err)
	owned, err := readOwned(dir)
	is(t, "")(nil, err)
	if len(owned.TempDir) >= 40 || strings.HasPrefix(owned.TempDir, dir) || !pathExists(owned.TempDir) {
		t.Fatalf("TMPDIR is long, inside the leg, or missing: %q", owned.TempDir)
	}
	if runtime.GOOS == "darwin" && filepath.Dir(owned.TempDir) != "/private/tmp" {
		t.Fatalf("macOS TMPDIR is not under /private/tmp: %q", owned.TempDir)
	}
	for _, path := range owned.Dirs {
		if !pathExists(path) {
			t.Fatalf("recipe directory not created: %s", path)
		}
	}
	data, err := os.ReadFile(appEnvFile)
	is(t, "")(nil, err)
	if len(runner.envs) != 1 {
		t.Fatalf("captured %d seed environments", len(runner.envs))
	}
	for _, env := range [][]string{strings.Split(string(data), "\n"), runner.envs[0]} {
		if owned.LegMark == "" || envValue(env, "LITMUS_LEG_MARK") != owned.LegMark {
			t.Fatal("app or seed marker does not match owned.json")
		}
		if envValue(env, "LITMUS_ROOT") != root || len(root) != 90 || envValue(env, "TMPDIR") != owned.TempDir || envValue(env, "TMP") != owned.TempDir || envValue(env, "TEMP") != owned.TempDir {
			t.Fatalf("app or seed lost the long root or short temporary directory: %v", env)
		}
		for key, want := range privateHomeEnv(dir) {
			if envValue(env, key) != want {
				t.Errorf("process or seed %s = %q, want %q", key, envValue(env, key), want)
			}
		}
		if envValue(env, "TEST_CONFIG") != filepath.Join(dir, "home", "provider") || envValue(env, "PROVIDER_API_KEY") != "" {
			t.Fatal("process or seed lost isolation or inherited a credential")
		}
	}
	is(t, "")(nil, svc.teardown(ctx, run))
	for _, path := range append(owned.Dirs, owned.TempDir, filepath.Join(dir, "home")) {
		if pathExists(path) {
			t.Errorf("cleanup left private directory %s", path)
		}
	}
}

func TestFailedLaunchRemovesShortTempAndRecipeDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a stopped POSIX shell")
	}
	for _, stage := range []string{"start", "seed", "symlink"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			svc := New(NewStore(t.TempDir()), nil)
			sys := (&fakeSys{}).ops()
			sys.killGroup = sigkillGroup
			svc.sys = sys
			svc.runner = &fakeRunner{sh: func(string) error { return errors.New("seed fixture failed") }}
			run := Run{ID: "run_failed", State: RunPrepared}
			is(t, "")(nil, svc.store.Save(run))
			root := t.TempDir()
			is(t, "")(nil, writeManifest(root, Manifest{SHA: "abc"}))
			script := filepath.Join(root, "fixture.sh")
			writeFile(t, script, "kill -STOP $$\n")
			r := Recipe{Kind: "cli", Run: Step{Cmd: "sh", Args: []string{script}}, Ready: Ready{PageTargets: 1}, Seed: []SeedStep{{Cmd: "seed"}}, Isolation: Isolation{Home: boolPtr(true), Dirs: []string{"$LITMUS_LEG_DIR/profile"}}}
			dir := svc.store.legDir(run.ID, "head")
			outside := t.TempDir()
			if stage == "start" {
				r.Run.Dir = "missing"
			} else if stage == "symlink" {
				is(t, "")(nil, os.MkdirAll(dir, 0o700))
				is(t, "")(nil, os.Symlink(outside, filepath.Join(dir, "profile")))
				r.Isolation.Dirs[0] += "/escaped"
			}
			oldClient := httpClient
			httpClient = &http.Client{Transport: seedReadyTransport{}}
			t.Cleanup(func() { httpClient = oldClient })
			leg, err := svc.startLeg(ctx, run, r, Leg{Name: "head", Worktree: root}, newLaps(), nil)
			if err == nil || leg.State != "failed" {
				t.Fatalf("launch unexpectedly succeeded: %+v, %v", leg, err)
			}
			o, err := readOwned(dir)
			is(t, "")(nil, err)
			if o.TempDir == "" || pathExists(o.TempDir) || pathExists(filepath.Join(dir, "home")) {
				t.Fatalf("failed launch left private directories: %+v", o)
			}
			if pathExists(filepath.Join(outside, "escaped")) {
				t.Fatal("recipe directory escaped through a symlink")
			}
			if stage != "symlink" && (o.State != "cleaned" || pathExists(filepath.Join(dir, "profile"))) {
				t.Fatalf("failed launch did not clean recipe state: %+v", o)
			}
		})
	}
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
	r := Recipe{Kind: "cli", Isolation: Isolation{Home: boolPtr(true)}, Run: Step{Cmd: "sh", Args: []string{script}}, Ready: Ready{PageTargets: 1}, Seed: []SeedStep{{Cmd: "sample"}}}
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
	comment := renderVerdict(run, Verdict{Result: "partial"}, nil, t.TempDir(), "", evidence{})
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
		o := Owned{RunID: "run_s1", Leg: "head", PGID: Proc{100, t0}, Daemon: Proc{200, t0}, Electron: Proc{300, t0}, DaemonPort: 3200, DebugPort: 9400, State: "running", Dirs: []string{filepath.Join(dir, "electron"), filepath.Join(dir, "data")}, LegMark: "0123456789abcdef0123456789abcdef"}
		is(t, "")(nil, writeOwned(dir, o))
		if got, err := readOwned(dir); err != nil || !reflect.DeepEqual(got, o) {
			t.Fatalf("owned round trip: %+v err %v", got, err)
		}
		return dir, &fakeSys{started: started, cmds: cmds, busy: busy, envs: map[int][]string{400: {"LITMUS_LEG_MARK=" + o.LegMark}, 600: {"LITMUS_LEG_MARK=" + o.LegMark}}}
	}
	run := func(dir string, f *fakeSys) string {
		t.Helper()
		f.log = nil
		is(t, "")(nil, cleanLeg(ctx, f.ops(), dir, true))
		return strings.Join(f.log, "|")
	}

	// pid 300 was recycled, so only the recorded identities and inherited markers die, in that order.
	dir, f := newLeg(map[int]time.Time{100: t0, 200: t0, 300: t0.Add(time.Hour)}, map[int]string{500: "/usr/bin/other", 600: "tmux -L litmus-run_s1-head"}, 2) // ports busy for two polls
	f.cmds[400] = "/x/Electron --user-data-dir=" + dir + "/electron"
	if got, want := run(dir, f), "killGroup 100|kill 200|kill 400|kill 600"; got != want {
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
	if got := run(dir, f); got != "kill 200" {
		t.Fatalf("recycled leader: %s", got)
	}
	dir, f = newLeg(nil, nil, 0) // everything already dead: a leaderless group may still hold children
	if got := run(dir, f); got != "killGroup 100" {
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
	tmpDir, err := newLegTempDir()
	is(t, "")(nil, err)
	t.Cleanup(func() { is(t, "")(nil, os.RemoveAll(tmpDir)) })
	left := svc.store.legDir("run_s2", "head")
	o, err := readOwned(left)
	is(t, "")(nil, err)
	o.TempDir, o.Dirs = tmpDir, []string{filepath.Join(left, "arbitrary-profile")}
	is(t, "")(nil, os.MkdirAll(o.Dirs[0], 0o700))
	is(t, "")(nil, writeOwned(left, o))
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
	if pathExists(tmpDir) || pathExists(o.Dirs[0]) {
		t.Fatal("stale sweep left the temporary directory or recipe directory")
	}
}

func TestCleanLegRejectsUnownedTempDir(t *testing.T) {
	unprefixed, err := os.MkdirTemp(legTempRoot(), "other-")
	is(t, "")(nil, err)
	t.Cleanup(func() { is(t, "")(nil, os.RemoveAll(unprefixed)) })
	for _, path := range []string{filepath.Join(t.TempDir(), "lt-outside"), unprefixed} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(path, "keep"), "owner data")
		is(t, "")(nil, writeOwned(dir, Owned{State: "running", TempDir: path}))
		if err := cleanLeg(ctx, (&fakeSys{}).ops(), dir, false); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("unowned temporary directory was not reported: %v", err)
		}
		if !pathExists(filepath.Join(path, "keep")) {
			t.Fatalf("cleanup deleted unowned temporary directory %s", path)
		}
	}
}

func TestCleanLegMatchesOnlyExactEnvironmentMarker(t *testing.T) {
	dir := t.TempDir()
	tmpDir, err := newLegTempDir()
	is(t, "")(nil, err)
	t.Cleanup(func() { is(t, "")(nil, os.RemoveAll(tmpDir)) })
	const marker = "0123456789abcdef0123456789abcdef"
	const entry = "LITMUS_LEG_MARK=" + marker
	o := Owned{RunID: "run_1", Leg: "head", State: "running", LegMark: marker, TempDir: tmpDir}
	is(t, "")(nil, writeOwned(dir, o))
	f := &fakeSys{cmds: map[int]string{
		600: "daemonized-worker", 601: `tester --prompt "Inspect ` + entry + `"`,
		602: `tester --prompt "Inspect litmus-run_1-head"`, 603: "worker", 604: "worker", 605: "worker", 606: "worker", 607: "unreadable",
		608: "tester " + dir + "/profile " + tmpDir, 609: "tmux",
	}, envs: map[int][]string{
		600: {"OTHER=1", entry}, 601: {}, 602: {}, 603: {"PROMPT=Inspect " + entry},
		604: {"OTHER_" + entry}, 605: {entry + "x"}, 606: {"LITMUS_LEG_MARK=another-leg"}, 608: {}, 609: {entry},
	}}
	f.cmds[os.Getpid()], f.envs[os.Getpid()] = "current process", []string{entry}
	is(t, "")(nil, cleanLeg(ctx, f.ops(), dir, false))
	if got := strings.Join(f.log, "|"); got != "kill 600|kill 609" {
		t.Fatalf("cleanup did not require the exact environment entry: %s", got)
	}
	if pathExists(tmpDir) {
		t.Fatal("valid lt- temporary directory was not removed")
	}
}

func TestCleanLegFindsProcessesByPrivateEnvironmentPaths(t *testing.T) {
	for _, tc := range []struct {
		name, env, argv string
		kill            bool
	}{
		{"preview server", "TMPDIR=$TEMP/preview", "", true},
		{"private HOME and TMPDIR under leg dir", "HOME=$LEG/data/private-runtime\nTMPDIR=$LEG/data/private-runtime", "", true},
		{"TMP exact temp dir", "TMP=$TEMP", "", true},
		{"TEMP under temp dir", "TEMP=$TEMP/worker", "", true},
		{"HOME exact leg dir", "HOME=$LEG", "", true},
		{"cleaned absolute path", "TMPDIR=$LEG/tmp/../profile/", "", true},
		{"cleaned path outside leg", "TMPDIR=$LEG/../outside", "", false},
		{"sibling leg", "TMPDIR=$LEG-2/preview", "", false},
		{"sibling temp dir", "TMPDIR=$TEMP-2/preview", "", false},
		{"relative path", "TMPDIR=$REL/preview", "", false},
		{"empty paths", "TMPDIR=\nTMP=\nTEMP=\nHOME=", "", false},
		{"other variable", "PROFILE=$LEG/profile", "", false},
		{"key suffix", "OTHER_TMPDIR=$LEG/tmp", "", false},
		{"argv only", "", "--profile=$LEG/data --temp=$TEMP", false},
		{"owner HOME", "HOME=$OWNER_HOME", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tmpDir, err := newLegTempDir()
			is(t, "")(nil, err)
			t.Cleanup(func() { is(t, "")(nil, os.RemoveAll(tmpDir)) })
			expand := strings.NewReplacer("$LEG", dir, "$TEMP", tmpDir, "$REL", strings.TrimPrefix(dir, string(filepath.Separator)), "$OWNER_HOME", os.Getenv("HOME"))
			is(t, "")(nil, writeOwned(dir, Owned{State: "running", LegMark: "leg-marker", TempDir: tmpDir, PrivateHome: new(bool)}))
			f := &fakeSys{cmds: map[int]string{600: "worker " + expand.Replace(tc.argv)}, envs: map[int][]string{600: strings.Split(expand.Replace(tc.env), "\n")}}
			f.cmds[os.Getpid()], f.envs[os.Getpid()] = "current process", []string{"HOME=" + dir}
			is(t, "")(nil, cleanLeg(ctx, f.ops(), dir, false))
			want := ""
			if tc.kill {
				want = "kill 600"
			}
			if got := strings.Join(f.log, "|"); got != want {
				t.Fatalf("cleanup actions = %q, want %q", got, want)
			}
		})
	}
}

func TestCleanLegWithoutMarkerUsesOnlyRecordedIdentities(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	// A legacy record has needles but no legMark. Its command text must be ignored.
	writeFile(t, filepath.Join(dir, "owned.json"), fmt.Sprintf(`{"state":"running","daemon":{"pid":200,"start":%q},"processNeedles":["litmus-run_1-head"]}`, t0.Format(time.RFC3339)))
	writeFile(t, filepath.Join(dir, "home", "state"), "private")
	f := &fakeSys{started: map[int]time.Time{200: t0}, cmds: map[int]string{600: "tmux -L litmus-run_1-head " + dir}}
	sys := f.ops()
	sys.environ = func(int) ([]string, error) { t.Fatal("legacy cleanup read a process environment"); return nil, nil }
	is(t, "")(nil, cleanLeg(ctx, sys, dir, false))
	if got := strings.Join(f.log, "|"); got != "kill 200" || pathExists(filepath.Join(dir, "home")) {
		t.Fatalf("legacy cleanup: kills %q, private home exists %t", got, pathExists(filepath.Join(dir, "home")))
	}
}

func TestCleanLegRejectsSymlinkDotDotAndContinues(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	tmpDir, err := newLegTempDir()
	is(t, "")(nil, err)
	t.Cleanup(func() { is(t, "")(nil, os.RemoveAll(tmpDir)) })
	is(t, "")(nil, os.Mkdir(filepath.Join(outside, "subdir"), 0o700))
	link, base := tmpDir+"-link", filepath.Base(tmpDir)
	is(t, "")(nil, os.Symlink(filepath.Join(outside, "subdir"), link))
	t.Cleanup(func() { is(t, "")(nil, os.Remove(link)) })
	writeFile(t, filepath.Join(outside, base, "keep"), "owner data")
	writeFile(t, filepath.Join(tmpDir, "keep"), "temp data")
	writeFile(t, filepath.Join(dir, "home", "private"), "leg data")
	writeFile(t, filepath.Join(dir, "profile", "private"), "leg data")
	t0 := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	unsafePath := link + string(filepath.Separator) + ".." + string(filepath.Separator) + base
	is(t, "")(nil, writeOwned(dir, Owned{State: "running", PGID: Proc{100, t0}, Daemon: Proc{200, t0}, Electron: Proc{300, t0}, TempDir: unsafePath, Dirs: []string{filepath.Join(dir, "profile")}}))
	f := &fakeSys{started: map[int]time.Time{100: t0, 200: t0, 300: t0}}
	if err := cleanLeg(ctx, f.ops(), dir, true); err == nil || !strings.Contains(err.Error(), unsafePath) {
		t.Fatalf("noncanonical temporary directory was not reported: %v", err)
	}
	if got := strings.Join(f.log, "|"); got != "killGroup 100|kill 200|kill 300" {
		t.Fatalf("invalid temporary directory prevented process cleanup: %s", got)
	}
	if !pathExists(filepath.Join(outside, base, "keep")) || !pathExists(filepath.Join(tmpDir, "keep")) {
		t.Fatal("cleanup deleted the symlink target or the cleaned path")
	}
	if pathExists(filepath.Join(dir, "home")) || pathExists(filepath.Join(dir, "profile")) {
		t.Fatal("invalid temporary directory prevented leg directory cleanup")
	}
	owned, err := readOwned(dir)
	is(t, "")(nil, err)
	if owned.State != "running" || owned.TempDir != unsafePath {
		t.Fatalf("failed cleanup changed the owned record: %+v", owned)
	}
}

func TestElectronLaunchArgsKeepHiddenWindowsPainting(t *testing.T) {
	def, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	env, err := legEnv(def, Run{ID: "run_s1"}, "head", "/run/head", "/clone", "", 3200, 9400, "/private/tmp/lt-fixture")
	is(t, "")(nil, err)
	occ, bg := "--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding"
	port := "--remote-debugging-port=9400"
	for _, c := range []struct {
		name string
		r    Recipe
		goos string
		want []string
	}{
		{"default recipe", def, "darwin", []string{"start", "--", port, occ, bg, "--use-mock-keychain"}},
		{"windows adds the occlusion feature", def, "windows", []string{"start", "--", port, occ, bg, winOcclusion, "--use-mock-keychain"}},
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
	if len(def.Run.Args) != 4 {
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

func TestRecipeValidationFailureDoesNotFreezeRetention(t *testing.T) {
	svc, _ := prepSvc(t, map[string]string{"v24": "v24.21.0"})
	// Standalone validation has no tester or spawn marker.
	svc.store = NewStore(t.TempDir())
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	r.Run.Dir = "no-such-directory" // fail before any app process can start
	data, err := yaml.Marshal(r)
	is(t, "")(nil, err)
	path := filepath.Join(t.TempDir(), "recipe.yaml")
	is(t, "")(nil, os.WriteFile(path, data, 0o600))
	sha := strings.Repeat("a", 40)
	if _, err := svc.RecipeValidate(ctx, RecipeValidateRequest{Path: &path, SHA: sha}); err == nil {
		t.Fatal("validation unexpectedly launched from a missing directory")
	}
	runs, unreadable := svc.loadForPrune()
	if len(unreadable) != 0 || len(runs) != 1 {
		t.Fatalf("validation left unreadable run directories: runs=%d unreadable=%q", len(runs), unreadable)
	}
	run := runs[0]
	if !strings.HasPrefix(run.ID, "validate_") || run.SessionID != "" || run.State != RunPrepared || run.HeadSHA != sha || run.Error == "" || len(run.Legs) != 1 {
		t.Fatalf("validation did not retain its prepared checkout and failure: %+v", run)
	}
	if run.Comment != CommentOff || run.Evidence != EvidenceOff || run.CreatedAt.IsZero() {
		t.Fatal("validation record is missing its nonpublishing lifecycle metadata")
	}
	if !pathExists(filepath.Join(svc.store.legDir(run.ID, "head"), "logs", "dev.log")) {
		t.Fatal("failed validation logs were lost")
	}
	stale := filepath.Join(svc.store.reposDir(run.Repo), strings.Repeat("b", 40))
	writeFile(t, filepath.Join(stale, "node_modules", "unused"), "generated")
	is(t, "")(nil, writeManifest(stale, Manifest{UsedAt: svc.clock().Add(-24 * time.Hour)}))
	report, err := svc.Prune(ctx, 0, 0)
	if err != nil || pathExists(stale) || len(report.Checkouts) != 1 || len(report.Runs) != 0 {
		t.Fatalf("validation froze unrelated retention: %+v err=%v", report, err)
	}
	if !pathExists(run.Legs[0].Worktree) || !pathExists(filepath.Join(svc.store.root, run.ID)) {
		t.Fatal("prune deleted validation state before cleanup was proven")
	}
	if strings.Contains(strings.Join(report.Lines, "\n"), "no readable run.json") {
		t.Fatalf("validation was still classified as unreadable: %q", report.Lines)
	}
	// The tester sweep must not expire or kill a nonexistent validation session.
	svc.clock = func() time.Time { return run.Deadline.Add(time.Hour) }
	svc.SweepExpired(ctx, func(context.Context, string) error {
		t.Fatal("standalone validation reached the tester-session killer")
		return nil
	}, quiet)
	if saved, err := svc.store.Get(run.ID); err != nil || saved.State != RunPrepared || saved.Error != run.Error {
		t.Fatalf("tester expiry changed standalone validation: state=%s err=%v", saved.State, err)
	}
}

func TestRelaunchLegContinuesReceiptsAndBothRecordingSegments(t *testing.T) {
	svc, _, _, runDir := driveSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	start := svc.clock()
	f := &fakeSys{started: map[int]time.Time{123: start, 456: start}}
	svc.sys = f.ops()
	head, base := svc.store.legDir(run.ID, "head"), svc.store.legDir(run.ID, "base")
	writeFile(t, filepath.Join(head, "home", "draft"), "discard this hung draft")
	writeFile(t, filepath.Join(head, "tmp", "old"), "temporary")
	writeFile(t, filepath.Join(head, "logs", "dev.log"), "first launch\n")
	writeFile(t, filepath.Join(base, "home", "keep"), "other leg")
	is(t, "")(nil, writeOwned(head, Owned{RunID: run.ID, Leg: "head", PGID: Proc{123, start}, State: "running"}))
	is(t, "")(nil, writeOwned(base, Owned{RunID: run.ID, Leg: "base", PGID: Proc{456, start}, State: "running"}))
	run.Repo = DefaultRepo
	leg := run.Legs[0]
	leg.RecordStartedAt = start
	run.Legs[0] = leg
	is(t, "")(nil, svc.store.Save(run))
	e := svc.engineFor(run.ID, leg)
	first := svc.receipt(ctx, run.ID, e, "head", "snapshot", nil, refInfo{}, nil, nil, 0, "first", "")
	r := syntheticRecording(t, head, start, start.Add(time.Second))
	svc.recorders = map[string]*screencast{run.ID + "/head": r}
	// A stopped target still has the same pid/start identity in owned.json.
	// The missing build stops this test before startLeg can launch a process.
	is(t, "TESTING_NOT_PREPARED")(svc.Relaunch(ctx, "s1", "head"))
	if !reflect.DeepEqual(f.log, []string{"killGroup 123"}) || pathExists(filepath.Join(head, "home")) || pathExists(filepath.Join(head, "tmp")) || !pathExists(filepath.Join(base, "home", "keep")) {
		t.Fatalf("cleanup affected wrong state: kills %q", f.log)
	}
	log, err := os.ReadFile(filepath.Join(head, "logs", "dev.log"))
	is(t, "")(nil, err)
	if string(log) != "first launch\n" {
		t.Fatalf("cleanup lost logs: %q", log)
	}
	// Supply the new engine and recorder without starting an app process.
	leg.Engine = &EngineInfo{RunDir: runDir, Session: "fresh", Namespace: "fresh"}
	next := svc.engineFor(run.ID, leg)
	next.connectPage = e.connectPage
	second := svc.receipt(ctx, run.ID, next, "head", "snapshot", nil, refInfo{}, nil, nil, 0, "second", "")
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("receipt numbers restarted: %d, %d", first.Seq, second.Seq)
	}
	r = syntheticRecording(t, head, start, start.Add(time.Minute))
	svc.recorders[run.ID+"/head"] = r
	svc.runner = &videoProbeRunner{duration: "20"}
	media := &fakeMedia{}
	svc.media = media
	run = svc.stopRecording(ctx, run)
	meta, frames, err := readRecording(head, start)
	is(t, "")(nil, err)
	list, err := os.ReadFile(filepath.Join(head, "frames-concat.txt"))
	is(t, "")(nil, err)
	if len(run.Gaps) != 0 || len(frames) != 2 || frames[1].N != 2 || *frames[1].OffsetMS != 11000 || recordingLength(meta) != 20*time.Second || len(media.calls) != 1 || !fileExists(recordingPath(runDir, "head")) || !strings.Contains(string(list), "000001.jpg'\nduration 11.000") || !strings.Contains(string(list), "000002.jpg'\nduration 9.000") {
		t.Fatalf("relaunch video: frames %+v, manifest %+v, gaps %q, list %s, encodes %v", frames, meta, run.Gaps, list, media.calls)
	}
}

func TestLaunchRunningLegStillRefusesAndRelaunchNeedsPriorLaunch(t *testing.T) {
	svc, _, _, _ := driveSvc(t)
	is(t, "TESTING_LEG_RUNNING")(svc.Launch(ctx, "s1", "head"))
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	run.Repo = DefaultRepo
	is(t, "")(nil, svc.store.Save(run))
	is(t, "TESTING_LEG_NOT_LAUNCHED")(svc.Relaunch(ctx, "s1", "head"))
}
