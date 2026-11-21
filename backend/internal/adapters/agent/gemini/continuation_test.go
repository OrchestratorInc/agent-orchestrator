package gemini

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGeminiWindowsProjectOwnershipRetainsExistingSlug(t *testing.T) {
	cwd := `C:\Users\Alice\Project`
	for _, registry := range []bool{true, false} {
		root := t.TempDir()
		dir := filepath.Join(root, "tmp", "existing-slug")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if registry {
			body, err := json.Marshal(map[string]any{"projects": map[string]string{strings.ToLower(cwd): "existing-slug"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "projects.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(dir, ".project_root"), []byte(strings.ToLower(cwd)), 0o600); err != nil {
			t.Fatal(err)
		}
		provider, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		got := geminiProjectDirectory(provider, cwd, "new-hash", true)
		_ = provider.Close()
		if got != "existing-slug" {
			t.Fatalf("Windows project (registry=%v) = %q", registry, got)
		}
	}
}

func TestImportedGeminiCopyKeepsIDAndSurvivesRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires Unix symlink permissions")
	}
	root, launch := t.TempDir(), t.TempDir()
	link := filepath.Join(t.TempDir(), "launch-link")
	if err := os.Symlink(launch, link); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "tmp", "original", "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(dir, "session-2026-11-02T08-00-11111111.jsonl")
	body := `{"sessionId":"11111111-1111-4111-8111-111111111111","projectHash":"old"}
{"id":"u1","type":"user","content":"synthetic code word"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	relocation := ports.ImportedSessionRelocation{NativeSessionID: id, ConfigDir: root, TranscriptPath: path, LaunchCWD: link}
	p := New()
	if err := p.RelocateImportedSession(context.Background(), relocation); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(launch)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(resolved))
	hash := hex.EncodeToString(sum[:])
	copied := filepath.Join(root, "tmp", hash, "chats", filepath.Base(path))
	data, err := os.ReadFile(copied)
	if err != nil || !strings.Contains(string(data), hash) || !strings.Contains(string(data), id) {
		t.Fatalf("copy = %s, %v", data, err)
	}
	if info, err := os.Stat(copied); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy permissions = %v, %v", info, err)
	}
	if err := os.WriteFile(copied, append(data, []byte("{\"id\":\"ao-turn\"}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), relocation); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(copied); !strings.Contains(string(data), "ao-turn") {
		t.Fatal("retry overwrote AO turns")
	}
	if data, _ := os.ReadFile(path); string(data) != body {
		t.Fatal("original provider history changed")
	}
	if available, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root}); err != nil || available != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %s, %v", available, err)
	}
	relocation.NativeSessionID = "wrong-id"
	if err := p.RelocateImportedSession(context.Background(), relocation); err == nil {
		t.Fatal("mismatched source id was copied")
	}
}
