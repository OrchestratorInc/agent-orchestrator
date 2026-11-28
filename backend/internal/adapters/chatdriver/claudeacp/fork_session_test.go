package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type forkConfigPlugin struct{ dir string }

func (forkConfigPlugin) ResolveBinary(context.Context) (string, error) { return "claude", nil }
func (p forkConfigPlugin) NativeSessionConfigDir(context.Context, map[string]string) (string, error) {
	return p.dir, nil
}

func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestClaudeForkScriptMovesFile(t *testing.T) {
	config, src, dst := realDir(t), realDir(t), realDir(t)
	source := filepath.Join(config, "projects", claudeProjectKey(src), "source-id.jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotDir, gotConfig string
	restore := runForkScript
	runForkScript = func(_ context.Context, env []string, sourceID, dir, _ string) (string, error) {
		gotDir = dir
		for _, kv := range env {
			if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
				gotConfig = strings.TrimPrefix(kv, "CLAUDE_CONFIG_DIR=")
			}
		}
		// The SDK writes the child beside the source, keeping the source cwd.
		var lines []byte
		for _, record := range []map[string]string{
			{"type": "user", "sessionId": "child-id", "cwd": src, "uuid": "a"},
			{"type": "assistant", "sessionId": "child-id", "cwd": filepath.Join(src, "sub"), "uuid": "b"},
			{"type": "custom-title", "sessionId": "child-id"},
		} {
			line, err := json.Marshal(record)
			if err != nil {
				return "", err
			}
			lines = append(append(lines, line...), '\n')
		}
		return "child-id", os.WriteFile(filepath.Join(filepath.Dir(source), "child-id.jsonl"), lines, 0o600)
	}
	defer func() { runForkScript = restore }()

	d := &checkpointDriver{plugin: forkConfigPlugin{dir: config}}
	id, err := d.ForkIntoWorkspace(context.Background(), "source-id", src, "", ports.ChatStartConfig{WorkspacePath: dst})
	if err != nil {
		t.Fatal(err)
	}
	if id != "child-id" || gotDir != src || gotConfig != config {
		t.Fatalf("id=%q dir=%q config=%q", id, gotDir, gotConfig)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(source), "child-id.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child left in the source folder: %v", err)
	}
	moved, err := os.ReadFile(filepath.Join(config, "projects", claudeProjectKey(dst), "child-id.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var cwds []string
	for _, line := range strings.Split(strings.TrimSpace(string(moved)), "\n") {
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		cwds = append(cwds, rec.Cwd)
	}
	if cwds[0] != dst || cwds[1] != filepath.Join(dst, "sub") || cwds[2] != "" {
		t.Fatalf("cwds = %v", cwds)
	}
	if b, _ := os.ReadFile(source); string(b) != "{}\n" {
		t.Fatalf("source changed: %q", b)
	}
}

func TestClaudeForkFailureLeavesSourceUntouched(t *testing.T) {
	config, src, dst := realDir(t), realDir(t), realDir(t)
	restore := runForkScript
	runForkScript = func(context.Context, []string, string, string, string) (string, error) {
		return "", errors.New("not found")
	}
	defer func() { runForkScript = restore }()
	d := &checkpointDriver{plugin: forkConfigPlugin{dir: config}}
	if _, err := d.ForkIntoWorkspace(context.Background(), "source-id", src, "", ports.ChatStartConfig{WorkspacePath: dst}); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(config, "projects", claudeProjectKey(dst))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fork folder created: %v", err)
	}
}

func TestClaudeProjectKey(t *testing.T) {
	if got := claudeProjectKey("/Users/a/.ao/w_1"); got != "-Users-a--ao-w-1" {
		t.Fatalf("key = %q", got)
	}
}

// TestClaudeForkRealSDK runs the bundled SDK. Set AO_TEST_CLAUDE_FORK_SESSION
// to a Claude session file and AO_ACP_RUNTIME_DIR to an installed runtime.
func TestClaudeForkRealSDK(t *testing.T) {
	sessionFile := os.Getenv("AO_TEST_CLAUDE_FORK_SESSION")
	if sessionFile == "" || os.Getenv("AO_ACP_RUNTIME_DIR") == "" {
		t.Skip("set AO_TEST_CLAUDE_FORK_SESSION and AO_ACP_RUNTIME_DIR")
	}
	config, src, dst := realDir(t), realDir(t), realDir(t)
	sourceID := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl")
	body, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(config, "projects", claudeProjectKey(src), sourceID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &checkpointDriver{plugin: forkConfigPlugin{dir: config}}
	id, err := d.ForkIntoWorkspace(context.Background(), sourceID, src, "", ports.ChatStartConfig{WorkspacePath: dst})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := os.ReadFile(filepath.Join(config, "projects", claudeProjectKey(dst), id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moved), `"sessionId":"`+id+`"`) || strings.Contains(string(moved), `"cwd":"`+src) {
		t.Fatalf("moved session not rewritten")
	}
	if after, _ := os.ReadFile(source); string(after) != string(body) {
		t.Fatal("source session changed")
	}
}

func TestResolveForkPointUsesLastLineInSourceFile(t *testing.T) {
	dir := realDir(t)
	file := filepath.Join(dir, "s.jsonl")
	// One API message is streamed as several lines; the cut must include all.
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"msg_A","content":[{"type":"thinking"}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"id":"msg_A","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"next"}}`,
		`{"type":"assistant","uuid":"b1","message":{"id":"msg_B","content":[{"type":"text","text":"later"}]}}`,
	}
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveForkPoint(context.Background(), file, "msg_A")
	if err != nil || got != "a2" {
		t.Fatalf("fork point = %q, %v; want a2", got, err)
	}
	if _, err := resolveForkPoint(context.Background(), file, "msg_missing"); err == nil {
		t.Fatal("an unknown message must not fork the whole session")
	}
}

func TestClaudeForkPassesTheCutPoint(t *testing.T) {
	config, src, dst := realDir(t), realDir(t), realDir(t)
	source := filepath.Join(config, "projects", claudeProjectKey(src), "source-id.jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(`{"type":"assistant","uuid":"a2","message":{"id":"msg_A"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotCut string
	restore := runForkScript
	runForkScript = func(_ context.Context, _ []string, _, _, upTo string) (string, error) {
		gotCut = upTo
		return "child", os.WriteFile(filepath.Join(filepath.Dir(source), "child.jsonl"), []byte("{}\n"), 0o600)
	}
	defer func() { runForkScript = restore }()
	d := &checkpointDriver{plugin: forkConfigPlugin{dir: config}}
	if _, err := d.ForkIntoWorkspace(context.Background(), "source-id", src, "msg_A", ports.ChatStartConfig{WorkspacePath: dst}); err != nil {
		t.Fatal(err)
	}
	if gotCut != "a2" {
		t.Fatalf("cut = %q", gotCut)
	}
}

// TestClaudeForkRealSDKAtOlderMessage cuts a real session after its first
// assistant message. Opt-in, like TestClaudeForkRealSDK.
func TestClaudeForkRealSDKAtOlderMessage(t *testing.T) {
	sessionFile := os.Getenv("AO_TEST_CLAUDE_FORK_SESSION")
	if sessionFile == "" || os.Getenv("AO_ACP_RUNTIME_DIR") == "" {
		t.Skip("set AO_TEST_CLAUDE_FORK_SESSION and AO_ACP_RUNTIME_DIR")
	}
	body, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	var first string
	var assistants []string
	for _, line := range strings.Split(string(body), "\n") {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == "assistant" && rec.Message.ID != "" {
			if first == "" {
				first = rec.Message.ID
			}
			if len(assistants) == 0 || assistants[len(assistants)-1] != rec.Message.ID {
				assistants = append(assistants, rec.Message.ID)
			}
		}
	}
	if len(assistants) < 2 {
		t.Skip("the session needs two assistant messages")
	}
	config, src, dst := realDir(t), realDir(t), realDir(t)
	sourceID := strings.TrimSuffix(filepath.Base(sessionFile), ".jsonl")
	source := filepath.Join(config, "projects", claudeProjectKey(src), sourceID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &checkpointDriver{plugin: forkConfigPlugin{dir: config}}
	id, err := d.ForkIntoWorkspace(context.Background(), sourceID, src, first, ports.ChatStartConfig{WorkspacePath: dst})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := os.ReadFile(filepath.Join(config, "projects", claudeProjectKey(dst), id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(moved), assistants[len(assistants)-1]) {
		t.Fatal("the cut fork still contains a later assistant message")
	}
	if !strings.Contains(string(moved), first) {
		t.Fatal("the cut fork lost the message it was cut at")
	}
}

// A fork's session copy is removed even when its workspace is already gone
// and was reached through a symlink, so its folder can't be derived again.
func TestDeleteWorkspaceForkFindsTheCopyWithoutItsWorkspace(t *testing.T) {
	config, resolved := realDir(t), realDir(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	// Saved under the resolved folder's key, as ForkIntoWorkspace does.
	forked := filepath.Join(config, "projects", claudeProjectKey(resolved), "child-id.jsonl")
	other := filepath.Join(config, "projects", claudeProjectKey(resolved), "other-id.jsonl")
	for _, path := range []string{forked, other} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(resolved); err != nil {
		t.Fatal(err)
	}

	d := &checkpointDriver{plugin: forkConfigPlugin{dir: config}}
	if err := d.DeleteWorkspaceFork(context.Background(), "child-id", link, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(forked); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fork's session forked left behind: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another session was removed: %v", err)
	}
	// A pattern in the id must never widen what is removed.
	if err := d.DeleteWorkspaceFork(context.Background(), "*", link, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("a wildcard id removed another session: %v", err)
	}
}

// The copied conversation names the original's worktree in tool inputs and
// results too, not only in cwd. Left as they are, the fork's agent edits the
// original's files, so every reference moves to the fork's worktree. A
// sibling folder that only shares the name's start is left alone.
func TestForkCopyPointsEveryWorktreePathAtTheFork(t *testing.T) {
	src, dst := "/w/fork-test/fork-test-1", "/w/fork-test/fork-test-4"
	record := map[string]any{
		"cwd": src,
		"message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "name": "Write", "input": map[string]any{"file_path": src + "/a.txt"}},
			map[string]any{"type": "tool_result", "content": "Wrote " + src + "/a.txt; left " + src + "0/b.txt alone"},
		}},
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	got := string(rewriteWorkspacePaths(line, []string{src}, dst))
	for _, want := range []string{`"cwd":"` + dst + `"`, `"file_path":"` + dst + `/a.txt"`, "Wrote " + dst + "/a.txt", src + "0/b.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten line lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ReplaceAll(got, src+"0/", ""), src) {
		t.Errorf("the original's worktree is still named:\n%s", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("rewritten line is not JSON: %s", got)
	}
}
