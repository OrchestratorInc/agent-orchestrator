package minimaxcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
)

func versionProbeFixture(t *testing.T) string {
	t.Helper()
	previousSpec, previousProbe := binarySpec, runVersionProbe
	t.Cleanup(func() { binarySpec, runVersionProbe = previousSpec, previousProbe })
	binary := filepath.Join(t.TempDir(), "mcode.exe")
	if err := os.WriteFile(binary, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	binarySpec = binaryutil.BinarySpec{Label: "mcode", Names: []string{binary}, WinNames: []string{binary}}
	return binary
}

func TestVersionMismatchIsCachedUntilInvalidation(t *testing.T) {
	binary := versionProbeFixture(t)
	version := "0.6.4"
	calls := 0
	runVersionProbe = func(context.Context, string) ([]byte, error) {
		calls++
		return []byte(version), nil
	}
	plugin := New()
	_, mismatch := plugin.ResolveBinary(context.Background())
	if mismatch == nil || calls != 1 {
		t.Fatalf("initial mismatch = %v, probe calls = %d", mismatch, calls)
	}
	version = "0.6.5"
	for range 3 {
		if got, err := plugin.ResolveBinary(context.Background()); got != "" || !errors.Is(err, mismatch) {
			t.Fatalf("cached mismatch = (%q, %v), want original mismatch", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plugin.ResolveBinary(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached mismatch hid caller cancellation: %v", err)
	}
	if calls != 1 {
		t.Fatalf("cached mismatch repeated the version probe %d times", calls)
	}
	plugin.InvalidateBinaryResolution()
	if got, err := plugin.ResolveBinary(context.Background()); got != binary || err != nil || calls != 2 {
		t.Fatalf("resolution after invalidation = (%q, %v), probe calls = %d", got, err, calls)
	}
}

func TestTransientVersionProbeFailureIsNotCached(t *testing.T) {
	binary := versionProbeFixture(t)
	calls := 0
	runVersionProbe = func(context.Context, string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, context.DeadlineExceeded
		}
		return []byte("0.6.5\n"), nil
	}
	plugin := New()
	if _, err := plugin.ResolveBinary(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("version probe failure = %v, want original timeout", err)
	}
	if got, err := plugin.ResolveBinary(context.Background()); got != binary || err != nil || calls != 2 {
		t.Fatalf("retry after transient failure = (%q, %v), probe calls = %d", got, err, calls)
	}
}

func TestSupportsVersionAcceptsQualifiedAndNewerReleases(t *testing.T) {
	for version, want := range map[string]bool{
		"0.6.5": true, "0.6.5\n": true, "0.6.9": true, "0.7.0": true, "1.0.0": true, "v0.6.10": true,
		"0.6.4": false, "0.5.9": false, "": false, "0.6": false, "0.6.9-beta.1": false, "mcode 0.6.9": false,
	} {
		if got := SupportsVersion(version); got != want {
			t.Errorf("SupportsVersion(%q) = %t, want %t", version, got, want)
		}
	}
}
