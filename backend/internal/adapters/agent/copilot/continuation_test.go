package copilot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const continuationTestID = "41fc8602-ec68-477c-9e91-f49a531ed5c0"

func TestNativeSessionConfigDirUsesLaunchHome(t *testing.T) {
	daemonRoot := t.TempDir()
	t.Setenv("COPILOT_HOME", daemonRoot)
	home := t.TempDir()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "daemon override", want: daemonRoot},
		{name: "launch override", env: map[string]string{"COPILOT_HOME": filepath.Join(home, "selected")}, want: filepath.Join(home, "selected")},
		{name: "explicit default", env: map[string]string{"COPILOT_HOME": "", "HOME": home}, want: filepath.Join(home, ".copilot")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New().NativeSessionConfigDir(context.Background(), tc.env)
			if err != nil || got != tc.want {
				t.Fatalf("native home = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestLocateNativeTranscriptAndProbeAvailability(t *testing.T) {
	root := t.TempDir()
	ref := ports.NativeSessionRef{NativeSessionID: continuationTestID, ConfigDir: root}
	plugin := New()
	availability, err := plugin.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: continuationTestID})
	if err != nil || availability != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("missing root = %q, %v", availability, err)
	}
	availability, err = plugin.ProbeNativeSession(context.Background(), ref)
	if err != nil || availability != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("missing native history = %q, %v", availability, err)
	}
	path := filepath.Join(root, "session-state", continuationTestID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user.message","data":{"content":"synthetic context"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok, err := plugin.LocateTranscript(context.Background(), ref)
	if err != nil || !ok || got != path {
		t.Fatalf("native path = %q, %v, %v", got, ok, err)
	}
	availability, err = plugin.ProbeNativeSession(context.Background(), ref)
	if err != nil || availability != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("persisted native history = %q, %v", availability, err)
	}
	for _, id := range []string{"../" + continuationTestID, "not-a-uuid"} {
		ref.NativeSessionID = id
		if _, ok, err := plugin.LocateTranscript(context.Background(), ref); err == nil || ok {
			t.Fatalf("invalid native id accepted: %q", id)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plugin.NativeSessionConfigDir(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled config resolution = %v", err)
	}
	if _, _, err := plugin.LocateTranscript(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled probe = %v", err)
	}
}

func TestLocateNativeTranscriptDoesNotFollowOutsideSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "session-state", continuationTestID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "events.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, ok, _ := New().LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: continuationTestID, ConfigDir: root}); ok {
		t.Fatal("native transcript escaped the selected Copilot home")
	}
}
