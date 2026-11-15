package computeruse

import (
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
)

var observedTools = map[string]bool{
	"click": true, "double_click": true, "right_click": true, "set_value": true,
	"type_text": true, "press_key": true, "hotkey": true, "scroll": true, "drag": true,
	"bring_to_front": true,
}

const keepSnapshots = 512

type window struct {
	pid, windowID any
}

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

func (s *Service) observeAfter(ctx context.Context, binary string, saved *savedImages, arguments map[string]any, result any) any {
	output, ok := result.(map[string]any)
	if !ok {
		return result
	}
	session := arguments["session"]
	target, found := s.actionWindow(session, arguments)
	if !found {
		output["observation"] = map[string]any{"ok": false, "skipped": "Orchestrator could not tell which window this action ran in. Read it with get_window_state to see the result, and pass pid and window_id next time."}
		return output
	}
	read := map[string]any{"pid": target.pid, "window_id": target.windowID, "since": "latest"}
	if session != nil {
		read["session"] = session
	}
	screenshot := saved.nextPath(".png")
	read["screenshot_out_file"] = screenshot
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

func (s *Service) prepareObserve(saved *savedImages, arguments map[string]any) string {
	observe := map[string]any{}
	given, _ := arguments["observe"].(map[string]any)
	for key, value := range given {
		observe[key] = value
	}
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

const renderWarning = "This window may not have rendered: its tree or screenshot is nearly empty. Apps stop painting windows hidden behind others, Chromium-based browsers and apps especially. Do not retry; ask the user to bring the window to the front and leave it visible, then read it again."

const minRenderedElements = 3

const blankShare = 0.98

func flagUnrendered(tool string, arguments map[string]any, result any) {
	tree := treeRequested(tool, arguments)
	state, _ := result.(map[string]any)
	flagUnrenderedRead(state, tree)
	observation, _ := state["observation"].(map[string]any)
	observed, _ := observation["state"].(map[string]any)
	flagUnrenderedRead(observed, tree)
}

func flagUnrenderedRead(state map[string]any, tree bool) {
	if state == nil || state["snapshot_id"] == nil && state["screenshot_file_path"] == nil {
		return
	}
	elements, counted := state["element_count"].(float64)
	sparse := tree && counted && elements < minRenderedElements && state["truncated"] != true
	path, _ := state["screenshot_file_path"].(string)
	if sparse || (path != "" && blankScreenshot(path)) {
		state["render_warning"] = renderWarning
	}
}

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
			key := r>>12<<8 | g>>12<<4 | b>>12
			counts[key]++
			most = max(most, counts[key])
		}
	}
	return float64(most) >= blankShare*grid*grid
}
