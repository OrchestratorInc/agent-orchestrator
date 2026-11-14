package computeruse

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writeScreenshot saves a PNG: one color when blank, otherwise a pattern of
// distinct blocks like a painted window.
func writeScreenshot(t *testing.T, blank bool) string {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 230, 408))
	for y := 0; y < 408; y++ {
		for x := 0; x < 230; x++ {
			shade := color.RGBA{R: 40, G: 40, B: 40, A: 255}
			if !blank && (x/23+y/34)%2 == 0 {
				shade = color.RGBA{R: 220, G: 220, B: 220, A: 255}
			}
			picture.Set(x, y, shade)
		}
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, picture); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFlagUnrenderedReads(t *testing.T) {
	blank, painted := writeScreenshot(t, true), writeScreenshot(t, false)
	for name, tc := range map[string]struct {
		state map[string]any
		tree  bool
		want  bool
	}{
		"frame only":          {map[string]any{"snapshot_id": "s1", "element_count": float64(1), "screenshot_file_path": painted}, true, true},
		"blank screenshot":    {map[string]any{"snapshot_id": "s1", "element_count": float64(40), "screenshot_file_path": blank}, true, true},
		"painted window":      {map[string]any{"snapshot_id": "s1", "element_count": float64(33), "screenshot_file_path": painted}, true, false},
		"tree-only read":      {map[string]any{"snapshot_id": "s1", "element_count": float64(33)}, true, false},
		"screenshot-only":     {map[string]any{"screenshot_file_path": painted}, true, false},
		"unreadable image":    {map[string]any{"snapshot_id": "s1", "element_count": float64(33), "screenshot_file_path": "/no/such.png"}, true, false},
		"not a window read":   {map[string]any{"summary": "clicked"}, true, false},
		"blank, no tree read": {map[string]any{"screenshot_file_path": blank}, true, true},
		// Cua Driver 0.37.0 on Windows, max_elements 5 on a painted Notepad.
		"truncated, painted": {map[string]any{"snapshot_id": "s1", "element_count": float64(1), "total_element_count": float64(1), "truncated": true, "truncation_reason": "node_budget", "screenshot_file_path": painted}, true, false},
		"truncated, blank":   {map[string]any{"snapshot_id": "s1", "element_count": float64(1), "truncated": true, "screenshot_file_path": blank}, true, true},
		// Cua Driver 0.37.0 on macOS, include_accessibility_tree:false on a
		// painted Brave window: a snapshot_id, and an element_count of 0.
		"tree skipped, painted": {map[string]any{"snapshot_id": "s1", "element_count": float64(0), "total_element_count": float64(0), "elements_complete": false, "screenshot_file_path": painted}, false, false},
		"tree skipped, blank":   {map[string]any{"snapshot_id": "s1", "element_count": float64(0), "screenshot_file_path": blank}, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			flagUnrenderedRead(tc.state, tc.tree)
			if _, got := tc.state["render_warning"]; got != tc.want {
				t.Fatalf("render_warning present = %v, want %v (%v)", got, tc.want, tc.state)
			}
		})
	}

	batch := map[string]any{"steps": []any{}, "observation": map[string]any{"ok": true, "state": map[string]any{"snapshot_id": "s2", "element_count": float64(0)}}}
	flagUnrendered("run_steps", map[string]any{"steps": []any{}}, batch)
	if batch["observation"].(map[string]any)["state"].(map[string]any)["render_warning"] == nil {
		t.Fatalf("run_steps observation not flagged: %v", batch)
	}
}

func TestTreeRequested(t *testing.T) {
	skip := map[string]any{"include_accessibility_tree": false}
	click := map[string]any{"click": map[string]any{"element_token": "s1:2"}}
	for name, tc := range map[string]struct {
		tool      string
		arguments map[string]any
		want      bool
	}{
		"window read":            {"get_window_state", map[string]any{"pid": 7, "window_id": 9}, true},
		"window read, tree off":  {"get_window_state", map[string]any{"pid": 7, "window_id": 9, "include_accessibility_tree": false}, false},
		"window read, tree on":   {"get_window_state", map[string]any{"include_accessibility_tree": true}, true},
		"batch":                  {"run_steps", map[string]any{"steps": []any{click}}, true},
		"batch observe, off":     {"run_steps", map[string]any{"steps": []any{click}, "observe": skip}, false},
		"trailing read, off":     {"run_steps", map[string]any{"steps": []any{click, map[string]any{"get_window_state": skip}}}, false},
		"trailing read, written": {"run_steps", map[string]any{"steps": []any{click, map[string]any{"tool": "get_window_state", "args": skip}}}, false},
		// The driver lets an explicit observe win over the step it merges in.
		"observe wins over step": {"run_steps", map[string]any{"steps": []any{click, map[string]any{"get_window_state": skip}}, "observe": map[string]any{"include_accessibility_tree": true}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := treeRequested(tc.tool, tc.arguments); got != tc.want {
				t.Fatalf("treeRequested(%s, %v) = %v, want %v", tc.tool, tc.arguments, got, tc.want)
			}
		})
	}
}

// A screenshot-only read of a painted window has an element_count of 0 by
// design; only a blank screenshot gets the warning.
func TestScreenshotOnlyReadWarnsOnlyWhenBlank(t *testing.T) {
	for name, blank := range map[string]bool{"painted": false, "blank": true} {
		t.Run(name, func(t *testing.T) {
			shot := writeScreenshot(t, blank)
			driver := &fakeDriver{daemonUp: true}
			driver.onCall = func(args []string) ([]byte, []byte, error) {
				var arguments map[string]any
				_ = json.Unmarshal([]byte(args[2]), &arguments)
				out := arguments["screenshot_out_file"].(string)
				data, _ := os.ReadFile(shot)
				_ = os.WriteFile(out, data, 0o600)
				encoded, _ := json.Marshal(map[string]any{"snapshot_id": "s1", "pid": 7, "window_id": 9, "element_count": 0, "total_element_count": 0, "screenshot_file_path": out})
				return encoded, nil, nil
			}
			s := newTestService(t, driver, "darwin")
			result, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9, "include_accessibility_tree": false}})
			if err != nil {
				t.Fatalf("Call() error = %v", err)
			}
			state := result.Result.(map[string]any)
			if _, warned := state["render_warning"]; warned != blank {
				t.Fatalf("render_warning present = %v, want %v (%v)", warned, blank, state)
			}
		})
	}
}

// The driver reports an unpainted window's read as ok; the read after an
// action carries the warning so the agent stops retrying.
func TestActionReadOfAnUnpaintedWindowWarns(t *testing.T) {
	blank := writeScreenshot(t, true)
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if args[1] != "get_window_state" {
			return []byte(`{"summary":"clicked"}`), nil, nil
		}
		var arguments map[string]any
		_ = json.Unmarshal([]byte(args[2]), &arguments)
		// Like the driver, write the requested file and name it in the result.
		out := arguments["screenshot_out_file"].(string)
		data, _ := os.ReadFile(blank)
		_ = os.WriteFile(out, data, 0o600)
		encoded, _ := json.Marshal(map[string]any{"snapshot_id": "s1", "pid": 7, "window_id": 9, "element_count": 12, "screenshot_file_path": out})
		return encoded, nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "press_key", Arguments: map[string]any{"pid": 7, "window_id": 9, "key": "f5"}})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	state := result.Result.(map[string]any)["observation"].(map[string]any)["state"].(map[string]any)
	if state["render_warning"] != renderWarning {
		t.Fatalf("observation state = %v, want a render warning", state)
	}
}
