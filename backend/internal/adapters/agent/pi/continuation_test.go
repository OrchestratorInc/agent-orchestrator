package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const testPiSession = "01a11ac8-11e4-7c7b-84ab-e32998fa4fb1"

func writePiSession(t *testing.T, configDir, folder, id string) string {
	t.Helper()
	dir := filepath.Join(configDir, "sessions", folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-08T09-11-21-828Z_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"`+id+`","cwd":"/work/repo"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeSessionConfigDirHonorsEnv(t *testing.T) {
	p := New()
	dir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"PI_CODING_AGENT_DIR": "/custom/pi"})
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/custom/pi" {
		t.Fatalf("config dir = %q", dir)
	}
	home, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"HOME": "/home/u"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/home/u", ".pi", "agent"); home != want {
		t.Fatalf("default config dir = %q, want %q", home, want)
	}
}

func TestLocateTranscriptFindsSessionAcrossCwdFolders(t *testing.T) {
	config := t.TempDir()
	want := writePiSession(t, config, "--work-repo--", testPiSession)
	p := New()
	got, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: testPiSession, ConfigDir: config})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Fatalf("located %q ok=%v, want %q", got, ok, want)
	}
}

func TestProbeNativeSessionReportsAvailability(t *testing.T) {
	config := t.TempDir()
	writePiSession(t, config, "--work-repo--", testPiSession)
	p := New()

	available, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: testPiSession, ConfigDir: config})
	if err != nil {
		t.Fatal(err)
	}
	if available != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("availability = %q, want available", available)
	}

	missing := "01a00000-0000-7000-8000-000000000000"
	gone, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: missing, ConfigDir: config})
	if err != nil {
		t.Fatal(err)
	}
	if gone != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", gone)
	}

	unknown, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: testPiSession})
	if err != nil {
		t.Fatal(err)
	}
	if unknown != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("availability = %q, want unknown", unknown)
	}
}

func TestLocateTranscriptRejectsInvalidID(t *testing.T) {
	p := New()
	if _, _, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: "not-a-uuid", ConfigDir: t.TempDir()}); err == nil {
		t.Fatal("expected invalid native session id error")
	}
}
