package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGrokDiscoveryAndChunks(t *testing.T) {
	root := t.TempDir()
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
		data += `{"timestamp":1790000000000,"params":{"sessionId":"` + id + `","update":` + item + "}}\n"
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
}
