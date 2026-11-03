package fx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFXNativeTranscriptLookup(t *testing.T) {
	root := t.TempDir()
	ref := ports.NativeSessionRef{NativeSessionID: "synthetic521", ConfigDir: root}
	p := New()
	if got, err := p.ProbeNativeSession(context.Background(), ref); err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("missing = %q, %v", got, err)
	}
	for _, rel := range []string{
		filepath.Join("sessions", ref.NativeSessionID, "events.jsonl"),
		filepath.Join("sessions", "v2", ref.NativeSessionID, "log.jsonl"),
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok, err := p.LocateTranscript(context.Background(), ref)
		if err != nil || !ok || got != path {
			t.Fatalf("locate = %q, %v, %v", got, ok, err)
		}
	}
	if got, err := p.ProbeNativeSession(context.Background(), ref); err != nil || got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("present = %q, %v", got, err)
	}
	ref.NativeSessionID = "../escape"
	if _, _, err := p.LocateTranscript(context.Background(), ref); err == nil {
		t.Fatal("accepted path traversal")
	}
}

func TestFXNativeProbeUnknownAndCancellation(t *testing.T) {
	p := New()
	if got, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{}); err != nil || got != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("no home = %q, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := p.LocateTranscript(ctx, ports.NativeSessionRef{}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestFXNativeConfigDirHonorsHomeOverride(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "FX_HOME set", env: map[string]string{"HOME": home, "FX_HOME": " " + override + " "}, want: override},
		{name: "FX_HOME unset", env: map[string]string{"HOME": home}, want: filepath.Join(home, ".fx")},
		{name: "FX_HOME blank", env: map[string]string{"HOME": home, "FX_HOME": " "}, want: filepath.Join(home, ".fx")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New().NativeSessionConfigDir(context.Background(), tc.env)
			if err != nil || got != tc.want {
				t.Fatalf("config dir = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
