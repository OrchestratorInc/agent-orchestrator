package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeEngine stands in for the agent-browser binary: it records each client call
// (the argv after --session <s>) and writes the file a screenshot names.
type fakeEngine struct {
	calls [][]string
	envs  [][]string
	fail  map[string]error // by first word
	out   map[string]string
	noImg bool // diff writes no image, as the engine does when it has nothing to diff
	shot  []byte
	hook  func(args []string) // called with each engine call's arguments, before it is answered
}

func (f *fakeEngine) exec(ctx context.Context, _ string, args, env []string) (string, error) {
	if args[0] != "--session" {
		return "", errors.New("argv must start with --session <name>")
	}
	f.calls, f.envs = append(f.calls, args[2:]), append(f.envs, env)
	if f.hook != nil {
		f.hook(args[2:])
	}
	if err := f.fail[args[2]]; err != nil {
		return "", err
	}
	if args[2] == "screenshot" {
		data := f.shot
		if data == nil {
			data = pngBytes(tinyOpaque())
		}
		_ = os.WriteFile(args[3], data, 0o600)
	}
	if args[2] == "diff" && !f.noImg {
		_ = os.WriteFile(args[7], pngBytes(tinyOpaque()), 0o600)
	}
	if args[2] == "hang" {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.out[args[2]], nil
}

func (f *fakeEngine) words() string {
	var w []string
	for _, c := range f.calls {
		w = append(w, c[0])
	}
	return strings.Join(w, ",")
}

func (f *fakeEngine) find(word string) []string {
	for _, c := range f.calls {
		if c[0] == word {
			return c
		}
	}
	return nil
}

// driveSvc is a service with a running run whose head leg has an engine wired to the fake.
func driveSvc(t *testing.T) (*Service, *fakeEngine, *fakeRunner, string) {
	t.Helper()
	svc, now := newSvc(t)
	f, r := &fakeEngine{fail: map[string]error{}, out: map[string]string{}}, &fakeRunner{}
	svc.engExec, svc.runner, svc.binary = f.exec, r, "/fake/agent-browser"
	t.Cleanup(func(d time.Duration) func() { return func() { settleDelay = d } }(settleDelay))
	settleDelay = 0
	t.Cleanup(func() { svc.closeEngines("run_s1") })
	run := Run{ID: "run_s1", SessionID: "s1", State: RunRunning, Deadline: now.Add(RunBudget)}
	leg, err := svc.attachEngine(ctx, run.ID, Leg{Name: "head", State: "running", DebugPort: 9400})
	is(t, "")(nil, err)
	run.Legs = []Leg{leg}
	is(t, "")(nil, svc.store.Save(run))
	return svc, f, r, filepath.Join(svc.store.root, run.ID)
}

func TestDriveMapsVerbsAndNormalizesRefs(t *testing.T) {
	d := t.TempDir()
	for _, c := range []struct{ in, want string }{
		{"snapshot -i", "snapshot -i"}, {"click e12", "click @e12"}, {"click @e12", "click @e12"}, {"fill e3 hi", "fill @e3 hi"},
		{"type @e3 x", "type @e3 x"}, {"press Enter", "press Enter"}, {"drag e1 e2", "drag @e1 @e2"}, {"wait --text Done", "wait --text Done"},
		{"wait 500", "wait 500"}, {"console", "console"}, {"errors", "errors"}, {"network", "network requests"},
		{"network --filter api", "network requests --filter api"}, {"tab", "tab list"}, {"tab list", "tab list"},
		{"screenshot a.png", "screenshot " + d + "/a.png"},
	} {
		f := strings.Fields(c.in)
		got, _, err := argv(f[0], f[1:], d, "head")
		if err != nil || strings.Join(got, " ") != c.want {
			t.Errorf("%s -> %q (%v), want %q", c.in, got, err, c.want)
		}
	}
	if got, file, err := argv("fill", []string{"e3", "hello world"}, d, "head"); err != nil || len(got) != 3 || got[2] != "hello world" || file != "" {
		t.Errorf("text with spaces: %q %v", got, err)
	}
	if _, file, err := argv("screenshot", nil, d, "head"); err != nil || !strings.HasPrefix(file, d+"/shots/head-") { // no path: a name under shots/
		t.Errorf("default screenshot %q %v", file, err)
	}
}

func TestBoxFieldPreservesNegativeCoordinates(t *testing.T) {
	const padded = "x:      48\ny:      48\nwidth:  1184\nheight: 18"
	const wrapped = `{"success":true,"data":{"height":18,"width":1184,"x":48,"y":48},"error":null}`
	for _, c := range []struct {
		out, name string
		want      float64
	}{
		{`{"x":-12.5}`, "x", -12.5},
		{`x: -12.5`, "x", -12.5},
		{`{"width":320.25}`, "width", 320.25},
		{`{"prefix":99,"xOffset":100,"x":-12.5}`, "x", -12.5},
		{padded, "x", 48},
		{padded, "y", 48},
		{padded, "width", 1184},
		{padded, "height", 18},
		{wrapped, "x", 48},
		{wrapped, "y", 48},
		{wrapped, "width", 1184},
		{wrapped, "height", 18},
	} {
		got, ok := boxField(c.out, c.name)
		if !ok || got != c.want {
			t.Errorf("boxField(%q, %q) = %v, %t; want %v, true", c.out, c.name, got, ok, c.want)
		}
	}
}

func TestDriveClickTabDriftReturnsErrorWithoutClick(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	initial := "→ [t1] App - http://localhost:5173/#/x\n  [t2] about:blank - about:blank\n"
	moved := "  [t1] App - http://localhost:5173/#/x\n→ [t2] about:blank - about:blank\n"
	f.out["tab"] = initial
	var tabLists int
	f.hook = func(args []string) {
		if len(args) == 2 && args[0] == "tab" && args[1] == "list" {
			tabLists++
			if tabLists == 2 {
				f.out["tab"] = moved
			}
		}
	}
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	e := svc.engineFor(run.ID, run.Legs[0])
	e.last = "before.png" // avoid a before-shot, so the first click-point call observes the drift
	_, err = svc.Drive(ctx, "s1", "click", []string{"e1"})
	is(t, "TESTING_ENGINE_FAILED")(nil, err)
	if !strings.Contains(err.Error(), errTabDrift.Error()) {
		t.Fatalf("error %q does not preserve tab-drift guidance", err)
	}
	for _, call := range f.calls {
		if len(call) > 0 && call[0] == "click" {
			t.Fatalf("click ran after tab drift: %v", f.calls)
		}
	}
}

func TestDriveClickPointFailuresAreBestEffortAndLogged(t *testing.T) {
	old := slog.Default()
	var logs strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	for _, c := range []struct {
		name  string
		setup func(*fakeEngine)
	}{
		{name: "parse", setup: func(f *fakeEngine) { f.out["get"] = "box unavailable" }},
		{name: "engine", setup: func(f *fakeEngine) { f.fail["get"] = errors.New("box failed") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, f, _, _ := driveSvc(t)
			c.setup(f)
			run, err := svc.store.Get("run_s1")
			is(t, "")(nil, err)
			e := svc.engineFor(run.ID, run.Legs[0])
			e.last = "before.png" // skip the unrelated first-action before-shot
			if _, err := svc.Drive(ctx, "s1", "click", []string{"e1"}); err != nil {
				t.Fatalf("click failed after best-effort point failure: %v", err)
			}
			if f.find("click") == nil {
				t.Fatalf("click was not performed after %s failure: %v", c.name, f.calls)
			}
		})
	}
	if !strings.Contains(logs.String(), "click point unavailable") {
		t.Fatalf("debug log does not describe best-effort click-point failures: %q", logs.String())
	}
}

func TestDriveRefusals(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	base, err := svc.attachEngine(ctx, "run_s1", Leg{Name: "base", DebugPort: 9401}) // created but not launched
	is(t, "")(nil, err)
	run, _ := svc.store.Get("run_s1")
	run.Legs = append(run.Legs, base)
	is(t, "")(nil, svc.store.Save(run))

	for _, c := range []struct{ want, in string }{
		{"TESTING_ACTION_UNSUPPORTED", "open https://example.com"}, {"TESTING_ACTION_UNSUPPORTED", "eval 1"}, {"TESTING_ACTION_UNSUPPORTED", "cookies"},
		{"TESTING_ARGS_INVALID", "click"}, {"TESTING_ARGS_INVALID", "click button"}, {"TESTING_ARGS_INVALID", "fill e1"},
		{"TESTING_ARGS_INVALID", "press --cdp=http://x"}, {"TESTING_ARGS_INVALID", "snapshot --session x"}, {"TESTING_ARGS_INVALID", "tab new"},
		{"TESTING_ACTION_UNSUPPORTED", "record start v.mp4"}, {"TESTING_ACTION_UNSUPPORTED", "record stop"},
		{"TESTING_PATH_INVALID", "screenshot ../x.png"}, {"TESTING_PATH_INVALID", "screenshot /etc/x.png"},
		{"TESTING_LEG_NOT_LAUNCHED", "snapshot --leg base"}, {"TESTING_LEG_NOT_LAUNCHED", "snapshot --leg=main"},
	} {
		w := strings.Fields(c.in)
		is(t, c.want)(svc.Drive(ctx, "s1", w[0], w[1:]))
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refused action reached the engine: %v", f.calls)
	}

	run.Legs[1].State = "running" // both launched: the leg must be named
	is(t, "")(nil, svc.store.Save(run))
	is(t, "TESTING_LEG_REQUIRED")(svc.Drive(ctx, "s1", "snapshot", nil))
	is(t, "")(svc.Drive(ctx, "s1", "snapshot", []string{"--leg", "base"}))

	run.State = RunPrepared // not running yet
	is(t, "")(nil, svc.store.Save(run))
	is(t, "TESTING_RUN_NOT_RUNNING")(svc.Drive(ctx, "s1", "snapshot", nil))
	run.State = RunRunning
	is(t, "")(nil, svc.store.Save(run))
	svc.clock = func() time.Time { return run.Deadline.Add(time.Second) }
	is(t, "TESTING_RUN_CLOSED")(svc.Drive(ctx, "s1", "snapshot", nil)) // past the deadline
}

func TestDriveAllowsMultilineInputAndEnter(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	for _, action := range []string{"type", "fill"} {
		is(t, "")(svc.Drive(ctx, "s1", action, []string{"e1", "first\nsecond\rthird"}))
	}
	is(t, "")(svc.Drive(ctx, "s1", "press", []string{"Enter"}))
	if f.find("type") == nil || f.find("fill") == nil || f.find("press") == nil || f.find("eval") != nil {
		t.Fatalf("input actions did not reach the engine directly: %v", f.calls)
	}
}

func TestDriveReceiptsSequenceAndAttach(t *testing.T) {
	svc, f, r, runDir := driveSvc(t)
	var console []string
	for i := 1; i <= 30; i++ {
		console = append(console, "line "+strconv.Itoa(i))
	}
	f.out["console"] = strings.Join(console, "\n")

	res, err := svc.Drive(ctx, "s1", "click", []string{"e1"}) // no check open
	rec, _ := res["receipt"].(actionRec)
	if err != nil || rec.Seq != 1 || !rec.Unassigned || rec.Leg != "head" || !strings.HasSuffix(rec.Before, "head-before.png") || rec.After != filepath.Join(runDir, "receipts", "1-after.png") {
		t.Fatalf("receipt %+v err %v", rec, err)
	}
	if res["after"] != "receipts/1-after.png" { // the reply names the shot to Read
		t.Fatalf("reply names the after-shot %v", res["after"])
	}
	if got, want := f.words(), "stream,tab,scrollintoview,get,screenshot,click,wait,screenshot,errors,console,snapshot"; got != want {
		t.Fatalf("engine calls %s, want %s", got, want)
	}
	if w := f.find("wait"); !reflect.DeepEqual(w, []string{"wait", "--load", "load"}) {
		t.Fatalf("settle ran %v", w)
	}
	if r.count("sips -Z 1320") != 2 {
		t.Fatalf("downscale calls: %q", r.calls)
	}
	if rec.ConsoleTail != "" {
		t.Fatalf("console tail %q", rec.ConsoleTail)
	}

	is(t, "")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", ""))
	is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
	f.calls = nil
	res, err = svc.Drive(ctx, "s1", "fill", []string{"e2", "hi"})
	rec, _ = res["receipt"].(actionRec)
	if err != nil || rec.Seq != 2 || rec.Unassigned || rec.Before != filepath.Join(runDir, "receipts", "1-after.png") {
		t.Fatalf("second receipt %+v err %v", rec, err)
	}
	if got := f.words(); got != "fill,wait,screenshot,errors,console,snapshot" { // the leg already has a before-shot
		t.Fatalf("engine calls %s", got)
	}
	if _, err := svc.Drive(ctx, "s1", "snapshot", nil); err != nil { // read-only verbs leave no receipt
		t.Fatal(err)
	}
	checks, _ := svc.store.Checks("run_s1")
	if len(checks[0].Receipts) != 1 || checks[0].Receipts[0].Seq != 2 || checks[0].Receipts[0].Action != "fill" || checks[0].Receipts[0].AfterPath != rec.After {
		t.Fatalf("attached %+v", checks[0].Receipts)
	}

	f.fail["screenshot"] = errors.New("boom") // the action happened: the record stays, with a warning
	res, err = svc.Drive(ctx, "s1", "press", []string{"Enter"})
	if _, named := res["after"]; named {
		t.Errorf("a missing shot is named in the reply: %v", res["after"])
	}
	if rec, _ = res["receipt"].(actionRec); err != nil || rec.Seq != 3 || rec.After != "" || len(rec.Warnings) == 0 {
		t.Fatalf("receipt without a shot: %+v err %v", rec, err)
	}
	f.fail["click"] = errors.New("stale ref") // a failed action leaves no receipt
	is(t, "TESTING_ENGINE_FAILED")(svc.Drive(ctx, "s1", "click", []string{"e9"}))

	data, _ := os.ReadFile(filepath.Join(runDir, "actions.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("actions.jsonl:\n%s", data)
	}
	var first map[string]any
	is(t, "")(nil, json.Unmarshal([]byte(lines[0]), &first))
	for _, k := range []string{"seq", "leg", "action", "args", "before", "after", "consoleTail", "at", "ms", "unassigned"} {
		if _, ok := first[k]; !ok {
			t.Errorf("actions.jsonl line lacks %q: %s", k, lines[0])
		}
	}
	if _, err := os.Stat(rec.Before); err != nil {
		t.Errorf("before-shot missing: %v", err)
	}

	// a daemon restart keeps counting
	svc.seqs = map[string]int{}
	f.fail = map[string]error{}
	if res, _ = svc.Drive(ctx, "s1", "check", []string{"e4"}); res["receipt"].(actionRec).Seq != 4 {
		t.Fatalf("seq after restart: %+v", res["receipt"])
	}
	is(t, "")(svc.CheckEnd(ctx, "s1", "c1", CheckPass, "", false, 0))
	if v, err := svc.Submit(ctx, "s1", "", ""); err != nil || v.Overall != CheckPartial { // the attached receipt satisfies submit; the base was never looked at
		t.Fatalf("submit %+v err %v", v, err)
	}
}

func TestDriveFailedShotCannotEndCheck(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	is(t, "")(svc.CheckAdd(ctx, "s1", "menu opens", "list shown", ""))
	is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
	f.fail["screenshot"] = errors.New("capture failed")
	res, err := svc.Drive(ctx, "s1", "click", []string{"e1"})
	if rec, ok := res["receipt"].(actionRec); err != nil || !ok || rec.After != "" || len(rec.Warnings) == 0 {
		t.Fatalf("receipt %+v, %v", res, err)
	}
	_, err = svc.CheckEnd(ctx, "s1", "c1", CheckFail, "menu closed", false, 0)
	is(t, "TESTING_CHECK_EVIDENCE")(nil, err)
	if !strings.Contains(err.Error(), "no picture was saved; act again or end it skipped") {
		t.Fatalf("evidence refusal: %v", err)
	}
	checks, err := svc.store.Checks("run_s1")
	if err != nil || checks[0].State != CheckRunning || len(checks[0].Receipts) != 1 {
		t.Fatalf("checks %+v, %v", checks, err)
	}
	is(t, "")(svc.CheckEnd(ctx, "s1", "c1", CheckSkipped, "capture failed", false, 0))
}

func TestEngineEnvAndSocketGuard(t *testing.T) {
	t.Setenv("AO_PORT", "3002")
	t.Setenv("ANTHROPIC_API_KEY", "secret")
	e, err := NewEngine("/fake/agent-browser", "/run/r1", "head", 9400, nil)
	is(t, "")(nil, err)
	defer e.Close(sysOps{})
	if n := len(socketPath(e.SocketDir, e.Namespace)); n > maxSocketPath || !strings.HasPrefix(e.SocketDir, "/tmp/litmus-") || len(filepath.Base(e.SocketDir)) != len("litmus-")+8 {
		t.Fatalf("socket dir %s (%d bytes)", e.SocketDir, n)
	}
	env := e.env()
	for k, want := range map[string]string{
		"AGENT_BROWSER_CDP": "http://127.0.0.1:9400", "AGENT_BROWSER_SOCKET_DIR": e.SocketDir, "AGENT_BROWSER_SESSION": e.Session, "AGENT_BROWSER_NAMESPACE": e.Namespace,
		"AGENT_BROWSER_CONTENT_BOUNDARIES": "1", "AGENT_BROWSER_MAX_OUTPUT": "50000", "AGENT_BROWSER_IDLE_TIMEOUT_MS": "300000", "AGENT_BROWSER_AUTO_CONNECT": "0",
		"HOME": e.home(), "TMPDIR": e.home(), "AGENT_BROWSER_CONFIG": filepath.Join(e.home(), "config.json"), "AO_PORT": "", "ANTHROPIC_API_KEY": "",
	} {
		if got := envValue(env, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(e.home(), "/run/r1/engine/") || envValue(env, "PATH") == "" {
		t.Errorf("home %s", e.home())
	}

	defer func(b string) { socketBase = b }(socketBase)
	socketBase = "/tmp/" + strings.Repeat("d", 80)
	if _, err := NewEngine("b", "/run", "head", 1, nil); err == nil || !strings.Contains(err.Error(), "socket path") {
		t.Fatalf("long socket dir accepted: %v", err)
	}
}

func TestEngineTimeoutCloseAndMissingBinary(t *testing.T) {
	f := &fakeEngine{}
	e := newEngine(EngineInfo{Binary: "/fake/agent-browser", RunDir: t.TempDir(), Session: "s", Namespace: "s", SocketDir: "/tmp/litmus-nothere"}, f.exec)
	begin := time.Now()
	if _, err := e.Run(ctx, 50*time.Millisecond, "hang"); err == nil || !strings.Contains(err.Error(), "agent-browser hang") || time.Since(begin) > 2*time.Second {
		t.Fatalf("timeout err = %v after %s", err, time.Since(begin))
	}
	done := make(chan error)
	go func() { _, err := e.Run(ctx, time.Minute, "hang"); done <- err }()
	time.Sleep(50 * time.Millisecond)
	e.Close(sysOps{})
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Close left the command running")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not stop the in-flight command")
	}

	_, err := newEngine(EngineInfo{}, f.exec).Run(ctx, time.Second, "snapshot") // no binary: reported at first use
	is(t, "TESTING_ENGINE_MISSING")(nil, err)

	// the real process runner kills on timeout and caps what it keeps
	begin = time.Now()
	tctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := realExec(tctx, "/bin/sleep", []string{"5"}, nil); err == nil || !strings.Contains(err.Error(), "killed") || time.Since(begin) > 3*time.Second {
		t.Fatalf("sleep not killed: %v after %s", err, time.Since(begin))
	}
	out, err := realExec(ctx, "/bin/sh", []string{"-c", "head -c 600000 /dev/zero | tr '\\0' a"}, nil)
	if err != nil || len(out) != outputCap {
		t.Fatalf("capped output %d bytes err %v", len(out), err)
	}
}

func TestEnginePinsTheAppTab(t *testing.T) {
	for _, c := range []struct {
		list, calls string
		moved       bool
	}{
		{"→ [t1] App - http://localhost:5173/#/x\n  [t2] about:blank - about:blank\n", "stream,tab,tab,snapshot,tab,tab,click", false},  // still on the app: refs stay valid
		{"  [t1] App - http://localhost:5173/#/x\n→ [t2] about:blank - about:blank\n", "stream,tab,tab,tab,snapshot,tab,tab,tab", true}, // a session's hidden page took over: back to t1, refs gone
	} {
		f := &fakeEngine{out: map[string]string{"tab": c.list}}
		e := newEngine(EngineInfo{Binary: "/fake/agent-browser", RunDir: t.TempDir(), Session: "s", Namespace: "s"}, f.exec)
		var err error
		for _, a := range [][]string{{"snapshot"}, {"tab", "list"}, {"click", "@e1"}} {
			_, err = e.Run(ctx, time.Second, a...)
		}
		if f.words() != c.calls || (err != nil) != c.moved || (c.moved && (!strings.Contains(err.Error(), "re-selected") || !reflect.DeepEqual(f.calls[3], []string{"tab", "t1"}))) {
			t.Errorf("calls %s (%v), err %v; want %s", f.words(), f.calls, err, c.calls)
		}
	}
}

func TestCleanupClosesEngineAndKillsItsDaemon(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	fs := &fakeSys{cmds: map[int]string{4242: "/fake/agent-browser", 4343: "/bin/other"}}
	svc.sys = fs.ops()
	run, _ := svc.store.Get("run_s1")
	info := run.Legs[0].Engine
	pidDir := filepath.Join(info.SocketDir, "namespaces", info.Namespace, "run")
	is(t, "")(nil, os.MkdirAll(pidDir, 0o700))
	is(t, "")(nil, os.WriteFile(filepath.Join(pidDir, info.Namespace+".pid"), []byte("4242"), 0o600))
	is(t, "")(nil, os.WriteFile(filepath.Join(pidDir, "other.pid"), []byte("4343"), 0o600)) // not an engine process: left alone

	out, err := svc.Cleanup(ctx, "s1")
	if err != nil || out.State != RunCleaned {
		t.Fatalf("cleanup %+v err %v", out, err)
	}
	if f.words() != "close" || fmt.Sprint(fs.log) != "[kill 4242]" {
		t.Fatalf("engine calls %q, process log %v", f.words(), fs.log)
	}
	if _, err := os.Stat(info.SocketDir); !os.IsNotExist(err) {
		t.Fatalf("socket dir left behind: %v", err)
	}
}

func TestResolveBinary(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "agent-browser", "agent-browser")
	is(t, "")(nil, os.MkdirAll(filepath.Dir(bin), 0o750))
	is(t, "")(nil, os.WriteFile(bin, nil, 0o700))
	for _, c := range []struct{ override, exe, want string }{
		{"", filepath.Join(root, "daemon", "ao"), bin}, {bin, "", bin}, {"/nonexistent/ab", "", ""}, {"", filepath.Join(t.TempDir(), "daemon", "ao"), ""},
	} {
		if got, err := resolveBinary(c.override, c.exe); got != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%+v: %q, %v", c, got, err)
		}
	}
}

func TestAfterShotRetriesOnceWhenTheWindowDoesNotPaint(t *testing.T) {
	timeout := fmt.Errorf("agent-browser screenshot: killed: %w", context.DeadlineExceeded)
	for _, c := range []struct {
		name      string
		failFirst int // after-shot attempts that time out
		wantShots int
		wantWarn  bool
	}{
		{"second try paints", 1, 2, false},
		{"still hidden: one retry, then the warning", 5, 2, true},
	} {
		svc, f, _, runDir := driveSvc(t)
		defer func(d time.Duration) { shotRetry = d }(shotRetry)
		shotRetry = 0
		tries := 0
		f.hook = func(args []string) {
			if args[0] == "screenshot" && strings.HasSuffix(args[1], "-after.png") {
				if tries++; tries <= c.failFirst {
					f.fail["screenshot"] = timeout
				} else {
					delete(f.fail, "screenshot")
				}
			}
		}
		res, err := svc.Drive(ctx, "s1", "click", []string{"e1"})
		rec, _ := res["receipt"].(actionRec)
		if err != nil || tries != c.wantShots || (len(rec.Warnings) > 0) != c.wantWarn {
			t.Fatalf("%s: after-shot attempts %d, receipt %+v, err %v", c.name, tries, rec, err)
		}
		if shot := filepath.Join(runDir, "receipts", "1-after.png"); (rec.After == shot) == c.wantWarn || fileExists(shot) == c.wantWarn {
			t.Errorf("%s: after %q, file present %t", c.name, rec.After, fileExists(shot))
		}
	}
}

func TestDriveNamesTheTargetFromTheLastSnapshot(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	f.out["snapshot"] = "- generic [ref=e1]\n  - button \"Go back\" [ref=e7]\n  - textbox \"Message the agent\" [ref=e9]\n  - button [disabled, ref=e11]\n"
	is(t, "")(svc.CheckAdd(ctx, "s1", "history", "going back works", ""))
	is(t, "")(svc.CheckStart(ctx, "s1", "c1"))
	drive := func(action string, args ...string) actionRec {
		t.Helper()
		res, err := svc.Drive(ctx, "s1", action, args)
		is(t, "")(nil, err)
		rec, _ := res["receipt"].(actionRec)
		return rec
	}
	for i, c := range []struct {
		action     string
		args       []string
		role, name string
	}{
		{"click", []string{"e7"}, "", ""}, // nothing was snapshotted yet
		{"snapshot", nil, "", ""},
		{"click", []string{"e7"}, "button", "Go back"},
		{"fill", []string{"@e9", "hello"}, "textbox", "Message the agent"},
		{"click", []string{"e11"}, "button", ""}, // a button without a name
		{"click", []string{"e99"}, "", ""},       // a ref the snapshot did not list
		{"press", []string{"Enter"}, "", ""},
	} {
		if c.action == "snapshot" {
			drive("snapshot", "-i")
			continue
		}
		if rec := drive(c.action, c.args...); rec.TargetRole != c.role || rec.TargetName != c.name {
			t.Errorf("step %d %s %v: target %q %q, want %q %q", i, c.action, c.args, rec.TargetRole, rec.TargetName, c.role, c.name)
		}
	}
	checks, _ := svc.store.Checks("run_s1")
	if rc := checks[0].Receipts; len(rc) != 6 || rc[1].TargetName != "Go back" || rc[2].TargetRole != "textbox" || rc[2].TargetName != "Message the agent" {
		t.Fatalf("receipts %+v", rc)
	}
	data, _ := os.ReadFile(filepath.Join(runDir, "actions.jsonl"))
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 6 || !strings.Contains(lines[1], `"targetRole":"button","targetName":"Go back"`) || strings.Contains(lines[0], "target") {
		t.Fatalf("actions.jsonl:\n%s", data)
	}
}

// A click records where the pointer went, read from the element's box before the click and the viewport size.
func TestDriveClickRecordsItsPoint(t *testing.T) {
	svc, f, _, runDir := driveSvc(t)
	f.out["get"] = `{"x":10,"y":20,"width":100,"height":40}`
	run, _ := svc.store.Get("run_s1")
	e := svc.engineFor(run.ID, run.Legs[0])
	f.out["eval"] = "--- AGENT_BROWSER_PAGE_CONTENT nonce=999999 origin=app ---\n1280 720\n--- END_AGENT_BROWSER_PAGE_CONTENT nonce=999999 ---"
	size := []float64{1280, 720}
	e.connectPage = func(context.Context) (*pageCDP, error) {
		return &pageCDP{request: func(_ context.Context, method string, params, out any) error {
			if method != "Runtime.evaluate" || params.(map[string]any)["expression"] != "[innerWidth, innerHeight]" {
				t.Fatalf("unexpected viewport request: %s %v", method, params)
			}
			data, _ := json.Marshal(map[string]any{"result": map[string]any{"value": size}})
			return json.Unmarshal(data, out)
		}}, nil
	}
	res, err := svc.Drive(ctx, "s1", "click", []string{"e1"})
	rec, _ := res["receipt"].(actionRec)
	if want := (&clickAt{X: 60, Y: 40, W: 1280, H: 720}); err != nil || !reflect.DeepEqual(rec.Click, want) {
		t.Fatalf("click point %+v, want %+v, err %v", rec.Click, want, err)
	}
	if got := f.find("get"); !reflect.DeepEqual(got, []string{"get", "box", "@e1"}) {
		t.Fatalf("box asked as %v", got)
	}
	if acts := readActions(runDir); len(acts) != 1 || !reflect.DeepEqual(acts[0].Click, rec.Click) {
		t.Fatalf("actions.jsonl lost the point: %+v", acts)
	}
	f.out["get"] = "x: 5 y: 6 width: 10 height: 20" // plain text works too
	size = []float64{800, 600}
	res, _ = svc.Drive(ctx, "s1", "click", []string{"e1"})
	if rec, _ = res["receipt"].(actionRec); !reflect.DeepEqual(rec.Click, &clickAt{X: 10, Y: 16, W: 800, H: 600}) {
		t.Fatalf("plain text box: %+v", rec.Click)
	}
	f.fail["get"] = errors.New("no such element") // best effort: the click still goes ahead, without a point
	res, err = svc.Drive(ctx, "s1", "click", []string{"e1"})
	if rec, _ = res["receipt"].(actionRec); err != nil || rec.Click != nil {
		t.Fatalf("a failed box lookup: %+v, err %v", rec.Click, err)
	}
	f.calls = nil
	if _, err := svc.Drive(ctx, "s1", "fill", []string{"e1", "x"}); err != nil || f.find("get") != nil {
		t.Fatalf("fill unexpectedly read a target box: %v %v", f.calls, err)
	}
}
