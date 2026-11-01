package qwen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const qwenTestID = "807b463e-6160-40ef-aa01-eb87dcc061e1"

func TestNativeSessionConfigDir(t *testing.T) {
	p := New()
	t.Run("QWEN_HOME wins", func(t *testing.T) {
		dir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{qwenHomeEnv: "/custom/qwen"})
		if err != nil {
			t.Fatal(err)
		}
		if dir != "/custom/qwen" {
			t.Fatalf("dir = %q", dir)
		}
	})
	t.Run("defaults under HOME", func(t *testing.T) {
		dir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"HOME": "/home/dev"})
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/home/dev", ".qwen"); dir != want {
			t.Fatalf("dir = %q, want %q", dir, want)
		}
	})
}

// TestSanitizeQwenCwd pins Qwen Code's folder encoder: every non-alphanumeric
// byte becomes a dash, so the leading slash and the dot in ".qwen" both dash.
func TestSanitizeQwenCwd(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/.ao/data", "-Users-x--ao-data"},
		{"/work/repo", "-work-repo"},
		{"/a.b/c-d", "-a-b-c-d"},
	}
	for _, c := range cases {
		if got := sanitizeQwenCwd(c.in); got != c.want {
			t.Fatalf("sanitizeQwenCwd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func writeQwenSource(t *testing.T, configDir, cwd, id string, lines []string) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", sanitizeQwenCwd(resolveQwenCwd(cwd)), "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func qwenLines(cwd string) []string {
	return []string{
		`{"type":"system","sessionId":"` + qwenTestID + `","cwd":"` + cwd + `","provenance":"system"}`,
		`{"type":"user","sessionId":"` + qwenTestID + `","cwd":"` + cwd + `","message":{"role":"user","parts":[{"text":"hi"}]}}`,
		`{"type":"assistant","sessionId":"` + qwenTestID + `","cwd":"` + cwd + `","message":{"role":"model","parts":[{"text":"hello"}]}}`,
	}
}

func TestPrepareNativeSessionResumeCopiesAndRewritesCWD(t *testing.T) {
	p := New()
	configDir := t.TempDir()
	srcCWD := filepath.Join(t.TempDir(), "orig")
	worktree := filepath.Join(t.TempDir(), "worktree")
	for _, d := range []string{srcCWD, worktree} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	srcPath := writeQwenSource(t, configDir, srcCWD, qwenTestID, qwenLines(srcCWD))
	srcBefore, _ := os.ReadFile(srcPath)

	ref := ports.NativeSessionRef{NativeSessionID: qwenTestID, ConfigDir: configDir}
	prepared, err := p.PrepareNativeSessionResume(context.Background(), ref, worktree, "")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	want := qwenChatPath(configDir, worktree, qwenTestID)
	if prepared != want {
		t.Fatalf("prepared = %q, want %q", prepared, want)
	}
	info, err := os.Stat(prepared)
	if err != nil {
		t.Fatalf("stat prepared: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	// Every record's cwd is rewritten to the resolved worktree; none keeps the source cwd.
	body, _ := os.ReadFile(prepared)
	resolvedWorktree := resolveQwenCwd(worktree)
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line not json: %q", line)
		}
		var cwd string
		_ = json.Unmarshal(rec["cwd"], &cwd)
		if cwd != resolvedWorktree {
			t.Fatalf("cwd = %q, want %q", cwd, resolvedWorktree)
		}
	}
	// The original is untouched.
	if after, _ := os.ReadFile(srcPath); string(after) != string(srcBefore) {
		t.Fatal("source transcript was modified")
	}
}

func TestPrepareNativeSessionResumeIdempotent(t *testing.T) {
	p := New()
	configDir := t.TempDir()
	srcCWD := filepath.Join(t.TempDir(), "orig")
	worktree := filepath.Join(t.TempDir(), "worktree")
	_ = os.MkdirAll(srcCWD, 0o755)
	_ = os.MkdirAll(worktree, 0o755)
	writeQwenSource(t, configDir, srcCWD, qwenTestID, qwenLines(srcCWD))

	ref := ports.NativeSessionRef{NativeSessionID: qwenTestID, ConfigDir: configDir}
	prepared, err := p.PrepareNativeSessionResume(context.Background(), ref, worktree, "")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate AO appending a turn to the copy after the first resume.
	appended := "\n" + `{"type":"user","sessionId":"` + qwenTestID + `","cwd":"x","message":{"role":"user","parts":[{"text":"second"}]}}` + "\n"
	f, _ := os.OpenFile(prepared, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(appended)
	_ = f.Close()
	withTurn, _ := os.ReadFile(prepared)

	// A later open at the same cwd must not overwrite the copy that now holds AO's turn.
	again, err := p.PrepareNativeSessionResume(context.Background(), ref, worktree, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if again != prepared {
		t.Fatalf("again = %q, want %q", again, prepared)
	}
	if after, _ := os.ReadFile(prepared); string(after) != string(withTurn) {
		t.Fatal("idempotent prepare overwrote AO's appended turn")
	}
}

func TestPrepareNativeSessionResumeRekeysFromPriorCopy(t *testing.T) {
	p := New()
	configDir := t.TempDir()
	srcCWD := filepath.Join(t.TempDir(), "orig")
	worktreeA := filepath.Join(t.TempDir(), "wt-a")
	worktreeB := filepath.Join(t.TempDir(), "wt-b")
	for _, d := range []string{srcCWD, worktreeA, worktreeB} {
		_ = os.MkdirAll(d, 0o755)
	}
	writeQwenSource(t, configDir, srcCWD, qwenTestID, qwenLines(srcCWD))
	ref := ports.NativeSessionRef{NativeSessionID: qwenTestID, ConfigDir: configDir}

	// First open prepares for worktree A, then AO appends a unique turn.
	preparedA, err := p.PrepareNativeSessionResume(context.Background(), ref, worktreeA, "")
	if err != nil {
		t.Fatal(err)
	}
	marker := `{"type":"user","sessionId":"` + qwenTestID + `","cwd":"x","message":{"role":"user","parts":[{"text":"AO-ONLY-TURN"}]}}`
	f, _ := os.OpenFile(preparedA, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(marker + "\n")
	_ = f.Close()

	// The launch cwd drifts to worktree B; re-key must copy from A's copy (with the
	// appended turn), not the untouched original.
	preparedB, err := p.PrepareNativeSessionResume(context.Background(), ref, worktreeB, worktreeA)
	if err != nil {
		t.Fatal(err)
	}
	if preparedB == preparedA {
		t.Fatal("expected a distinct target for the drifted cwd")
	}
	body, _ := os.ReadFile(preparedB)
	if !strings.Contains(string(body), "AO-ONLY-TURN") {
		t.Fatal("re-key did not copy AO's appended turn from the prior copy")
	}
}

func TestPrepareNativeSessionResumeMissingSource(t *testing.T) {
	p := New()
	configDir := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "worktree")
	_ = os.MkdirAll(worktree, 0o755)
	ref := ports.NativeSessionRef{NativeSessionID: qwenTestID, ConfigDir: configDir}
	if _, err := p.PrepareNativeSessionResume(context.Background(), ref, worktree, ""); err == nil {
		t.Fatal("expected a clear error when the source transcript is absent")
	}
}

func TestLocateTranscriptRejectsBadID(t *testing.T) {
	p := New()
	ref := ports.NativeSessionRef{NativeSessionID: "not-a-uuid", ConfigDir: t.TempDir()}
	if _, _, err := p.LocateTranscript(context.Background(), ref); err == nil {
		t.Fatal("expected an invalid-id error")
	}
}
