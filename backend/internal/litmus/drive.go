package litmus

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Tests shorten these.
var (
	settleDelay = 400 * time.Millisecond // let the UI react before the after-shot
	settleLoad  = 3 * time.Second        // cap on `wait --load`; a failure is recorded as a replay stop warning
	shotRetry   = time.Second            // pause before the one retry of an after-shot that timed out
)

// splitLeg pulls "--leg <name>" or "--leg=<name>" out of args; the CLI appends it when --leg is given.
func splitLeg(args []string) (leg string, rest []string) {
	if n := len(args); n >= 2 && args[n-2] == "--leg" {
		return args[n-1], args[:n-2]
	} else if n > 0 && strings.HasPrefix(args[n-1], "--leg=") {
		return strings.TrimPrefix(args[n-1], "--leg="), args[:n-1]
	}
	return "", args
}

// pickLeg names the launched leg an action applies to; without --leg it must be unambiguous.
func pickLeg(run Run, name string) (Leg, error) {
	var live []Leg
	for _, l := range run.Legs {
		if l.State == "running" && l.Engine != nil {
			live = append(live, l)
		}
	}
	switch {
	case name != "":
		if i := slices.IndexFunc(live, func(l Leg) bool { return l.Name == name }); i >= 0 {
			return live[i], nil
		}
		return Leg{}, conflict("TESTING_LEG_NOT_LAUNCHED", "leg "+name+" is not launched; run `ao testing launch "+name+"`")
	case len(live) == 1:
		return live[0], nil
	case len(live) == 0:
		return Leg{}, conflict("TESTING_LEG_NOT_LAUNCHED", "no leg is launched; run `ao testing launch "+run.targets()[0].Name+"`")
	}
	return Leg{}, invalid("TESTING_LEG_REQUIRED", "multiple legs are launched; name one with --leg")
}

// inRun resolves an output path the worker named: relative paths land in the run dir, and nothing may leave it.
func inRun(runDir, p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(runDir, p)
	}
	if rel, err := filepath.Rel(runDir, filepath.Clean(p)); err != nil || !filepath.IsLocal(rel) {
		return "", invalid("TESTING_PATH_INVALID", "output files must stay inside "+runDir)
	}
	return filepath.Clean(p), os.MkdirAll(filepath.Dir(p), 0o750)
}

// Drive runs one worker action on a launched leg. It is refused unless the run is
// running, inside its deadline and the named leg is launched.
func (s *Service) Drive(ctx context.Context, sessionID, action string, args []string) (map[string]any, error) {
	legName, args := splitLeg(args)
	raw := action == "browser"
	if raw {
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return nil, invalid("TESTING_ARGS_INVALID", "browser needs an agent-browser command")
		}
		action, args = args[0], args[1:]
	}
	if strings.TrimSpace(action) == "" {
		return nil, invalid("TESTING_ARGS_INVALID", "browser needs an agent-browser command")
	}
	receipt := true
	switch action {
	case "snapshot", "is", "console", "errors", "wait", "help", "--help", "--version", "skills", "session":
		receipt = false
	case "network":
		receipt = len(args) == 0 || args[0] != "requests"
	case "tab":
		receipt = len(args) > 0 && args[0] != "list"
	case "screenshot":
		receipt = raw
	}
	for _, a := range append([]string{action}, args...) {
		l := strings.ToLower(a)
		if slices.ContainsFunc(forbiddenFlags, func(f string) bool { return l == f || strings.HasPrefix(l, f+"=") }) {
			return nil, invalid("TESTING_ARGS_INVALID", "flag "+a+" is managed by Litmus")
		}
	}
	if action == "connect" {
		return nil, invalid("TESTING_ARGS_INVALID", "the test browser connection is managed by Litmus")
	}
	s.mu.Lock()
	run, err := s.load(sessionID)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := s.appDeadline(run); err != nil {
		return nil, err
	}
	if run.State != RunRunning {
		return nil, conflict("TESTING_RUN_NOT_RUNNING", fmt.Sprintf("run %s is %s; launch a leg first", run.ID, run.State))
	}
	leg, err := pickLeg(run, legName)
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(s.store.root, run.ID)
	cmd, file := append([]string{action}, args...), ""
	if action == "screenshot" && !raw {
		if len(args) > 1 {
			return nil, invalid("TESTING_ARGS_INVALID", "usage: screenshot [path]")
		}
		name := filepath.Join("shots", fmt.Sprintf("%s-%d.png", leg.Name, time.Now().UnixMilli()))
		if len(args) == 1 {
			name = args[0]
		}
		file, err = inRun(runDir, name)
		if err != nil {
			return nil, err
		}
		cmd = []string{action, file}
	}

	e := s.engineFor(run.ID, leg)
	e.mu.Lock()
	defer e.mu.Unlock()
	var target refInfo // what the last snapshot called the element about to be acted on, read before the page changes
	if len(cmd) > 1 {
		target = e.refs["@"+strings.TrimPrefix(cmd[1], "@")]
	}
	var click *clickAt
	var box *ElementBox
	if action == "click" && len(cmd) > 1 && !strings.HasPrefix(cmd[1], "-") { // read before the click: the page may move or close the element
		var pointErr error
		click, box, pointErr = e.clickTarget(ctx, cmd[1])
		if pointErr != nil {
			if isTabDrift(pointErr) {
				return nil, apierr.Unavailable("TESTING_ENGINE_FAILED", pointErr.Error())
			}
			slog.Default().Debug("litmus: click point unavailable", "ref", cmd[1], "error", pointErr)
		}
	}
	if receipt && e.last == "" { // nothing before it yet: this leg's first state change gets a before-shot of its own
		p := filepath.Join(runDir, "receipts", leg.Name+"-before.png")
		if err := s.shoot(ctx, e, p); err == nil || fileExists(p) {
			e.last = p
			if err != nil {
				slog.Default().Warn("litmus: before-shot", "error", err)
			}
		}
	}
	t0 := time.Now()
	out, err := e.Run(ctx, cmdTimeout, cmd...)
	if err != nil {
		if receipt {
			// A covered click is a useful, reproducible result. Keep the same
			// after-shot and receipt evidence as a successful state change, then
			// return the refusal so the tester stops rather than guessing.
			rec := s.receipt(ctx, run.ID, e, leg.Name, action, cmd[1:], target, click, box, time.Since(t0), out, err.Error())
			res := map[string]any{"leg": leg.Name, "action": action, "output": out, "receipt": rec, "refused": true, "reason": err.Error()}
			if rec.After != "" {
				res["after"] = relPath(runDir, rec.After)
			}
			if snap, snapErr := e.Run(ctx, cmdTimeout, "snapshot", "-i"); snapErr == nil {
				e.refs = parseRefs(snap)
				res["snapshot"] = compactSnapshot(snap)
			}
			addDriveText(res, runDir, "")
			message := err.Error()
			if text, ok := res["text"].(string); ok && text != "" {
				message += "\n" + text
			}
			if action != "click" || !isCoveredClickError(err) {
				return res, apierr.Unavailable("TESTING_ENGINE_FAILED", message)
			}
			return res, apierr.Conflict("TESTING_ELEMENT_COVERED", "click refused because the element is covered: "+message, nil)
		}
		return nil, apierr.Unavailable("TESTING_ENGINE_FAILED", err.Error())
	}
	if action == "snapshot" {
		e.refs = parseRefs(out)
	}
	res := map[string]any{"leg": leg.Name, "action": action, "output": out}
	if action == "screenshot" && file != "" {
		if err := e.flatten(ctx, file); err != nil {
			res["warning"] = "opaque screenshot: " + err.Error()
		}
	}
	if file != "" {
		res["path"] = file
	}
	if receipt {
		rec := s.receipt(ctx, run.ID, e, leg.Name, action, cmd[1:], target, click, box, time.Since(t0), out, "")
		res["receipt"] = rec
		if rec.After != "" { // the tester reads this shot to judge anything visual
			res["after"] = relPath(runDir, rec.After)
		}
		if snap, err := e.Run(ctx, cmdTimeout, "snapshot", "-i"); err == nil {
			e.refs = parseRefs(snap)
			res["snapshot"] = compactSnapshot(snap)
		}
	}
	if action == "snapshot" {
		res["snapshot"] = compactSnapshot(out)
	}
	addDriveText(res, runDir, "")
	return res, nil
}

// isCoveredClickError recognizes agent-browser's refusal when another painted
// element receives the click. The engine has used both "covered" and
// "intercepted" wording across releases.
func isCoveredClickError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, word := range []string{"covered", "intercept", "obscur", "overlap"} {
		if strings.Contains(msg, word) {
			return true
		}
	}
	return false
}

var interactiveSnapshotRoles = map[string]bool{
	"button": true, "checkbox": true, "combobox": true, "link": true,
	"listbox": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true, "option": true, "radio": true,
	"searchbox": true, "slider": true, "spinbutton": true, "switch": true,
	"tab": true, "textbox": true, "treeitem": true,
}

// compactSnapshot keeps only named interactive refs and caps the returned text
// so a single action does not consume the tester's context window.
func compactSnapshot(snapshot string) string {
	var b strings.Builder
	for _, line := range strings.Split(snapshot, "\n") {
		m := snapRef.FindStringSubmatch(line)
		if len(m) < 2 || !interactiveSnapshotRoles[strings.ToLower(m[1])] {
			continue
		}
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}
		if b.Len() > 0 && b.Len()+1+len(line) > 2048 {
			break
		}
		if b.Len() == 0 && len(line) > 2048 {
			return line[:validUTF8Prefix(line, 2048)]
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

func validUTF8Prefix(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && (s[n]&0xc0) == 0x80 {
		n--
	}
	return n
}

// addDriveText is the compact human-readable output used by `ao testing`.
// JSON callers still receive the structured fields alongside it.
func addDriveText(res map[string]any, runDir, fallbackSnapshot string) {
	snapshot, _ := res["snapshot"].(string)
	if snapshot == "" {
		snapshot = fallbackSnapshot
	}
	var lines []string
	if rec, ok := res["receipt"].(actionRec); ok {
		after := ""
		if rec.After != "" {
			after = relPath(runDir, rec.After)
		}
		line := fmt.Sprintf("receipt %d: %s %dms", rec.Seq, cmp.Or(after, "(none)"), rec.MS)
		line += " page errors: " + cmp.Or(strings.ReplaceAll(rec.PageErrors, "\n", " | "), "none")
		if rec.Reason != "" {
			line += " reason: " + rec.Reason
		}
		lines = append(lines, line)
		if rec.ConsoleTail != "" {
			lines = append(lines, rec.ConsoleTail)
		}
	}
	if snapshot != "" {
		lines = append(lines, snapshot)
	}
	if action, _ := res["action"].(string); action != "snapshot" || snapshot == "" {
		if output, _ := res["output"].(string); output != "" {
			lines = append(lines, output)
		}
	}
	res["text"] = strings.Join(lines, "\n")
}

// clickAt is where a click lands: the element's centre and the viewport it is in, both in CSS pixels, so the point
// can be placed on a screenshot of any size.
type clickAt struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// ElementBox is the CSS-pixel box returned by `get box` for a clicked element.
// It is kept separately from clickAt: clickAt also carries the viewport used to
// draw the pointer ring on an after-shot.
type ElementBox struct {
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	W              float64 `json:"w"`
	H              float64 `json:"h"`
	ViewportWidth  float64 `json:"viewportWidth,omitempty"`
	ViewportHeight float64 `json:"viewportHeight,omitempty"`
}

const pointTimeout = 5 * time.Second

// boxField reads one field (x, y, width, height) from `get box` output, whether it prints JSON or "x: 1 y: 2".
func boxField(out, name string) (float64, bool) {
	m := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `[^\w\s+-]{0,3}\s*([-+]?\d+(?:\.\d+)?)`).FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, err == nil
}

func isTabDrift(err error) bool {
	return errors.Is(err, errTabDrift)
}

// clickTarget reads the element box and viewport before a click. The box is
// returned even when the viewport lookup fails, so a refused click can still
// leave useful evidence.
func (e *Engine) clickTarget(ctx context.Context, ref string) (*clickAt, *ElementBox, error) {
	if _, err := e.Run(ctx, pointTimeout, "scrollintoview", ref); err != nil {
		return nil, nil, err
	}
	box, err := e.Run(ctx, pointTimeout, "get", "box", ref)
	if err != nil {
		return nil, nil, err
	}
	x, okx := boxField(box, "x")
	y, oky := boxField(box, "y")
	w, okw := boxField(box, "width")
	h, okh := boxField(box, "height")
	if !okx || !oky || !okw || !okh {
		return nil, nil, errors.New("get box returned no usable coordinates")
	}
	element := &ElementBox{X: x, Y: y, W: w, H: h}
	ctx, cancel := context.WithTimeout(ctx, pointTimeout)
	defer cancel()
	p, err := e.pageCDP(ctx)
	if err != nil {
		return nil, element, err
	}
	if p.conn != nil {
		defer func() { _ = p.conn.CloseNow() }()
	}
	var size []float64
	if err := p.evaluate(ctx, "[innerWidth, innerHeight]", &size); err != nil {
		return nil, element, err
	}
	if len(size) != 2 {
		return nil, element, errors.New("viewport size was not returned")
	}
	vw, vh := size[0], size[1]
	if vw <= 0 || vh <= 0 {
		return nil, element, errors.New("viewport size was invalid")
	}
	element.ViewportWidth, element.ViewportHeight = vw, vh
	return &clickAt{X: x + w/2, Y: y + h/2, W: vw, H: vh}, element, nil
}

// shoot saves a screenshot and, on macOS, downsizes it with the built-in sips (no Go image dependency).
func (s *Service) shoot(ctx context.Context, e *Engine, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if _, err := e.Run(ctx, cmdTimeout, "screenshot", path); err != nil {
		return err
	}
	if err := e.flatten(ctx, path); err != nil {
		return err
	}
	_, _ = s.command(ctx, "", nil, "sips", "-Z", "1320", path)
	return nil
}

// actionRec is one line of actions.jsonl.
type actionRec struct {
	Seq         int         `json:"seq"`
	Leg         string      `json:"leg"`
	Action      string      `json:"action"`
	Theme       string      `json:"theme,omitempty"`
	Args        []string    `json:"args,omitempty"`
	Output      string      `json:"output,omitempty"`
	Before      string      `json:"before"`
	After       string      `json:"after"`
	ConsoleTail string      `json:"consoleTail"`
	At          time.Time   `json:"at"`
	MS          int64       `json:"ms"`
	Click       *clickAt    `json:"click,omitempty"` // click only: where the pointer went
	Box         *ElementBox `json:"box,omitempty"`   // click only: the element's CSS-pixel box
	PageErrors  string      `json:"pageErrors,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	Warnings    []string    `json:"warnings,omitempty"`
	TargetRole  string      `json:"targetRole,omitempty"` // from the last snapshot: the element acted on, for plain captions
	TargetName  string      `json:"targetName,omitempty"`
}

// receipt settles the UI, takes the after-shot and the console tail, appends the record to
// actions.jsonl. Its failures are warnings: the action already happened.
func (s *Service) receipt(ctx context.Context, runID string, e *Engine, leg, action string, args []string, target refInfo, click *clickAt, box *ElementBox, took time.Duration, output, reason string) actionRec {
	s.mu.Lock()
	seq, err := s.nextSeq(runID, leg)
	s.mu.Unlock()
	output = strings.ToValidUTF8(output[max(0, len(output)-(4<<10)):], "")
	rec := actionRec{Output: output, Seq: seq, Leg: leg, Action: action, Args: args, Before: e.last, At: s.clock(), MS: took.Milliseconds(), TargetRole: target.role, TargetName: target.name, Click: click, Box: box, Reason: reason}
	warn := func(what string, err error) { rec.Warnings = append(rec.Warnings, what+": "+err.Error()) }
	if err != nil {
		warn("receipt sequence", err)
		return rec
	}

	select {
	case <-time.After(settleDelay):
	case <-ctx.Done():
	}
	if _, err := e.Run(ctx, settleLoad, "wait", "--load", "load"); err != nil && ctx.Err() == nil {
		warn("load", err)
	}
	rec.After = filepath.Join(e.RunDir, "receipts", fmt.Sprintf("%s-%d-after.png", leg, seq))
	err = s.shoot(ctx, e, rec.After)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil { // a window the OS hid often paints on the next frame
		select {
		case <-time.After(shotRetry):
			err = s.shoot(ctx, e, rec.After)
		case <-ctx.Done():
		}
	}
	if err != nil {
		if !fileExists(rec.After) {
			rec.After = ""
		} else {
			e.last = rec.After
		}
		warn("after-shot", err)
	} else {
		e.last = rec.After
	}
	if rec.Theme, err = e.theme(ctx); err != nil {
		warn("theme", err)
	}
	if out, err := e.Run(ctx, cmdTimeout, "errors"); err != nil {
		warn("page errors", err)
	} else {
		rec.PageErrors = compactErrors(out)
	}
	if out, err := e.Run(ctx, cmdTimeout, "console"); err != nil {
		warn("console", err)
	} else {
		rec.ConsoleTail = consoleErrorTail(out)
	}

	s.attach(runID, &rec)
	return rec
}

func compactErrors(out string) string {
	out = strings.TrimSpace(out)
	normalized := strings.ToLower(strings.TrimSpace(strings.TrimRight(out, ".")))
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "", "no error", "no errors", "no page error", "no page errors", "no errors found", "no page errors found":
		return ""
	}
	return clip(out, 2000)
}

// consoleErrorTail keeps the useful part of the console without returning a
// normal log tail on every action. A page error is reported separately.
func consoleErrorTail(out string) string {
	tail := lastLines([]byte(out), 20)
	low := strings.ToLower(tail)
	if !strings.Contains(low, "error") && !strings.Contains(low, "exception") && !strings.Contains(low, "uncaught") && !strings.Contains(low, "failed") {
		return ""
	}
	return strings.ToValidUTF8(tail[max(0, len(tail)-(4<<10)):], "")
}

// attach appends an action receipt. An action that already happened reports persistence failures as warnings.
func (s *Service) attach(runID string, rec *actionRec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.store.path(runID, "actions.jsonl")
	if err == nil {
		var f *os.File
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			err = errors.Join(json.NewEncoder(f).Encode(*rec), f.Close())
		}
	}
	if err != nil {
		rec.Warnings = append(rec.Warnings, "actions.jsonl: "+err.Error())
	}
}

// nextSeq numbers each leg's receipts from 1 and resumes after a daemon restart. Callers hold s.mu.
func (s *Service) nextSeq(runID, leg string) (int, error) {
	key := runID + "/" + leg
	n, ok := s.seqs[key]
	if !ok {
		receipts, err := s.store.Receipts(runID)
		if err != nil {
			return 0, err
		}
		for _, r := range receipts {
			if r.onLeg() == leg {
				n = max(n, r.Seq)
			}
		}
	}
	s.seqs[key] = n + 1
	return n + 1, nil
}

// engineFor returns the leg's engine, rebuilding it from run.json after a daemon restart.
func (s *Service) engineFor(runID string, leg Leg) *Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := runID + "/" + leg.Name
	if s.engines[key] == nil {
		s.engines[key] = newEngine(*leg.Engine, s.engExec)
	}
	return s.engines[key]
}

// attachEngine gives a freshly launched leg its engine. Failing here undoes the launch.
func (s *Service) attachEngine(ctx context.Context, runID string, leg Leg) (Leg, error) {
	e, err := NewEngine(s.binary, filepath.Join(s.store.root, runID), leg.Name, leg.DebugPort, s.engExec)
	if err != nil {
		_ = cleanLeg(ctx, s.sys, s.store.legDir(runID, leg.Name), false)
		leg.State = "failed"
		return leg, err
	}
	leg.Engine = &e.EngineInfo
	s.mu.Lock()
	s.engines[runID+"/"+leg.Name] = e
	s.mu.Unlock()
	return leg, nil
}

// closeEngines closes every engine of a run, whether or not this daemon created it.
func (s *Service) closeEngines(runID string) {
	run, err := s.store.Get(runID)
	if err != nil {
		return
	}
	for _, l := range run.Legs {
		if l.Engine != nil {
			s.engineFor(runID, l).Close(s.sys)
		}
	}
	s.mu.Lock()
	for k := range s.engines {
		if strings.HasPrefix(k, runID+"/") {
			delete(s.engines, k)
		}
	}
	s.mu.Unlock()
}
