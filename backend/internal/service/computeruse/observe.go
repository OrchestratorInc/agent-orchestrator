package computeruse

import (
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg" // screenshots the driver saves as JPEG
	_ "image/png"  // and as PNG
	"os"
	"strings"
)

// Every action comes back with a look at the window it acted on: the tree
// rows it changed and a screenshot file. A click or key press often reports
// its effect as "unverifiable", and an agent that does not look before the
// next step types into the wrong field or chat.
//
// A direct action is run unchanged, then the window is read with
// since:"latest", the driver's diff against the last read. run_steps gets the
// same read through its own `observe`. The read invalidates earlier element
// tokens; its tree_diff names the snapshot to use next.

// observedTools are the direct actions followed by a read. move_cursor is
// left out: in window scope it moves only the agent cursor overlay.
var observedTools = map[string]bool{
	"click": true, "double_click": true, "right_click": true, "set_value": true,
	"type_text": true, "press_key": true, "hotkey": true, "scroll": true, "drag": true,
	// A window brought to the front draws again; the read shows it.
	"bring_to_front": true,
}

// keepSnapshots bounds how many snapshot windows the service remembers.
const keepSnapshots = 512

// window is the pid and window_id an action ran in, as the caller or driver
// wrote them.
type window struct {
	pid, windowID any
}

// snapshotWindows remembers the window of each snapshot a read returned, per
// driver session, so a token-only action can be followed by a read of its
// window: a token names a snapshot, and neither the action's result nor
// run_steps' observe names the window.
type snapshotWindows struct {
	windows map[string]window
	order   []string
}

func snapshotKey(session any, snapshotID string) string {
	label, _ := session.(string)
	return label + "\x00" + snapshotID
}

func (s *Service) rememberSnapshot(session any, state map[string]any) {
	id, _ := state["snapshot_id"].(string)
	if id == "" || !has(state, "pid") || !has(state, "window_id") {
		return
	}
	key := snapshotKey(session, id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, known := s.snapshots.windows[key]; !known {
		s.snapshots.order = append(s.snapshots.order, key)
	}
	s.snapshots.windows[key] = window{pid: state["pid"], windowID: state["window_id"]}
	for len(s.snapshots.order) > keepSnapshots {
		delete(s.snapshots.windows, s.snapshots.order[0])
		s.snapshots.order = s.snapshots.order[1:]
	}
}

// rememberReads records the snapshots in a call's result: a get_window_state
// result, or run_steps' observation.
func (s *Service) rememberReads(session, result any) {
	state, _ := result.(map[string]any)
	s.rememberSnapshot(session, state)
	observation, _ := state["observation"].(map[string]any)
	observed, _ := observation["state"].(map[string]any)
	s.rememberSnapshot(session, observed)
}

func (s *Service) tokenWindow(session any, arguments map[string]any) (window, bool) {
	token, _ := arguments["element_token"].(string)
	snapshotID, _, found := strings.Cut(token, ":")
	if !found {
		return window{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known, ok := s.snapshots.windows[snapshotKey(session, snapshotID)]
	return known, ok
}

// actionWindow is the window a direct action ran in.
func (s *Service) actionWindow(session any, arguments map[string]any) (window, bool) {
	if targetKind(arguments) == "window" {
		target, _ := arguments["target"].(map[string]any)
		if has(target, "pid") && has(target, "window_id") {
			return window{pid: target["pid"], windowID: target["window_id"]}, true
		}
	}
	if has(arguments, "pid") && has(arguments, "window_id") {
		return window{pid: arguments["pid"], windowID: arguments["window_id"]}, true
	}
	return s.tokenWindow(session, arguments)
}

// observeAfter reads the window a direct action ran in and adds the read to
// the action's result as `observation`, shaped like run_steps'. The action
// already happened, so a failed read is reported there, not returned.
func (s *Service) observeAfter(ctx context.Context, binary string, saved *savedImages, arguments map[string]any, result any) any {
	output, ok := result.(map[string]any)
	if !ok {
		return result
	}
	session := arguments["session"]
	target, found := s.actionWindow(session, arguments)
	if !found {
		output["observation"] = map[string]any{"ok": false, "skipped": "AO could not tell which window this action ran in. Read it with get_window_state to see the result, and pass pid and window_id next time."}
		return output
	}
	read := map[string]any{"pid": target.pid, "window_id": target.windowID, "since": "latest"}
	if session != nil {
		read["session"] = session
	}
	screenshot := saved.nextPath(".png")
	read["screenshot_out_file"] = screenshot
	// The session can end between the action and this read; revive it like Call.
	stdout, stderr, err := s.callInSession(ctx, binary, "get_window_state", read)
	if err != nil {
		output["observation"] = map[string]any{"tool": "get_window_state", "ok": false, "error": commandFailure(err, stdout, stderr)}
		return output
	}
	var state any
	if json.Unmarshal(stdout, &state) != nil {
		state = map[string]any{"text": cleanText(string(stdout))}
	}
	state = saved.externalize(state)
	saved.adopt(screenshot)
	if fields, ok := state.(map[string]any); ok {
		s.rememberSnapshot(session, fields)
		flagUnrenderedRead(fields, true)
	}
	output["observation"] = map[string]any{"tool": "get_window_state", "ok": true, "state": state}
	return output
}

// prepareObserve has run_steps read the window after its last step with a
// screenshot, saved to a file of AO's choosing. A caller's observe is kept,
// with the screenshot turned on unless it opted out. When no step names a
// window the driver can find, the window of the batch's last step that AO can
// place (a nested window target or an element token) is filled in; a batch AO
// cannot place gets no observe, which the driver would refuse.
// It returns the screenshot path to adopt, or "".
func (s *Service) prepareObserve(saved *savedImages, arguments map[string]any) string {
	observe := map[string]any{}
	given, _ := arguments["observe"].(map[string]any)
	for key, value := range given {
		observe[key] = value
	}
	// AO alone picks where a screenshot goes, and a caller's file would also
	// force a capture the caller turned off.
	delete(observe, "screenshot_out_file")
	if given != nil {
		arguments["observe"] = observe
	}
	if !has(observe, "pid") || !has(observe, "window_id") {
		if !s.batchNamesWindow(arguments) {
			target, found := s.lastStepWindow(arguments)
			if !found {
				return ""
			}
			observe["pid"], observe["window_id"] = target.pid, target.windowID
		}
	}
	screenshot := ""
	if include, set := observe["include_screenshot"].(bool); !set || include {
		screenshot = saved.nextPath(".png")
		observe["include_screenshot"] = true
		observe["screenshot_out_file"] = screenshot
	}
	arguments["observe"] = observe
	return screenshot
}

// batchNamesWindow reports whether run_steps can work out its observe window
// itself: from its own pid and window_id, or a step naming both, or a step
// that names its app or window by title. The driver does not look inside a
// step's target:{kind:"window"}, so that form does not count.
func (s *Service) batchNamesWindow(arguments map[string]any) bool {
	if (has(arguments, "pid") && has(arguments, "window_id")) || hasAny(arguments, "app", "window") {
		return true
	}
	steps, _ := arguments["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		_, stepArguments := stepAction(step)
		if (has(stepArguments, "pid") && has(stepArguments, "window_id")) || hasAny(stepArguments, "app", "window") {
			return true
		}
	}
	return false
}

// lastStepWindow is the window of the batch's last step that names one AO
// can place: a nested window target, or an element token from a known read.
func (s *Service) lastStepWindow(arguments map[string]any) (window, bool) {
	steps, _ := arguments["steps"].([]any)
	for index := len(steps) - 1; index >= 0; index-- {
		step, _ := steps[index].(map[string]any)
		_, stepArguments := stepAction(step)
		if target, found := s.actionWindow(arguments["session"], stepArguments); found {
			return target, true
		}
	}
	return window{}, false
}

// renderWarning is added to a window read that looks unpainted.
const renderWarning = "This window may not have rendered: its tree or screenshot is nearly empty. Apps stop painting windows hidden behind others, Chromium-based browsers and apps especially. Do not retry; ask the user to bring the window to the front and leave it visible, then read it again."

// minRenderedElements is the smallest tree a painted window has: a window
// frame and at most one other element is the tree of an unpainted one.
const minRenderedElements = 3

// blankShare is the share of sampled screenshot pixels in one color above
// which the screenshot counts as blank.
const blankShare = 0.98

// flagUnrendered adds render_warning to the window reads in a call's result:
// a get_window_state result, or run_steps' observation. The driver reports
// such a read as ok, and an agent otherwise retries it, a screenshot each time.
func flagUnrendered(tool string, arguments map[string]any, result any) {
	tree := treeRequested(tool, arguments)
	state, _ := result.(map[string]any)
	flagUnrenderedRead(state, tree)
	observation, _ := state["observation"].(map[string]any)
	observed, _ := observation["state"].(map[string]any)
	flagUnrenderedRead(observed, tree)
}

// flagUnrenderedRead flags one window read. tree is whether the read walked
// the accessibility tree; a screenshot-only read's element_count is 0 by
// design, so only its screenshot is checked.
func flagUnrenderedRead(state map[string]any, tree bool) {
	if state == nil || state["snapshot_id"] == nil && state["screenshot_file_path"] == nil {
		return
	}
	// A read cut short by max_elements is small because it was asked to be;
	// only its screenshot can say whether the window painted.
	elements, counted := state["element_count"].(float64)
	sparse := tree && counted && elements < minRenderedElements && state["truncated"] != true
	path, _ := state["screenshot_file_path"].(string)
	if sparse || (path != "" && blankScreenshot(path)) {
		state["render_warning"] = renderWarning
	}
}

// treeRequested reports whether a call's window read walks the accessibility
// tree: include_accessibility_tree:false reads only the screenshot. run_steps'
// end read takes the field from observe, and otherwise from a get_window_state
// step, which the driver merges into observe as the end read.
func treeRequested(tool string, arguments map[string]any) bool {
	if tool != "run_steps" {
		return arguments["include_accessibility_tree"] != false
	}
	observe, _ := arguments["observe"].(map[string]any)
	if value, set := observe["include_accessibility_tree"]; set {
		return value != false
	}
	steps, _ := arguments["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		name, stepArguments := stepAction(step)
		if name == "get_window_state" && stepArguments["include_accessibility_tree"] == false {
			return false
		}
	}
	return true
}

// blankScreenshot reports whether nearly every sampled pixel of a screenshot
// has one color. An unreadable file is not called blank.
func blankScreenshot(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	picture, _, err := image.Decode(file)
	if err != nil {
		return false
	}
	bounds := picture.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return true
	}
	const grid = 48
	counts := map[uint32]int{}
	most := 0
	for row := 0; row < grid; row++ {
		for column := 0; column < grid; column++ {
			x := bounds.Min.X + column*bounds.Dx()/grid
			y := bounds.Min.Y + row*bounds.Dy()/grid
			r, g, b, _ := picture.At(x, y).RGBA()
			// 4 bits a channel: compression noise and anti-aliasing stay one color.
			key := r>>12<<8 | g>>12<<4 | b>>12
			counts[key]++
			most = max(most, counts[key])
		}
	}
	return float64(most) >= blankShare*grid*grid
}
