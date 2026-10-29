package kimi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const kimiTestSession = "session_11111111-2222-3333-4444-555555555555"

func writeKimiTranscript(t *testing.T, configDir, workdir, sessionID string) string {
	t.Helper()
	path := filepath.Join(configDir, "sessions", workdir, sessionID, "agents", "main", "wire.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKimiNativeSessionConfigDir(t *testing.T) {
	p := New()
	// An explicit KIMI_CODE_HOME wins over the default.
	got, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"KIMI_CODE_HOME": "/custom/kimi"})
	if err != nil || got != "/custom/kimi" {
		t.Fatalf("NativeSessionConfigDir = %q, %v", got, err)
	}
	// Without the env var it falls back to ~/.kimi-code under HOME.
	got, err = p.NativeSessionConfigDir(context.Background(), map[string]string{"HOME": "/home/alice"})
	if err != nil || got != filepath.Join("/home/alice", ".kimi-code") {
		t.Fatalf("default NativeSessionConfigDir = %q, %v", got, err)
	}
}

func TestKimiLocateTranscriptAndProbe(t *testing.T) {
	p := New()
	configDir := t.TempDir()
	want := writeKimiTranscript(t, configDir, "wd_repo_abc123def456", kimiTestSession)

	path, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: kimiTestSession, ConfigDir: configDir})
	if err != nil || !ok || path != want {
		t.Fatalf("LocateTranscript = %q, %v, %v", path, ok, err)
	}
	if avail, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: kimiTestSession, ConfigDir: configDir}); err != nil || avail != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("ProbeNativeSession = %q, %v", avail, err)
	}

	// A session with no local transcript is unavailable, not an error.
	missing := "session_99999999-8888-7777-6666-555555555555"
	if _, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: missing, ConfigDir: configDir}); err != nil || ok {
		t.Fatalf("LocateTranscript(missing) ok=%v err=%v", ok, err)
	}
	if avail, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: missing, ConfigDir: configDir}); err != nil || avail != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("ProbeNativeSession(missing) = %q, %v", avail, err)
	}
}

func TestKimiLocateTranscriptRejectsBadID(t *testing.T) {
	p := New()
	if _, _, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: "../escape", ConfigDir: t.TempDir()}); err == nil {
		t.Fatal("expected an invalid native session id to be rejected")
	}
}
