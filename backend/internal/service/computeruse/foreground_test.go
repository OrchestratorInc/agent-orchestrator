package computeruse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Foreground delivery raises the window and sends real input to whatever has
// focus; it is refused on direct actions, run_steps steps, and the batch.
func TestCallRefusesForegroundDelivery(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	s := newTestService(t, driver, "darwin")
	step := func(args map[string]any) map[string]any {
		return map[string]any{"steps": []any{map[string]any{"press_key": args}}}
	}
	for name, tc := range map[string]struct {
		tool      string
		arguments map[string]any
	}{
		"direct action":       {"press_key", map[string]any{"pid": 7, "window_id": 9, "key": "end", "delivery_mode": "foreground"}},
		"run_steps step":      {"run_steps", step(map[string]any{"pid": 7, "window_id": 9, "key": "end", "delivery_mode": "foreground"})},
		"run_steps batch":     {"run_steps", map[string]any{"pid": 7, "window_id": 9, "delivery_mode": "foreground", "steps": []any{map[string]any{"press_key": map[string]any{"key": "end"}}}}},
		"foreground fallback": {"run_steps", map[string]any{"pid": 7, "window_id": 9, "foreground_fallback": true, "steps": []any{map[string]any{"press_key": map[string]any{"key": "end"}}}}},
		// The driver coerces a boolean string to the boolean before running.
		"foreground fallback string": {"run_steps", map[string]any{"pid": 7, "window_id": 9, "foreground_fallback": " TRUE ", "steps": []any{map[string]any{"press_key": map[string]any{"key": "end"}}}}},
		// The driver reads delivery_mode in any letter case, and the older
		// dispatch field as delivery_mode.
		"direct any case":          {"press_key", map[string]any{"pid": 7, "window_id": 9, "key": "escape", "delivery_mode": "Foreground"}},
		"direct dispatch":          {"press_key", map[string]any{"pid": 7, "window_id": 9, "key": "escape", "dispatch": "foreground"}},
		"run_steps step case":      {"run_steps", step(map[string]any{"pid": 7, "window_id": 9, "key": "end", "delivery_mode": "FOREGROUND"})},
		"run_steps step dispatch":  {"run_steps", step(map[string]any{"pid": 7, "window_id": 9, "key": "end", "dispatch": "foreground"})},
		"run_steps batch dispatch": {"run_steps", map[string]any{"pid": 7, "window_id": 9, "dispatch": "Foreground", "steps": []any{map[string]any{"press_key": map[string]any{"key": "end"}}}}},
		"run_steps alias step":     {"run_steps", map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"tool": "key", "args": map[string]any{"key": "end", "delivery_mode": "foreground"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.Call(context.Background(), CallRequest{Tool: tc.tool, Arguments: tc.arguments})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_FOREGROUND_DISABLED" || !strings.Contains(apiErr.Message, "bring_to_front") {
				t.Fatalf("err = %v, want COMPUTER_USE_FOREGROUND_DISABLED", err)
			}
		})
	}
	if len(driver.calls) != 0 {
		t.Fatalf("driver ran %v for a foreground action", driver.calls)
	}
	if _, err := s.Call(context.Background(), CallRequest{Tool: "press_key", Arguments: map[string]any{"pid": 7, "window_id": 9, "key": "end", "delivery_mode": "background"}}); err != nil {
		t.Fatalf("background delivery: %v", err)
	}
	if _, err := s.Call(context.Background(), CallRequest{Tool: "run_steps", Arguments: map[string]any{"pid": 7, "window_id": 9, "foreground_fallback": false, "steps": []any{map[string]any{"press_key": map[string]any{"key": "end"}}}}}); err != nil {
		t.Fatalf("fallback turned off: %v", err)
	}
}

// The driver's own advice to retry in the foreground is answered with AO's,
// in results and in errors; an app's own labels in the tree are not advice.
func TestDriverForegroundAdviceIsReplaced(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	answer := `{"summary":"Sent end","hint":"If it didn't land, retry with delivery_mode:\"foreground\"."}`
	fail := false
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if args[1] == "get_window_state" {
			return []byte(`{"snapshot_id":"s1","pid":7,"window_id":9,"element_count":20,"tree_markdown":"- [3] AXButton (Foreground color)"}`), nil, nil
		}
		if fail {
			return nil, []byte("background input was ignored; retry with delivery_mode foreground"), errors.New("exit status 1")
		}
		return []byte(answer), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "press_key", Arguments: map[string]any{"pid": 7, "window_id": 9, "key": "end"}})
	if err != nil {
		t.Fatal(err)
	}
	if note := result.Result.(map[string]any)["ao_note"]; note != foregroundAdvice {
		t.Fatalf("ao_note = %v, want AO's advice", note)
	}

	read, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}})
	if err != nil {
		t.Fatal(err)
	}
	if note, ok := read.Result.(map[string]any)["ao_note"]; ok {
		t.Fatalf("an app label in the tree added ao_note %v", note)
	}

	fail = true
	_, err = s.Call(context.Background(), CallRequest{Tool: "press_key", Arguments: map[string]any{"pid": 7, "window_id": 9, "key": "end"}})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || !strings.HasSuffix(apiErr.Message, foregroundAdvice) {
		t.Fatalf("err = %v, want the driver message followed by AO's advice", err)
	}
}

// bring_to_front is offered and followed by a read of the window it raised.
func TestBringToFrontReadsTheRaisedWindow(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	var tools []string
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		tools = append(tools, args[1])
		if args[1] == "get_window_state" {
			return []byte(`{"snapshot_id":"s1","pid":7,"window_id":9,"element_count":40}`), nil, nil
		}
		return []byte(`{"ok":true}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "bring_to_front", Arguments: map[string]any{"pid": 7, "window_id": 9}})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if strings.Join(tools, " ") != "bring_to_front get_window_state" {
		t.Fatalf("driver tools = %v", tools)
	}
	if observation := result.Result.(map[string]any)["observation"].(map[string]any); observation["ok"] != true {
		t.Fatalf("observation = %v", observation)
	}
}

func TestAgentToolsHideForegroundAndCarryRules(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		switch args[1] {
		case "press_key":
			return []byte("name: press_key\n\ndescription:\nPress a key.\n\ninput_schema:\n" + `{"type":"object","properties":{"key":{"type":"string"},"delivery_mode":{"enum":["background","foreground"]}},"required":["key"]}`), nil, nil
		case "bring_to_front":
			return []byte("name: bring_to_front\n\ndescription:\nPersistently activate an app.\n\ninput_schema:\n" + `{"type":"object","properties":{"pid":{"type":"integer"}},"required":["pid"]}`), nil, nil
		}
		return nil, []byte("no such tool"), errors.New("exit status 1")
	}
	tools, err := newTestService(t, driver, "darwin").AgentTools(context.Background())
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	for _, tool := range tools {
		switch tool.Name {
		case "press_key":
			if _, ok := tool.InputSchema["properties"].(map[string]any)["delivery_mode"]; ok {
				t.Fatalf("delivery_mode left in press_key's schema")
			}
		case "bring_to_front":
			if !strings.Contains(tool.Description, "ask the user before using it") {
				t.Fatalf("bring_to_front description = %q, want AO's rule", tool.Description)
			}
		}
	}
}
