package memcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemcodeNativeRestoreRejectsUndecodableContentBlocks(t *testing.T) {
	for _, block := range []string{
		`1`, `true`, `"text"`, `[]`,
		`{"type":1}`, `{"type":"text","text":1}`,
		`{"type":"tool_result","is_error":"false"}`,
		`{"type":"tool_result","content":[]}`,
		`{"type":"tool_result","content_blocks":[1]}`,
		`{"type":"image","source":{"data":1}}`,
		`{"type":"text","cache_control":{"ttl":1}}`,
	} {
		workspace := t.TempDir()
		body := `{"session_id":"` + testID + `","messages":[{"role":"user","content":[{"type":"text","text":"valid prompt"}]},{"role":"assistant","content":[` + block + `]}]}`
		seedHistory(t, workspace, body)
		if _, _, err := transcript(workspace, testID); err == nil || !strings.Contains(err.Error(), "invalid wire block") {
			t.Fatalf("undecodable native block accepted: %s, %v", block, err)
		}
		got, err := os.ReadFile(filepath.Join(workspace, ".memcode", "sessions", testID, "messages.json"))
		if err != nil || string(got) != body {
			t.Fatal("validation changed native history", err)
		}
	}
}

func TestMemcodeNativeRestorePreservesSupportedWireBlocks(t *testing.T) {
	workspace := t.TempDir()
	body := `{"session_id":"` + testID + `","messages":[{"role":"user","content":[{"type":"text","text":"prompt","unknown_native_field":true}]},{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"read_file","input":{"path":"probe.txt"}},{"type":"thinking","thinking":"","signature":"native-signature"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","is_error":false,"content_blocks":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"synthetic"},"cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}]}`
	seedHistory(t, workspace, body)
	if _, count, err := transcript(workspace, testID); err != nil || count != 3 {
		t.Fatal("valid native wire history refused", count, err)
	}
	got, err := os.ReadFile(filepath.Join(workspace, ".memcode", "sessions", testID, "messages.json"))
	if err != nil || string(got) != body {
		t.Fatal("validation rewrote valid native blocks", err)
	}
}
