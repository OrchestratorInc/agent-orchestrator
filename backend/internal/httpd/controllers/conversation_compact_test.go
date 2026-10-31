package controllers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactConversationByteBudget(t *testing.T) {
	output := strings.Repeat("x", 5<<20)
	image := strings.Repeat("i", 660<<10)
	snapshot := ConversationSnapshotResponse{Activities: []ConversationActivityResponse{
		{ID: "command", ActivityKind: "command", Status: "completed", Detail: map[string]any{"command": "cat large.log", "exitCode": float64(0), "output": output, "content": []any{map[string]any{"text": output}}}},
		{ID: "mcp", ActivityKind: "mcp_tool", Status: "completed", Detail: map[string]any{"output": []any{map[string]any{"data": image}}, "content": []any{map[string]any{"type": "image", "data": image, "mimeType": "image/png"}}}},
	}}
	before, _ := json.Marshal(snapshot)
	compactConversationDetails(&snapshot, true, true)
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) >= 64<<10 {
		t.Fatalf("compact response = %d bytes, budget <65536", len(body))
	}
	t.Logf("5 MiB command + duplicated 660 KiB image: before=%d after=%d response bytes", len(before), len(body))
	command := snapshot.Activities[0]
	if command.Detail["command"] != "cat large.log" || command.Detail["exitCode"] != float64(0) || !command.DetailOmitted {
		t.Fatal("short fields or omission marker lost")
	}
	if command.Detail["contentOmitted"] != "output" || snapshot.Activities[1].Detail["outputOmitted"] != "content" {
		t.Fatal("duplicate source markers missing")
	}
	// The byte budget applies to settled detail. A live row deliberately keeps
	// every byte, including any duplicate needed by streaming consumers.
	live := ConversationSnapshotResponse{Activities: []ConversationActivityResponse{{
		ActivityKind: "command", Status: "running", Detail: map[string]any{"output": output, "content": output},
	}}}
	compactConversationDetails(&live, true, true)
	if live.Activities[0].DetailOmitted || live.Activities[0].Detail["output"] != output || live.Activities[0].Detail["content"] != output {
		t.Fatal("5 MiB live command output was changed")
	}
}

func TestCompactConversationEligibilityAndUTF8(t *testing.T) {
	long := strings.Repeat("界", 100)
	for _, kind := range []string{"command", "mcp_tool", "file_change", "reasoning", "system", "approval", "user_input", "plan"} {
		for _, status := range []string{"completed", "running", "pending"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				snapshot := ConversationSnapshotResponse{Activities: []ConversationActivityResponse{{ID: "row", ActivityKind: kind, Status: status, Detail: map[string]any{"text": long, "progress": long + "last line", "nested": []any{map[string]any{"patch": long}}}}}}
				compactConversationDetails(&snapshot, true, true)
				row := snapshot.Activities[0]
				eligible := status == "completed" && (kind == "command" || kind == "mcp_tool" || kind == "file_change" || kind == "reasoning")
				if row.DetailOmitted != eligible {
					t.Fatalf("omitted=%v eligible=%v", row.DetailOmitted, eligible)
				}
				text := row.Detail["text"].(string)
				progress := row.Detail["progress"].(string)
				if !eligible && text != long {
					t.Fatal("interactive/inline row changed")
				}
				if eligible && (len(text) > 128 || !utf8.ValidString(text) || !utf8.ValidString(progress) || len(progress) > 128 || !strings.HasSuffix(progress, "last line")) {
					t.Fatal("invalid UTF-8 preview/tail")
				}
			})
		}
	}
}

func TestCompactConversationPreservesFileNavigation(t *testing.T) {
	cwd := "/workspace/" + strings.Repeat("long-worktree-segment/", 12) + "project"
	path := cwd + "/packages/" + strings.Repeat("nested/", 24) + "new.ts"
	oldPath := cwd + "/archive/" + strings.Repeat("nested/", 24) + "old.ts"
	body := strings.Repeat("large body", 100)
	file := map[string]any{"path": path, "oldPath": oldPath, "patch": body}
	snapshot := ConversationSnapshotResponse{Activities: []ConversationActivityResponse{
		{ID: "command", ActivityKind: "command", Status: "completed", Detail: map[string]any{"cwd": cwd, "output": body}},
		{ID: "file", ActivityKind: "file_change", Status: "completed", Detail: map[string]any{"files": []any{file}}},
		{ID: "tool", ActivityKind: "mcp_tool", Status: "completed", Detail: map[string]any{"result": map[string]any{"cwd": body, "path": body, "oldPath": body}}},
	}}
	compactConversationDetails(&snapshot, true, true)
	if snapshot.Activities[0].Detail["cwd"] != cwd || file["path"] != path || file["oldPath"] != oldPath {
		t.Fatal("collapsed turn file-navigation addresses were shortened")
	}
	if len(snapshot.Activities[0].Detail["output"].(string)) != activityPreviewBytes || len(file["patch"].(string)) != activityPreviewBytes {
		t.Fatal("preserving navigation bypassed body previews")
	}
	for _, value := range snapshot.Activities[2].Detail["result"].(map[string]any) {
		if len(value.(string)) != activityPreviewBytes {
			t.Fatal("unrelated nested payload keys bypassed body previews")
		}
	}
	for _, row := range snapshot.Activities {
		if !row.DetailOmitted {
			t.Fatal("body omission marker lost")
		}
	}
	metadataOnly := ConversationSnapshotResponse{Activities: []ConversationActivityResponse{{
		ActivityKind: "file_change", Status: "completed", Detail: map[string]any{"cwd": cwd, "files": []any{map[string]any{"path": path, "oldPath": oldPath}}},
	}}}
	compactConversationDetails(&metadataOnly, true, true)
	if metadataOnly.Activities[0].DetailOmitted {
		t.Fatal("complete navigation metadata unnecessarily requires a full-row read")
	}
}

func TestDedupePreservesDistinctBodies(t *testing.T) {
	long := strings.Repeat("body", 100)
	for _, tc := range []struct {
		removed, kept any
		omit          bool
	}{
		{long, long, true},
		{[]any{map[string]any{"text": long}}, long, true},
		{[]any{long, long + "unique"}, long, false},
		{"short", "short", false},
		{long, nil, false},
	} {
		detail := map[string]any{"content": tc.removed, "output": tc.kept, "command": "echo ok", "rawCommand": "echo ok"}
		dedupeActivityDetail("command", detail)
		_, exists := detail["content"]
		if exists == tc.omit {
			t.Fatalf("omit=%v detail=%v", tc.omit, detail)
		}
		if detail["rawCommandOmitted"] != "command" {
			t.Fatal("exact raw command duplicate retained")
		}
	}
}

func TestCompactionLeavesIDsOrderAndLegacyResponse(t *testing.T) {
	original := ConversationSnapshotResponse{Messages: []ConversationMessageResponse{{ID: "m", Sequence: 1}}, Activities: []ConversationActivityResponse{{ID: "a", Revision: 7, Sequence: 2, ActivityKind: "command", Status: "completed", Detail: map[string]any{"output": strings.Repeat("x", 300)}}}}
	body, _ := json.Marshal(original)
	var legacy, compact ConversationSnapshotResponse
	_ = json.Unmarshal(body, &legacy)
	_ = json.Unmarshal(body, &compact)
	compactConversationDetails(&legacy, false, false)
	after, _ := json.Marshal(legacy)
	if string(body) != string(after) {
		t.Fatal("legacy bytes changed")
	}
	compactConversationDetails(&compact, true, true)
	if !reflect.DeepEqual(original.Messages, compact.Messages) || compact.Activities[0].ID != "a" || compact.Activities[0].Revision != 7 || compact.Activities[0].Sequence != 2 {
		t.Fatal("identity/order changed")
	}
}

func TestConversationDetailFlagsAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name            string
		compact, dedupe bool
	}{
		{"legacy", false, false}, {"preview only", true, false}, {"dedupe only", false, true}, {"both", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full := strings.Repeat("output", 100)
			snapshot := ConversationSnapshotResponse{Activities: []ConversationActivityResponse{{ActivityKind: "command", Status: "completed", Detail: map[string]any{"command": "cat file", "rawCommand": "cat file", "output": full, "content": full}}}}
			compactConversationDetails(&snapshot, tc.compact, tc.dedupe)
			row := snapshot.Activities[0]
			if row.DetailOmitted != tc.compact {
				t.Fatal("preview opt-in changed")
			}
			_, contentPresent := row.Detail["content"]
			if contentPresent == tc.dedupe {
				t.Fatal("dedupe opt-in changed")
			}
			if !tc.compact && row.Detail["output"] != full {
				t.Fatal("deduplication shortened retained body")
			}
		})
	}
}
