package droid

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeSessionConfigDir(t *testing.T) {
	p := New()
	t.Run("home override wins", func(t *testing.T) {
		dir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{factoryHomeEnv: "/custom/home"})
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/custom/home", factoryDirName); dir != want {
			t.Fatalf("dir = %q, want %q", dir, want)
		}
	})
	t.Run("falls back to env HOME", func(t *testing.T) {
		dir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"HOME": "/home/dev"})
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/home/dev", factoryDirName); dir != want {
			t.Fatalf("dir = %q, want %q", dir, want)
		}
	})
}

func TestLocateTranscript(t *testing.T) {
	id := "7f0c09e2-ce0e-43f6-ad51-a2925a075e70"
	p := New()

	t.Run("found across cwd-dashed folders", func(t *testing.T) {
		config := t.TempDir()
		want := filepath.Join(config, "sessions", "-work-repo", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(want, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: config})
		if err != nil || !ok || got != want {
			t.Fatalf("path=%q ok=%v err=%v", got, ok, err)
		}
		avail, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: config})
		if err != nil || avail != ports.NativeSessionAvailabilityAvailable {
			t.Fatalf("availability=%v err=%v", avail, err)
		}
	})

	t.Run("flat location", func(t *testing.T) {
		config := t.TempDir()
		want := filepath.Join(config, "sessions", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(want, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: config})
		if err != nil || !ok || got != want {
			t.Fatalf("path=%q ok=%v err=%v", got, ok, err)
		}
	})

	t.Run("absent", func(t *testing.T) {
		config := t.TempDir()
		_, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: config})
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		avail, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: config})
		if err != nil || avail != ports.NativeSessionAvailabilityUnavailable {
			t.Fatalf("availability=%v err=%v", avail, err)
		}
	})

	t.Run("rejects non-uuid id", func(t *testing.T) {
		if _, _, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: "../escape", ConfigDir: t.TempDir()}); err == nil {
			t.Fatal("expected invalid native session id error")
		}
	})
}
