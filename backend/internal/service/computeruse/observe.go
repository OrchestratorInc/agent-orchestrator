package computeruse

import (
	"context"
	"encoding/json"
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
	stdout, stderr, err := s.callWithDaemon(ctx, binary, "get_window_state", read)
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
	}
	output["observation"] = map[string]any{"tool": "get_window_state", "ok": true, "state": state}
	return output
}

// prepareObserve has run_steps read the window after its last step with a
// screenshot, saved to a file of AO's choosing. A caller's observe is kept,
// with the screenshot turned on unless it opted out. When no step names a
// window the driver can find, the window of the batch's last token is filled
// in; a batch AO cannot place gets no observe, which the driver would refuse.
// It returns the screenshot path to adopt, or "".
func (s *Service) prepareObserve(saved *savedImages, arguments map[string]any) string {
	observe := map[string]any{}
	given, _ := arguments["observe"].(map[string]any)
	for key, value := range given {
		observe[key] = value
	}
	if !has(observe, "pid") || !has(observe, "window_id") {
		if !s.batchNamesWindow(arguments) {
			target, found := s.lastTokenWindow(arguments)
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
// that names its app or window by title.
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
		if targetKind(stepArguments) == "window" {
			return true
		}
	}
	return false
}

func (s *Service) lastTokenWindow(arguments map[string]any) (window, bool) {
	steps, _ := arguments["steps"].([]any)
	for index := len(steps) - 1; index >= 0; index-- {
		step, _ := steps[index].(map[string]any)
		_, stepArguments := stepAction(step)
		if target, found := s.tokenWindow(arguments["session"], stepArguments); found {
			return target, true
		}
	}
	return window{}, false
}
