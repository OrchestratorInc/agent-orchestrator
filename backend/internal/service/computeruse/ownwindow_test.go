package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeOwnApp struct {
	pid   int
	drags []map[string]any
}

func (f *fakeOwnApp) PID() int { return f.pid }

func (f *fakeOwnApp) Drag(_ context.Context, _ string, drag map[string]any) (any, error) {
	f.drags = append(f.drags, drag)
	return map[string]any{"ok": true, "route": "send_input_event"}, nil
}

func ownWindowService(t *testing.T) (*Service, *fakeOwnApp, *[]string) {
	t.Helper()
	tools := &[]string{}
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		*tools = append(*tools, args[1])
		if args[1] != "get_window_state" {
			return []byte(`{"ok":true}`), nil, nil
		}
		var arguments map[string]any
		_ = json.Unmarshal([]byte(args[2]), &arguments)
		if path, ok := arguments["screenshot_out_file"].(string); ok {
			_ = os.WriteFile(path, pngHeader, 0o600)
		}
		return []byte(`{"snapshot_id":"s1","pid":7,"window_id":9,"screenshot_width":1000,"screenshot_height":500,"window_bounds":{"x":0,"y":0,"width":2000,"height":1000}}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	app := &fakeOwnApp{pid: 7}
	s.SetOwnApp(app)
	return s, app, tools
}

func drag(pid, windowID int) map[string]any {
	return map[string]any{"drag": map[string]any{"pid": pid, "window_id": windowID, "from_x": 10, "from_y": 20, "to_x": 30, "to_y": 40, "duration_ms": 300}}
}

func TestDragInAOWindowRunsThroughTheDesktop(t *testing.T) {
	s, app, tools := ownWindowService(t)
	ctx := context.Background()
	if _, err := s.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "w"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{drag(7, 9)}}, SessionID: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if len(app.drags) != 1 {
		t.Fatalf("desktop drags = %v", app.drags)
	}
	got := app.drags[0]
	if got["windowId"] != 9 || got["fromX"] != 20.0 || got["fromY"] != 40.0 || got["toX"] != 60.0 || got["toY"] != 80.0 || got["durationMs"] != 300 {
		t.Fatalf("desktop drag = %v", got)
	}
	for _, tool := range *tools {
		if tool == "run_steps" || tool == "drag" {
			t.Fatalf("driver ran %s for AO's own window: %v", tool, *tools)
		}
	}
	if output := result.Result.(map[string]any); output["route"] != "ao_desktop" || output["ok"] != true {
		t.Fatalf("result = %v", output)
	}
	if observed := observation(t, result); observed["ok"] != true {
		t.Fatalf("observation = %v", observed)
	}
}

func TestDragInAOWindowRefusesWhatItCannotRun(t *testing.T) {
	s, app, tools := ownWindowService(t)
	ctx := context.Background()
	var apiErr *apierr.Error
	for name, first := range map[string]any{
		"click": map[string]any{"click": map[string]any{"pid": 7, "window_id": 9, "x": 1, "y": 1}},
		"read":  map[string]any{"get_window_state": map[string]any{"pid": 7, "window_id": 9}},
	} {
		_, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{first, drag(7, 9)}}, SessionID: "w"})
		if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_OWN_WINDOW_BATCH" {
			t.Fatalf("%s+drag batch error = %v", name, err)
		}
	}
	_, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{drag(7, 9)}}, SessionID: "w"})
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_READ_FIRST" {
		t.Fatalf("unread window error = %v", err)
	}
	if len(app.drags) != 0 || len(*tools) != 0 {
		t.Fatalf("refused batches ran: desktop %v, driver %v", app.drags, *tools)
	}
}

func TestDragInAOWindowRefusesBadCoordinatesBeforeAnyGesture(t *testing.T) {
	s, app, _ := ownWindowService(t)
	ctx := context.Background()
	if _, err := s.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "w"}); err != nil {
		t.Fatal(err)
	}
	var apiErr *apierr.Error
	for name, toX := range map[string]any{"missing": nil, "null": nil, "text": "30"} {
		bad := drag(7, 9)
		arguments := bad["drag"].(map[string]any)
		delete(arguments, "to_x")
		if name != "missing" {
			arguments["to_x"] = toX
		}
		_, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{drag(7, 9), bad}}, SessionID: "w"})
		if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_DRAG_COORDINATES" {
			t.Fatalf("%s to_x error = %v", name, err)
		}
	}
	if len(app.drags) != 0 {
		t.Fatalf("a batch with a bad drag still moved the pointer: %v", app.drags)
	}
}

func TestDragInOtherAppsStaysWithTheDriver(t *testing.T) {
	s, app, tools := ownWindowService(t)
	if _, err := s.Call(context.Background(), CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{drag(8, 9)}}, SessionID: "w"}); err != nil {
		t.Fatal(err)
	}
	if len(app.drags) != 0 || len(*tools) == 0 || (*tools)[0] != "run_steps" {
		t.Fatalf("desktop drags %v, driver tools %v", app.drags, *tools)
	}
}
