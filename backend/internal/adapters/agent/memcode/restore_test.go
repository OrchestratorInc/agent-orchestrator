package memcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const testID = "sess_a0a0a0a0"
const readyTerminal = "○ idle\n→  Ask memcode…   ·   $ = shell\nmemcode · glm-5.3-flash · allow-all"

func seedHistory(t *testing.T, workspace, body string) {
	t.Helper()
	dir := filepath.Join(workspace, ".memcode", "sessions", testID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

const validHistory = `{"session_id":"sess_a0a0a0a0","messages":[{"role":"user","content":[]}]}`

func TestHistoryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		pass       bool
	}{{"valid", validHistory, true}, {"missing", "", false}, {"corrupt", "{", false}, {"empty", `{"session_id":"sess_a0a0a0a0","messages":[]}`, false}, {"mismatch", strings.Replace(validHistory, testID, "sess_b1b1b1b1", 1), false}, {"badwire", `{"session_id":"sess_a0a0a0a0","messages":[{"role":"user","content":"text"}]}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.body != "" {
				seedHistory(t, dir, tc.body)
			}
			_, _, err := transcript(dir, testID)
			if (err == nil) != tc.pass {
				t.Fatalf("history error %v", err)
			}
		})
	}
}
func TestRestoreRequiresCurrentWitnessAndEmptyComposer(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	seedHistory(t, workspace, validHistory)
	digest, count, err := transcript(workspace, testID)
	if err != nil {
		t.Fatal(err)
	}
	p := &restorePlan{workspace: workspace, dataDir: data, session: "ao-1", target: testID, digest: digest, count: count}
	write := func(g, id string, sequence int) {
		path, _ := witnessPath(data, "ao-1", "launch-1")
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		b, _ := json.Marshal(witness{g, id, sequence})
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("launch-old", "sess_b1b1b1b1", 1)
	if ok, _ := p.Ready(context.Background(), "launch-1", readyTerminal); ok {
		t.Fatal("stale witness accepted")
	}
	write("launch-1", "sess_b1b1b1b1", 1)
	if ok, err := p.Ready(context.Background(), "launch-1", readyTerminal); !ok || err != nil {
		t.Fatal(ok, err)
	}
	banner := readyTerminal + "\n↩ resumed " + testID + " (1 messages)"
	if ok, _ := p.Restored(context.Background(), "launch-1", banner); ok {
		t.Fatal("false banner accepted")
	}
	write("launch-1", testID, 2)
	if ok, err := p.Restored(context.Background(), "launch-1", banner); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := p.Restored(context.Background(), "launch-1", strings.Replace(banner, "Ask memcode…", "user draft", 1)); ok {
		t.Fatal("draft accepted")
	}
	write("launch-1", "sess_c2c2c2c2", 3)
	if _, err := p.Restored(context.Background(), "launch-1", banner); err == nil {
		t.Fatal("wrong native identity accepted")
	}
	seedHistory(t, workspace, validHistory+" ")
	if _, err := p.Ready(context.Background(), "launch-1", readyTerminal); err == nil {
		t.Fatal("changed transcript accepted")
	}
}
func TestDirectRestoreCannotClaimNativeResume(t *testing.T) {
	_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{})
	if err == nil || ok {
		t.Fatal(ok, err)
	}
}
func TestModelDoesNotPretendFlagOverridesNativeSelection(t *testing.T) {
	workspace := t.TempDir()
	_ = os.MkdirAll(filepath.Join(workspace, ".memcode"), 0700)
	_ = os.WriteFile(filepath.Join(workspace, ".memcode", "config.json"), []byte(`{"endpoints":[{"name":"local","base_url":"http://127.0.0.1:9","last_model":"actual"}]}`), 0600)
	t.Setenv("MEMCODE_ENDPOINT_URL", "")
	if err := validateModel(workspace, "actual"); err == nil {
		t.Fatal("partial native provider selection accepted")
	}
	if err := validateModel(workspace, ""); err != nil {
		t.Fatal(err)
	}
	if err := validateModel(workspace, "ignored-override"); err == nil {
		t.Fatal("model silently ignored")
	}
}
func TestComposerRejectsHistoricalChrome(t *testing.T) {
	if emptyComposer(readyTerminal + strings.Repeat("\nnew output", 20)) {
		t.Fatal("historical idle composer accepted")
	}
}
func TestPermissionAndToolMappingsFailClosed(t *testing.T) {
	for _, cfg := range []ports.LaunchConfig{{Permissions: ports.PermissionModeAcceptEdits}, {AllowedTools: []string{"read"}}, {DisallowedTools: []string{"bash"}}} {
		if _, err := New().GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatal("unsupported capability accepted")
		}
	}
}
func TestCurrentNativeDecisionCardsAreBlocked(t *testing.T) {
	for _, card := range []string{"Do you want to proceed?\n❯ 1. Yes\n 2. Yes, and don't ask again\n 3. No, and tell me what to do differently (esc)", "Question?\n↑↓ select · Enter · or type your own answer · Esc to skip", "Plan\n↑↓ select · Enter · type to revise · Esc cancel"} {
		output := card + "\n→  Ask memcode…   ·   $ = shell\nmemcode · model · ask"
		state, ok := New().DetectTerminalActivity(output)
		if !ok || state != "blocked" || New().ComposerIsEmpty(output) {
			t.Fatal(state, ok)
		}
		if state, ok := New().DetectTerminalActivity(output + strings.Repeat("\nnew", 70)); ok && state == "blocked" {
			t.Fatal("historical card accepted")
		}
		if state, ok := New().DetectTerminalActivity(card + "\n" + readyTerminal); !ok || state != "idle" {
			t.Fatal("settled composer not recovered", state, ok)
		}
	}
}
