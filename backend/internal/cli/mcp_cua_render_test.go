package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeCuaResponse(t *testing.T, raw string) cuaCallResponse {
	t.Helper()
	var response cuaCallResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestRenderCuaWindowRead(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"get_window_state","result":{
		"window_id":1234567,"pid":42166,"screenshot_width":460,"screenshot_height":816,"element_count":33,
		"snapshot_id":"s0000002e","screenshot_file_path":"/data/a.png",
		"tree_markdown":"- [7] AXWindow \"Calculator\"\n    - [12] AXButton (7) [id=Seven actions=[press]]",
		"truncated":true,"returned_element_count":33,"total_element_count":90,
		"invalidated_snapshot_ids":["s0000002d"]},
		"images":[{"path":"/data/a.png","mimeType":"image/png"}]}`)
	text, ok := renderCuaCall(response)
	if !ok {
		t.Fatal("window read not rendered")
	}
	want := `window_id=1234567 pid=42166 size=460x816 elements=33 snapshot_id=s0000002e
element_token for row [N] = s0000002e:N (row [1] is "s0000002e:1")

- [7] AXWindow "Calculator"
    - [12] AXButton (7) [id=Seven actions=[press]]
Tree truncated: 33 of 90 elements shown; pass query, or a higher max_elements.
Invalidated snapshots s0000002d: their element_tokens are stale.
`
	if text != want {
		t.Fatalf("text =\n%s\nwant\n%s", text, want)
	}
}

func TestRenderCuaWindowReadAsElements(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"get_window_state","result":{
		"window_id":9,"pid":7,"element_count":2,"snapshot_id":"s1",
		"elements":[
			{"element_index":1,"element_token":"s1:1","role":"AXWindow","label":"Calculator","depth":0},
			{"element_index":2,"element_token":"s1:2","role":"AXButton","label":"Clear","frame":{"x":1,"y":2,"w":3,"h":4},"parent_index":1,"depth":1}
		]},
		"images":[]}`)
	text, ok := renderCuaCall(response)
	if !ok {
		t.Fatal("window read not rendered")
	}
	want := `window_id=9 pid=7 elements=2 snapshot_id=s1

{"depth":0,"element_index":1,"element_token":"s1:1","label":"Calculator","role":"AXWindow"}
{"depth":1,"element_index":2,"element_token":"s1:2","frame":{"h":4,"w":3,"x":1,"y":2},"label":"Clear","parent_index":1,"role":"AXButton"}
`
	if text != want {
		t.Fatalf("text =\n%s\nwant\n%s", text, want)
	}
}

func TestRenderCuaActionWithObservation(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"click","result":{
		"summary":"✅ Performed AXPress on [12] AXButton \"\".","effect":"unverifiable","hint":"Read what changed.",
		"observation":{"ok":true,"tool":"get_window_state","state":{"window_id":37545,"pid":42166,"element_count":33,
			"snapshot_id":"s0000002f","screenshot_file_path":"/data/b.png",
			"tree_diff":"since s0000002e: 0 added, 1 changed, 0 removed.\n~ AXStaticText = \"7\" (Edit field)",
			"invalidated_snapshot_ids":["s0000002e"]}}},
		"images":[{"path":"/data/b.png","mimeType":"image/png"}]}`)
	text, _ := renderCuaCall(response)
	for _, want := range []string{
		"✅ Performed AXPress on [12] AXButton \"\".\n\nwindow_id=37545 pid=42166 elements=33 snapshot_id=s0000002f\n",
		"\nsince s0000002e: 0 added, 1 changed, 0 removed.\n~ AXStaticText = \"7\" (Edit field)\n",
		"Invalidated snapshots s0000002e: their element_tokens are stale.\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Read what changed.") || strings.Contains(text, "/data/b.png") {
		t.Fatalf("hint or screenshot path printed after a read:\n%s", text)
	}
}

func TestRenderCuaActionWithoutObservation(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"hotkey","result":{"summary":"Pressed cmd+f.","hint":"Read what changed.",
		"observation":{"ok":false,"skipped":"AO could not tell which window this action ran in."}},"images":[]}`)
	text, _ := renderCuaCall(response)
	if text != "Pressed cmd+f.\nobservation skipped: AO could not tell which window this action ran in.\nRead what changed.\n" {
		t.Fatalf("text =\n%s", text)
	}
}

func TestRenderCuaRunSteps(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"run_steps","result":{"ok":false,"executed":2,"total":3,"failed_step":1,
		"steps":[{"index":0,"tool":"click","ok":true,"message":"clicked Clear"},{"index":1,"tool":"type_text","ok":false,"phase":"find","message":"no textfield named Search"}],
		"observation":{"ok":true,"summary":"changed since s00000030: 1 changed","state":{"window_id":9,"pid":7,"snapshot_id":"s00000032",
			"screenshot_file_path":"/data/c.png","tree_diff":"since s00000030: 1 changed"}}},
		"images":[{"path":"/data/c.png","mimeType":"image/png"}]}`)
	text, _ := renderCuaCall(response)
	want := `run_steps: 1/3 steps ok
1. click ok: clicked Clear
2. type_text failed (find): no textfield named Search
observation: changed since s00000030: 1 changed

window_id=9 pid=7 snapshot_id=s00000032

since s00000030: 1 changed
`
	if text != want {
		t.Fatalf("text =\n%s\nwant\n%s", text, want)
	}
}

// A step can succeed while the read after the batch fails; the agent must
// learn it could not see the result.
func TestRenderCuaRunStepsWithFailedObservation(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"run_steps","result":{"ok":true,"executed":1,"total":1,
		"steps":[{"index":0,"tool":"click","ok":true,"message":"✅ Performed AXPress on [2] AXButton \"\"."}],
		"observation":{"ok":false,"tool":"get_window_state","message":"window_id 99999999 is not a live window (closed, or the id is stale)."}},
		"images":[]}`)
	text, _ := renderCuaCall(response)
	want := `run_steps: 1/1 steps ok
1. click ok: ✅ Performed AXPress on [2] AXButton "".
observation failed: window_id 99999999 is not a live window (closed, or the id is stale).
The steps above ran, but AO could not read the window after them. Read it with get_window_state before acting again.
`
	if text != want {
		t.Fatalf("text =\n%s\nwant\n%s", text, want)
	}
}

func TestRenderCuaLeavesOtherToolsToJSON(t *testing.T) {
	for _, raw := range []string{
		`{"tool":"list_windows","result":{"windows":[]},"images":[]}`,
		`{"tool":"click","result":"ok","images":[]}`,
	} {
		if _, ok := renderCuaCall(decodeCuaResponse(t, raw)); ok {
			t.Fatalf("rendered %s", raw)
		}
	}
}

func TestRenderCuaWindowWarnsWhenUnrendered(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"get_window_state","result":{"window_id":9,"pid":7,"element_count":1,"snapshot_id":"s1",
		"render_warning":"This window may not have rendered."},"images":[]}`)
	text, _ := renderCuaCall(response)
	if !strings.Contains(text, "window_id=9 pid=7 elements=1 snapshot_id=s1\nwarning: This window may not have rendered.\n") {
		t.Fatalf("text =\n%s", text)
	}
}

func TestRenderCuaPrintsAONote(t *testing.T) {
	response := decodeCuaResponse(t, `{"tool":"press_key","result":{"summary":"Sent end","ao_note":"AO refuses delivery_mode foreground.",
		"observation":{"ok":false,"skipped":"no window"}},"images":[]}`)
	text, _ := renderCuaCall(response)
	if !strings.HasSuffix(text, "note: AO refuses delivery_mode foreground.\n") {
		t.Fatalf("text =\n%s", text)
	}
}
