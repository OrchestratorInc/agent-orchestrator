package qwen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const qwenTestID = "807b463e-6160-40ef-aa01-eb87dcc061e1"

func TestNativeSessionConfigDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"override", map[string]string{qwenHomeEnv: t.TempDir()}},
		{"default", map[string]string{"HOME": t.TempDir(), qwenHomeEnv: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.env[qwenHomeEnv]
			if want == "" {
				want = filepath.Join(tc.env["HOME"], ".qwen")
			}
			got, err := New().NativeSessionConfigDir(context.Background(), tc.env)
			if err != nil || got != want {
				t.Fatalf("config dir = %q, %v; want %q", got, err, want)
			}
		})
	}
}

// Qwen 0.26.4 sanitizeCwd: lowercase on win32, then /[^a-zA-Z0-9]/g.
// JavaScript's regexp replaces each UTF-16 code unit (two for an emoji).
func TestSanitizeQwenCwd(t *testing.T) {
	for _, tc := range []struct {
		cwd     string
		windows bool
		want    string
	}{
		{"/Users/x/.ao/data", false, "-Users-x--ao-data"},
		{"/a_b/has space", false, "-a-b-has-space"},
		{"/é/中文/🙂", false, "--------"},
		{`C:\Users\Dev_Name\My Repo`, true, "c--users-dev-name-my-repo"},
		{`C:\Users\Dev_Name`, false, "C--Users-Dev-Name"},
		{"/İ", true, "-i-"},
	} {
		if got := sanitizeQwenCwdForPlatform(tc.cwd, tc.windows); got != tc.want {
			t.Errorf("sanitize(%q, windows=%v) = %q, want %q", tc.cwd, tc.windows, got, tc.want)
		}
	}
}

func writeQwenSource(t *testing.T, root, cwd string) string {
	t.Helper()
	path := qwenChatPath(root, cwd, qwenTestID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, kind := range []string{"system", "user", "assistant"} {
		line, err := json.Marshal(map[string]any{"type": kind, "sessionId": qwenTestID, "cwd": cwd, "opaque": map[string]any{"number": 1234567890123456789, "text": "<native>"}})
		if err != nil {
			t.Fatal(err)
		}
		body.Write(line)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRelocateImportedSessionRawSymlinkCWD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privileges")
	}
	root := t.TempDir()
	realDir := t.TempDir()
	rawDir := filepath.Join(t.TempDir(), "launch-link")
	if err := os.Symlink(realDir, rawDir); err != nil {
		t.Fatal(err)
	}
	source := writeQwenSource(t, root, t.TempDir())
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	r := ports.ImportedSessionRelocation{NativeSessionID: qwenTestID, ConfigDir: root, TranscriptPath: source, LaunchCWD: rawDir}
	if err := New().RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	target := qwenChatPath(root, rawDir, qwenTestID)
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var rec struct {
			CWD    string          `json:"cwd"`
			Opaque json.RawMessage `json:"opaque"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.CWD != rawDir {
			t.Fatalf("cwd = %q, error = %v; want raw %q", rec.CWD, err, rawDir)
		}
		// The fixture's json.Marshal escapes HTML; compare native JSON values.
		var native struct {
			Number json.Number `json:"number"`
			Text   string      `json:"text"`
		}
		if err := json.Unmarshal(rec.Opaque, &native); err != nil || native.Number.String() != "1234567890123456789" || native.Text != "<native>" {
			t.Fatalf("native fields changed: %s (decode error: %v)", rec.Opaque, err)
		}
	}
	if _, err := os.Stat(qwenChatPath(root, realDir, qwenTestID)); !os.IsNotExist(err) {
		t.Fatalf("resolved cwd copy should not exist: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected private copy: %v, %v", info, err)
	}
	if after, err := os.ReadFile(source); err != nil || string(after) != string(before) {
		t.Fatal("original transcript changed")
	}
	// Later opens must keep AO's appended turns, even if the source disappears.
	withTurn := append(body, []byte("{\"type\":\"user\",\"text\":\"AO-ONLY-TURN\"}\n")...)
	if err := os.WriteFile(target, withTurn, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := New().RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(target); err != nil || string(after) != string(withTurn) {
		t.Fatal("existing copy lost AO's turn")
	}
}

func TestRelocateImportedSessionRejectsInvalidCopy(t *testing.T) {
	for _, body := range []string{"", "\n", "{torn\n", "null\n", `{"sessionId":"wrong","cwd":"/old"}` + "\n"} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source.jsonl")
			if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			r := ports.ImportedSessionRelocation{NativeSessionID: qwenTestID, ConfigDir: root, TranscriptPath: source, LaunchCWD: t.TempDir()}
			if err := New().RelocateImportedSession(context.Background(), r); err == nil {
				t.Fatal("invalid transcript must fail before publication")
			}
			if _, err := os.Stat(qwenChatPath(root, r.LaunchCWD, qwenTestID)); !os.IsNotExist(err) {
				t.Fatalf("invalid copy was published: %v", err)
			}
		})
	}
}

func TestRelocateImportedSessionFindsSource(t *testing.T) {
	root := t.TempDir()
	writeQwenSource(t, root, t.TempDir())
	r := ports.ImportedSessionRelocation{NativeSessionID: qwenTestID, ConfigDir: root, LaunchCWD: t.TempDir()}
	if err := New().RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(qwenChatPath(root, r.LaunchCWD, qwenTestID)); err != nil {
		t.Fatal(err)
	}
}

func TestRelocateImportedSessionValidation(t *testing.T) {
	root := t.TempDir()
	for _, r := range []ports.ImportedSessionRelocation{
		{NativeSessionID: "../bad", ConfigDir: root, LaunchCWD: t.TempDir()},
		{NativeSessionID: qwenTestID, LaunchCWD: t.TempDir()},
		{NativeSessionID: qwenTestID, ConfigDir: root, LaunchCWD: "relative"},
		{NativeSessionID: qwenTestID, ConfigDir: root, LaunchCWD: t.TempDir()},
	} {
		if err := New().RelocateImportedSession(context.Background(), r); err == nil {
			t.Fatalf("expected validation/missing-source error for %#v", r)
		}
	}
}

func TestLocateTranscriptRejectsBadID(t *testing.T) {
	ref := ports.NativeSessionRef{NativeSessionID: "not-a-uuid", ConfigDir: t.TempDir()}
	if _, _, err := New().LocateTranscript(context.Background(), ref); err == nil {
		t.Fatal("expected invalid-id error")
	}
}
