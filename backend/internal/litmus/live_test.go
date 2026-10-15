package litmus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var refRe = regexp.MustCompile(`(?:ref=|@)(e[0-9]+)`)

// TestLiveHeadOfPR6394 prepares, launches (its seed spawns a real Claude Code chat session) and drives the
// head of PR 6394 in a temp data dir, using this checkout as the clone source. Run once:
// LITMUS_LIVE=1 go test -run TestLive -timeout 15m -v ./internal/litmus/
func TestLiveHeadOfPR6394(t *testing.T) {
	if os.Getenv("LITMUS_LIVE") != "1" {
		t.Skip("set LITMUS_LIVE=1")
	}
	src, _ := filepath.Abs("../../..")
	data, err := os.MkdirTemp("/tmp", "lv") // short: the target binds a unix socket beside running.json (104-byte path limit)
	is(t, "")(nil, err)
	svc := New(NewStore(data), nil).WithRepos(func(context.Context, ...string) (string, error) { return src, nil })
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	if svc.binary == "" { // the test binary sits in a temp dir, not beside the daemon
		if svc.binary, err = resolveBinary(filepath.Join(src, "frontend", "agent-browser", "agent-browser"), ""); err != nil {
			t.Fatal(err)
		}
	}
	began, lastLap := time.Now(), time.Now()
	lap := func(what string) {
		t.Logf("%-24s +%.1fs (total %.1fs)", what, time.Since(lastLap).Seconds(), time.Since(began).Seconds())
		lastLap = time.Now()
	}
	t.Cleanup(func() { _, _ = svc.Cleanup(ctx, "live"); _ = os.RemoveAll(data) })

	leg, err := svc.prepareLeg(ctx, DefaultRepo, r, "head", "e2204e0f361867a28310e71421a4227ab1d09544")
	is(t, "")(nil, err)
	lap("prepare")
	is(t, "")(nil, svc.store.Save(Run{ID: "run_live", SessionID: "live", Repo: DefaultRepo, PRNumber: 6394, HeadSHA: "e2204e0f361867a28310e71421a4227ab1d09544", State: RunPrepared, CreatedAt: time.Now(), Deadline: time.Now().Add(RunBudget), Legs: []Leg{leg}}))
	is(t, "")(svc.CheckAdd(ctx, "live", "open chat", "the sample chat opens with a message composer", ""))
	is(t, "")(svc.CheckAdd(ctx, "live", "model menu", "the model control opens a menu with Model, Effort and Plan Mode", ""))
	run, err := svc.Launch(ctx, "live", "head")
	is(t, "")(nil, err)
	lap("launch + seed")
	leg = run.Legs[0]

	drive := func(action string, args ...string) string {
		t.Helper()
		res, err := svc.Drive(ctx, "live", action, args)
		if err != nil {
			t.Fatalf("%s %v: %v", action, args, err)
		}
		return res["output"].(string)
	}
	// until polls snapshots (the renderer is still starting services at first) for lines matching pat.
	until := func(pat string, limit time.Duration) (snap string, lines []string) {
		re := regexp.MustCompile(pat)
		for end := time.Now().Add(limit); ; time.Sleep(3 * time.Second) {
			snap, lines = drive("snapshot"), nil
			for _, l := range strings.Split(snap, "\n") {
				if re.MatchString(l) {
					lines = append(lines, l)
				}
			}
			if len(lines) > 0 || time.Now().After(end) {
				return snap, lines
			}
		}
	}
	click := func(pat, what string) string { // state-changing: takes a receipt
		t.Helper()
		snap, lines := until(pat, 60*time.Second)
		if len(lines) == 0 {
			t.Fatalf("no %s in the snapshot:\n%.2500s", what, snap)
		}
		drive("click", refRe.FindStringSubmatch(lines[0])[1])
		return lines[0]
	}

	is(t, "")(svc.CheckStart(ctx, "live", "c1"))
	t.Logf("tab list: %.300s", drive("tab"))
	lap("tab list")
	click(`Toggle Sample project \S+ sessions.*ref=e[0-9]+`, "project row") // the project row starts collapsed
	t.Logf("sidebar: %s", click(`Sample chat \S+.*ref=e[0-9]+`, "sample chat session"))
	lap("opened the sample chat")

	snap, composer := until(`(?i)textbox|composer|combobox "message`, 30*time.Second) // AO's chat input is a combobox named "Message the agent"
	t.Logf("composer lines: %q", composer)
	if len(composer) == 0 {
		t.Errorf("no chat composer after opening the sample chat:\n%.3000s", snap)
	}
	is(t, "")(svc.CheckEnd(ctx, "live", "c1", CheckPass, "composer present: "+strings.Join(composer, "; "), false, 0))
	is(t, "")(svc.CheckStart(ctx, "live", "c2"))
	all := strings.Split(snap, "\n")
	for i, l := range all {
		if strings.Contains(l, "Model and reasoning effort") {
			t.Logf("model control, snapshot lines %d-%d:\n%s", max(0, i-14), i+6, strings.Join(all[max(0, i-14):min(len(all), i+6)], "\n"))
			click(`Model and reasoning effort.*ref=e[0-9]+`, "model control") // a fresh snapshot: a page the app opens can take the engine's tab and drop refs
			_, items := until(`menuitem`, 10*time.Second)
			t.Logf("menu items after opening it: %.1500q", strings.Join(items, "\n"))
			drive("press", "Escape")
			break
		}
	}

	is(t, "")(svc.CheckEnd(ctx, "live", "c2", CheckPass, "model control clicked, Escape pressed", false, 0))
	v, err := svc.Submit(ctx, "live", "live check", "")
	is(t, "")(nil, err)
	lap("submit")
	runDir, art := filepath.Join(svc.store.root, "run_live"), filepath.Join(data, "artifacts", "live")
	for d, video := range map[string]string{runDir: "head/head.mp4", art: "head.mp4"} { // the head leg alone ran: no base.mp4
		for _, f := range []string{"verdict.md", "receipts/1-after.png", video} {
			exists(t, filepath.Join(d, f))
		}
	}
	exists(t, filepath.Join(art, "index.md"))
	md, _ := os.ReadFile(filepath.Join(runDir, "verdict.md"))
	t.Logf("overall %s; verdict.md:\n%.1800s", v.Overall, md)
	names, _ := filepath.Glob(filepath.Join(art, "*"))
	t.Logf("artifact dir: %q", names)
	if out, err := exec.Command("/opt/homebrew/bin/ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width,height,duration", "-of", "csv=p=0", filepath.Join(art, "head.mp4")).Output(); err == nil {
		t.Logf("head.mp4 streams: %s", out)
	}
	actions, err := os.ReadFile(filepath.Join(runDir, "actions.jsonl"))
	if n := strings.Count(string(actions), "\n"); err != nil || n == 0 {
		t.Fatalf("actions.jsonl: %d lines, err %v", n, err)
	}
	if shot, err := os.Stat(filepath.Join(runDir, "receipts", "1-after.png")); err != nil || shot.Size() == 0 {
		t.Fatalf("first after-shot: %v", err)
	}
	t.Logf("actions.jsonl:\n%.2500s", actions)
	enginePID := 0
	pidFiles, _ := filepath.Glob(filepath.Join(leg.Engine.SocketDir, "namespaces", "*", "run", "*.pid"))
	for _, f := range pidFiles {
		b, _ := os.ReadFile(f)
		enginePID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}

	_, err = svc.Cleanup(ctx, "live")
	is(t, "")(nil, err)
	lap("cleanup")
	for _, p := range []int{leg.DaemonPort, leg.DebugPort} {
		if svc.sys.portBusy(p) {
			t.Errorf("port %d still open after cleanup", p)
		}
	}
	if _, alive := svc.sys.startTime(enginePID); alive && enginePID > 0 {
		t.Errorf("engine daemon %d survived cleanup", enginePID)
	}
	if _, err := os.Stat(leg.Engine.SocketDir); err == nil {
		t.Errorf("engine socket dir %s survived cleanup", leg.Engine.SocketDir)
	}
	for pid, cmd := range svc.sys.procs() {
		if strings.Contains(cmd, svc.store.root) {
			t.Errorf("leftover process %d: %.200s", pid, cmd)
		}
	}
}

// TestLiveHeadTwice launches head, closes the run with `submit --blocked`, then launches head again for a new
// session in the same data dir: the second seed must not meet the first run's worktree or branch in the shared clone.
// LITMUS_LIVE=1 go test -run TestLiveHeadTwice -timeout 20m -v ./internal/litmus/
func TestLiveHeadTwice(t *testing.T) {
	if os.Getenv("LITMUS_LIVE") != "1" {
		t.Skip("set LITMUS_LIVE=1")
	}
	src, _ := filepath.Abs("../../..")
	data, err := os.MkdirTemp("/tmp", "lv") // short: the target binds a unix socket beside running.json
	is(t, "")(nil, err)
	svc := New(NewStore(data), nil).WithRepos(func(context.Context, ...string) (string, error) { return src, nil })
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	if svc.binary, err = resolveBinary(filepath.Join(src, "frontend", "agent-browser", "agent-browser"), ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = svc.Cleanup(ctx, "live1"); _, _ = svc.Cleanup(ctx, "live2"); _ = os.RemoveAll(data) })
	const sha = "e2204e0f361867a28310e71421a4227ab1d09544"
	launch := func(session string) (Run, error) {
		leg, err := svc.prepareLeg(ctx, DefaultRepo, r, "head", sha)
		is(t, "")(nil, err)
		is(t, "")(nil, svc.store.Save(Run{ID: "run_" + session, SessionID: session, Repo: DefaultRepo, PRNumber: 6394, HeadSHA: sha, State: RunPrepared, CreatedAt: time.Now(), Deadline: time.Now().Add(RunBudget), Legs: []Leg{leg}}))
		is(t, "")(svc.CheckAdd(ctx, session, "open chat", "the sample chat opens", ""))
		is(t, "")(svc.CheckAdd(ctx, session, "model menu", "the model control opens a menu", ""))
		return svc.Launch(ctx, session, "head")
	}
	run, err := launch("live1")
	is(t, "")(nil, err)
	clone := run.Legs[0].Worktree
	_, err = svc.SubmitBlocked(ctx, "live1", "second launch must not collide with this run")
	is(t, "")(nil, err)
	branches, err := svc.git(ctx, clone, "for-each-ref", "--format=%(refname:short)", "refs/heads/ao/litmus-*")
	is(t, "")(nil, err)
	worktrees, _ := svc.git(ctx, clone, "worktree", "list", "--porcelain")
	t.Logf("after run 1: litmus branches %q, worktrees %d", branches, strings.Count(worktrees, "worktree "))
	if branches != "" || strings.Count(worktrees, "worktree ") != 1 {
		t.Fatalf("the shared clone kept run 1's leftovers:\n%s\n%s", branches, worktrees)
	}

	run, err = launch("live2")
	if err != nil {
		t.Fatalf("second launch: %v", err)
	}
	seedLog, _ := os.ReadFile(filepath.Join(svc.store.legDir("run_live2", "head"), "logs", "seed.log"))
	if strings.Contains(string(seedLog), "BRANCH_CHECKED_OUT_ELSEWHERE") {
		t.Fatalf("second seed collided:\n%.1500s", seedLog)
	}
	t.Logf("second launch ok, state %s; seed.log:\n%.1200s", run.Legs[0].State, seedLog)
}

// TestLiveHeadAndBaseVideos launches head and base of PR 6394, drives two actions on each, submits and checks that
// every leg has its own recording and that base-cut.mp4 is captioned with base actions only.
// LITMUS_LIVE=1 go test -run TestLiveHeadAndBaseVideos -timeout 25m -v ./internal/litmus/
func TestLiveHeadAndBaseVideos(t *testing.T) {
	if os.Getenv("LITMUS_LIVE") != "1" {
		t.Skip("set LITMUS_LIVE=1")
	}
	src, _ := filepath.Abs("../../..")
	data, err := os.MkdirTemp("/tmp", "lv") // short: the target binds a unix socket beside running.json
	is(t, "")(nil, err)
	svc := New(NewStore(data), nil).WithRepos(func(context.Context, ...string) (string, error) { return src, nil })
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	if svc.binary, err = resolveBinary(filepath.Join(src, "frontend", "agent-browser", "agent-browser"), ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = svc.Cleanup(ctx, "live"); _ = os.RemoveAll(data) })
	shas := map[string]string{"head": "e2204e0f361867a28310e71421a4227ab1d09544", "base": "0ca80cfc370b21443c4a2b9d22199c047b8c74e7"}
	var legs []Leg
	for _, name := range []string{"head", "base"} {
		leg, err := svc.prepareLeg(ctx, DefaultRepo, r, name, shas[name])
		is(t, "")(nil, err)
		legs = append(legs, leg)
	}
	is(t, "")(nil, svc.store.Save(Run{ID: "run_live", SessionID: "live", Repo: DefaultRepo, PRNumber: 6394, HeadSHA: shas["head"], BaseSHA: shas["base"], State: RunPrepared, CreatedAt: time.Now(), Deadline: time.Now().Add(RunBudget), Legs: legs}))
	is(t, "")(svc.CheckAdd(ctx, "live", "open chat", "the sample chat opens", ""))
	is(t, "")(svc.CheckAdd(ctx, "live", "open chat", "the sample chat opens", "base"))
	for _, leg := range []string{"head", "base"} {
		_, err := svc.Launch(ctx, "live", leg)
		is(t, "")(nil, err)
	}
	run, _ := svc.store.Get("run_live")
	for _, l := range run.Legs {
		log, _ := os.ReadFile(filepath.Join(svc.store.root, "run_live", l.Name, "engine.log"))
		t.Logf("%s: recording started %s; engine.log: %.400q", l.Name, l.RecordStartedAt.Format(time.TimeOnly), log)
		if l.RecordStartedAt.IsZero() {
			t.Fatalf("%s: no recording started", l.Name)
		}
	}
	act := func(leg string) { // the project row toggle, then the chat session: two receipts on this leg
		for _, pat := range []string{`Toggle Sample project live sessions.*ref=e[0-9]+`, `Sample chat live.*ref=e[0-9]+`} {
			re, line, snap := regexp.MustCompile(pat), "", ""
			for end := time.Now().Add(60 * time.Second); line == "" && time.Now().Before(end); time.Sleep(3 * time.Second) {
				res, err := svc.Drive(ctx, "live", "snapshot", []string{"--leg", leg})
				is(t, "")(nil, err)
				snap = res["output"].(string)
				for _, l := range strings.Split(snap, "\n") {
					if re.MatchString(l) {
						line = l
						break
					}
				}
			}
			if line == "" {
				t.Fatalf("%s: no line matching %s in:\n%.1500s", leg, pat, snap)
			}
			_, err := svc.Drive(ctx, "live", "click", []string{refRe.FindStringSubmatch(line)[1], "--leg", leg})
			is(t, "")(nil, err)
		}
	}
	for i, leg := range []string{"head", "base"} {
		id := fmt.Sprintf("c%d", i+1)
		is(t, "")(svc.CheckStart(ctx, "live", id))
		act(leg)
		is(t, "")(svc.CheckEnd(ctx, "live", id, map[string]CheckState{"head": CheckPass, "base": CheckFail}[leg], leg+" live", false, 0))
	}
	v, err := svc.Submit(ctx, "live", "live video check", "")
	is(t, "")(nil, err)
	runDir, art := filepath.Join(svc.store.root, "run_live"), filepath.Join(data, "artifacts", "live")
	for _, leg := range []string{"head", "base"} {
		exists(t, recordingPath(runDir, leg))
		exists(t, filepath.Join(art, leg+".mp4"))
		exists(t, filepath.Join(art, leg+"-cut.mp4"))
		out, err := exec.Command("/opt/homebrew/bin/ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", recordingPath(runDir, leg)).Output()
		d, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		if err != nil || perr != nil || d <= 0 {
			t.Errorf("%s.mp4 duration %q: %v %v", leg, out, err, perr)
		}
		t.Logf("%s.mp4 duration %.2fs", leg, d)
	}
	srt := filepath.Join(data, "base-cut-extracted.srt") // the captions live in the mp4 as a soft track
	is(t, "")(nil, svc.media.Run(ctx, "-y", "-i", cutPath(runDir, "base"), "-map", "0:s:0", srt))
	text, _ := os.ReadFile(srt)
	var caps []string
	for _, l := range strings.Split(string(text), "\n") {
		if strings.HasPrefix(l, "Step ") {
			caps = append(caps, l)
		}
	}
	if len(caps) < 2 || strings.Contains(string(text), "@e") {
		t.Errorf("base-cut.mp4 captions: %q", caps)
	}
	t.Logf("overall %s; base-cut.mp4 caption lines: %q", v.Overall, caps)
	_, err = svc.Cleanup(ctx, "live")
	is(t, "")(nil, err)
	for _, l := range run.Legs {
		for _, p := range []int{l.DaemonPort, l.DebugPort} {
			if svc.sys.portBusy(p) {
				t.Errorf("%s port %d still open after cleanup", l.Name, p)
			}
		}
	}
}

// TestLiveOcclusionFlags launches the head of PR 6394 (without the recipe's seed, so no chat session is spawned),
// checks the Electron process was started with the occlusion flags and takes one screenshot through the engine.
// LITMUS_LIVE=1 go test -run TestLiveOcclusionFlags -timeout 20m -v ./internal/litmus/
func TestLiveOcclusionFlags(t *testing.T) {
	if os.Getenv("LITMUS_LIVE") != "1" {
		t.Skip("set LITMUS_LIVE=1")
	}
	src, _ := filepath.Abs("../../..")
	data, err := os.MkdirTemp("/tmp", "lv") // short: the target binds a unix socket beside running.json
	is(t, "")(nil, err)
	svc := New(NewStore(data), nil).WithRepos(func(context.Context, ...string) (string, error) { return src, nil })
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	if svc.binary, err = resolveBinary(filepath.Join(src, "frontend", "agent-browser", "agent-browser"), ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = svc.Cleanup(ctx, "live"); _ = os.RemoveAll(data) })
	r.Seed = nil // the flags do not depend on the seeded chat session
	recipe, err := yaml.Marshal(r)
	is(t, "")(nil, err)
	is(t, "")(nil, os.MkdirAll(filepath.Dir(svc.store.recipePath(DefaultRepo)), 0o750))
	is(t, "")(nil, os.WriteFile(svc.store.recipePath(DefaultRepo), recipe, 0o600))

	const sha = "e2204e0f361867a28310e71421a4227ab1d09544"
	leg, err := svc.prepareLeg(ctx, DefaultRepo, r, "head", sha)
	is(t, "")(nil, err)
	is(t, "")(nil, svc.store.Save(Run{ID: "run_live", SessionID: "live", Repo: DefaultRepo, PRNumber: 6394, HeadSHA: sha, State: RunPrepared, CreatedAt: time.Now(), Deadline: time.Now().Add(RunBudget), Legs: []Leg{leg}}))
	is(t, "")(svc.CheckAdd(ctx, "live", "a", "x", ""))
	is(t, "")(svc.CheckAdd(ctx, "live", "b", "y", ""))
	run, err := svc.Launch(ctx, "live", "head")
	is(t, "")(nil, err)
	leg = run.Legs[0]
	port, pid := "--remote-debugging-port="+strconv.Itoa(leg.DebugPort), 0
	for p, cmd := range svc.sys.procs() { // the browser process: Chromium's helpers carry --type= and not our flags
		if strings.Contains(cmd, port) && strings.Contains(cmd, "Electron") && !strings.Contains(cmd, " --type=") {
			pid = p
		}
	}
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || pid == 0 {
		t.Fatalf("electron pid %d: %v", pid, err)
	}
	t.Logf("electron pid %d command line: %s", pid, out)
	for _, flag := range append([]string{port}, occlusionFlags...) {
		if n := strings.Count(string(out), " "+flag); n != 1 {
			t.Errorf("%s appears %d times in the Electron command line", flag, n)
		}
	}
	if _, err := svc.Drive(ctx, "live", "tab", nil); err != nil { // the engine's first call also connects; time the screenshot alone
		t.Fatal(err)
	}
	began := time.Now()
	res, err := svc.Drive(ctx, "live", "screenshot", nil)
	is(t, "")(nil, err)
	shot, _ := res["path"].(string)
	if st, err := os.Stat(shot); err != nil || st.Size() == 0 {
		t.Fatalf("screenshot %q: %v", shot, err)
	}
	t.Logf("screenshot returned in %d ms: %s", time.Since(began).Milliseconds(), shot)
}
