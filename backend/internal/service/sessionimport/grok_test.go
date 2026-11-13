package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGrokDiscoveryAndChunks(t *testing.T) {
	root := canonical(t.TempDir())
	id := "00000000-0000-7000-8000-000000000001"
	dir := filepath.Join(root, "sessions", "%2Fwork", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"info":{"id":"`+id+`","cwd":"/work"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data := ""
	for _, item := range []string{
		`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"Fix widget"}}`,
		`{"sessionUpdate":"tool_call","content":{"type":"text","text":"noise"}}`,
		`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"hidden"}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"All "}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}`,
		`{"sessionUpdate":"turn_completed"}`,
	} {
		data += `{"timestamp":1790000000,"params":{"sessionId":"` + id + `","update":` + item + "}}\n"
	}
	data += `{"torn":`
	path := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := discoverGrok(context.Background(), root)
	if err != nil || len(files) != 1 || files[0].nativeID() != id {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	got, err := readGrokTranscript(context.Background(), root, path)
	if err != nil || got.title != "Fix widget" || len(got.messages) != 2 || got.messages[1].Text != "All done" {
		t.Fatalf("transcript=%+v err=%v", got, err)
	}
	if !got.last.Equal(time.Unix(1790000000, 0)) || !got.messages[0].At.Equal(got.last) {
		t.Fatalf("native timestamps=%v message=%v", got.last, got.messages[0].At)
	}
}

func TestGrokRewindDropsDiscardedPrompts(t *testing.T) {
	root := canonical(t.TempDir())
	id := "00000000-0000-7000-8000-000000000002"
	dir := filepath.Join(root, "sessions", "bucket", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"info":{"id":"`+id+`","cwd":"/work"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, update := range []string{
		`{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":0},"content":{"type":"text","text":"first request"}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"first answer"}}`,
		`{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":1},"content":{"type":"text","text":"discard request"}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"discard answer"}}`,
		`{"sessionUpdate":"rewind_marker","target_prompt_index":1}`,
		`{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":1},"content":{"type":"text","text":"replacement"}}`,
		`{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":2},"content":{"type":"text","text":"next adjacent prompt"}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"next answer"}}`,
		`{"sessionUpdate":"rewind_marker","target_prompt_index":2}`,
		`{"sessionUpdate":"user_message_chunk","_meta":{"hostTurn":true},"content":{"type":"text","text":"injected host context"}}`,
		`{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":2},"content":{"type":"text","text":"final request"}}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"final answer"}}`,
		`{"sessionUpdate":"rewind_marker","target_prompt_index":99}`,
	} {
		var kind struct {
			SessionUpdate string `json:"sessionUpdate"`
		}
		if err := json.Unmarshal([]byte(update), &kind); err != nil {
			t.Fatal(err)
		}
		method := "session/update"
		if kind.SessionUpdate == "rewind_marker" {
			method = "_x.ai/session/update"
		}
		data = append(data, []byte(`{"method":"`+method+`","params":{"sessionId":"`+id+`","update":`+update+"}}\n")...)
	}
	path := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readGrokTranscript(context.Background(), root, path)
	if err != nil || len(got.messages) != 5 {
		t.Fatalf("rewound=%+v err=%v", got, err)
	}
	for i, want := range []string{"first request", "first answer", "replacement", "final request", "final answer"} {
		if got.messages[i].Text != want {
			t.Fatalf("message %d=%q want=%q", i, got.messages[i].Text, want)
		}
	}
	info, err := os.Stat(path)
	if err != nil || !got.last.Equal(info.ModTime()) || !got.messages[0].At.Equal(info.ModTime()) {
		t.Fatalf("legacy timestamp fallback=%v err=%v", got.last, err)
	}
}
