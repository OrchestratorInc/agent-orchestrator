package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
)

// OwnApp is the AO desktop app that runs drags in its own windows.
type OwnApp interface {
	PID() int
	Drag(ctx context.Context, sessionID string, drag map[string]any) (any, error)
}

// SetOwnApp sets the desktop app that runs drags in AO's own windows.
func (s *Service) SetOwnApp(app OwnApp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownApp = app
}

type frame struct{ xScale, yScale float64 }

func frameKey(session any, pid, windowID int) string {
	label, _ := session.(string)
	return fmt.Sprintf("%s\x00%d\x00%d", label, pid, windowID)
}

func (s *Service) rememberFrame(session any, state map[string]any) {
	pid, okPID := number(state["pid"])
	windowID, okWindow := number(state["window_id"])
	width, _ := number(state["screenshot_width"])
	height, _ := number(state["screenshot_height"])
	bounds, _ := state["window_bounds"].(map[string]any)
	boundsWidth, _ := number(bounds["width"])
	boundsHeight, _ := number(bounds["height"])
	if !okPID || !okWindow || width <= 0 || height <= 0 || boundsWidth <= 0 || boundsHeight <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) >= keepSnapshots {
		clear(s.frames)
	}
	s.frames[frameKey(session, int(pid), int(windowID))] = frame{xScale: boundsWidth / width, yScale: boundsHeight / height}
}

type ownWindowDrag struct {
	pid, windowID int
	drag          map[string]any
}

func (s *Service) ownWindowDrags(arguments map[string]any) ([]ownWindowDrag, OwnApp, error) {
	s.mu.Lock()
	app := s.ownApp
	s.mu.Unlock()
	if app == nil {
		return nil, nil, nil
	}
	own := app.PID()
	if own <= 0 {
		return nil, nil, nil
	}
	steps, _ := arguments["steps"].([]any)
	var drags []ownWindowDrag
	routed := false
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		name, stepArguments := stepAction(step)
		tool, _ := batchTool(name, stepArguments)
		pid, windowID := windowOf(stepArguments, arguments)
		if tool == "drag" && pid == own {
			routed = true
			if windowID != 0 && !hasAny(step, "wait_for", "expect") {
				drags = append(drags, ownWindowDrag{pid: pid, windowID: windowID, drag: stepArguments})
			}
		}
	}
	if !routed {
		return nil, nil, nil
	}
	if len(drags) != len(steps) {
		return nil, nil, actionRefused("COMPUTER_USE_OWN_WINDOW_BATCH", "run_steps", 0,
			"A drag in an Orchestrator window runs from the Orchestrator desktop app, not Cua Driver. Send it in a run_steps batch of only drag steps that each name the Orchestrator pid and window_id, with no wait_for or expect; send other actions in another batch.")
	}
	return drags, app, nil
}

func (s *Service) runOwnWindowDrags(ctx context.Context, sessionID string, session any, drags []ownWindowDrag, app OwnApp) (map[string]any, error) {
	requests := make([]map[string]any, 0, len(drags))
	for index, step := range drags {
		if step.drag["from_zoom"] == true {
			return nil, actionRefused("COMPUTER_USE_OWN_WINDOW_ZOOM", "drag", index+1,
				"A drag in an Orchestrator window takes coordinates from a get_window_state screenshot, not a zoom image. Measure them on the window's screenshot.")
		}
		s.mu.Lock()
		scale, known := s.frames[frameKey(session, step.pid, step.windowID)]
		s.mu.Unlock()
		if !known {
			return nil, actionRefused("COMPUTER_USE_READ_FIRST", "drag", index+1,
				"Read the Orchestrator window with get_window_state first, then measure the drag on that screenshot.")
		}
		drag := map[string]any{"windowId": step.windowID}
		for key, axis := range map[string]struct {
			name  string
			scale float64
		}{"from_x": {"fromX", scale.xScale}, "to_x": {"toX", scale.xScale}, "from_y": {"fromY", scale.yScale}, "to_y": {"toY", scale.yScale}} {
			value, ok := number(step.drag[key])
			if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, actionRefused("COMPUTER_USE_DRAG_COORDINATES", "drag", index+1,
					"A drag needs numbers for from_x, from_y, to_x and to_y, measured on the window's get_window_state screenshot.")
			}
			drag[axis.name] = value * axis.scale
		}
		for key, name := range map[string]string{"duration_ms": "durationMs", "steps": "steps", "button": "button", "modifier": "modifiers"} {
			if has(step.drag, key) {
				drag[name] = step.drag[key]
			}
		}
		requests = append(requests, drag)
	}
	reports := make([]any, 0, len(requests))
	ok := true
	for index, drag := range requests {
		value, err := app.Drag(ctx, sessionID, drag)
		report := map[string]any{"step": index + 1, "tool": "drag", "ok": err == nil}
		if err != nil {
			report["error"] = err.Error()
			ok = false
			reports = append(reports, report)
			break
		}
		report["result"] = value
		reports = append(reports, report)
	}
	return map[string]any{
		"ok":    ok,
		"route": "ao_desktop",
		"steps": reports,
		"note":  "The Orchestrator desktop app ran these drags in its own window: the pointer did not move and focus did not change. The observation shows the window afterwards; check it for the effect you wanted.",
	}, nil
}

func windowOf(arguments, batch map[string]any) (int, int) {
	source := batch
	if targetKind(arguments) == "window" {
		source, _ = arguments["target"].(map[string]any)
	} else if has(arguments, "pid") || has(arguments, "window_id") {
		source = arguments
	}
	pid, _ := number(source["pid"])
	windowID, _ := number(source["window_id"])
	return int(pid), int(windowID)
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	}
	return 0, false
}
