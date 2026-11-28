//go:build !windows

package local

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type leftover struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// startLeftover runs a process group leader whose command line carries marker,
// standing in for a hub a killed daemon left behind. It is reaped as soon as
// it exits (as launchd reaps an orphaned hub), so its group empties.
func startLeftover(t *testing.T, marker string) leftover {
	t.Helper()
	// The trailing ":" keeps the shell (and marker in its command line) alive:
	// with "sleep 30" alone, sh execs sleep and the marker vanishes, so under
	// load the check could see a plain sleep and rightly leave it running.
	cmd := exec.Command("/bin/sh", "-c", "sleep 30; :", marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	l := leftover{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(l.done) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-l.done })
	return l
}

func exited(l leftover, within time.Duration) bool {
	select {
	case <-l.done:
		return true
	case <-time.After(within):
		return false
	}
}

func TestStaleHubIsStoppedOnStart(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	if err := os.MkdirAll(p.stateRoot(), 0o750); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(t.TempDir(), "expo-device-hub", "cli.mjs")
	leftover := startLeftover(t, entry)
	p.recordHubPID(leftover.cmd.Process.Pid)

	p.stopStaleHub(entry)

	if !exited(leftover, 10*time.Second) {
		t.Fatal("the hub a previous daemon left running is still alive")
	}
	if _, err := os.Stat(p.hubPIDPath()); !os.IsNotExist(err) {
		t.Fatalf("pid record left behind: %v", err)
	}
}

func TestRecycledHubPIDIsLeftAlone(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	if err := os.MkdirAll(p.stateRoot(), 0o750); err != nil {
		t.Fatal(err)
	}
	unrelated := startLeftover(t, "something-else")
	p.recordHubPID(unrelated.cmd.Process.Pid)

	p.stopStaleHub(filepath.Join(t.TempDir(), "expo-device-hub", "cli.mjs"))

	if exited(unrelated, 500*time.Millisecond) {
		t.Fatal("stopped a process that is not this data directory's hub")
	}
}

func TestHubRestartDelayBacksOffToThirtySeconds(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := hubRestartDelay(i); got != w {
			t.Fatalf("attempt %d waits %v, want %v", i, got, w)
		}
	}
}

func TestClosedProviderDoesNotReviveTheHub(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { p.reviveHub(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a closed provider kept trying to restart its hub")
	}
}

func TestStreamQualitiesOutliveTheDaemon(t *testing.T) {
	dir := t.TempDir()
	first := New(Config{DataDir: dir})
	first.mu.Lock()
	first.tuned = map[string]tunedStream{"UDID-1": {device: ports.DeviceInfo{ID: "UDID-1", Name: "iPhone", Platform: ports.DevicePlatformIOS}, quality: ports.DeviceQualityBalanced}}
	first.saveTunedLocked()
	first.mu.Unlock()

	restored := New(Config{DataDir: dir}).loadTuned()
	got, ok := restored["UDID-1"]
	if !ok || got.quality != ports.DeviceQualityBalanced || got.device.Platform != ports.DevicePlatformIOS {
		t.Fatalf("restored %+v (ok=%v), want UDID-1 at balanced", got, ok)
	}
}

func TestLiveRhythmNeedsSteadyFramesNotTheFirstOne(t *testing.T) {
	t0 := time.Unix(100, 0)
	at := func(ms ...int) []time.Time {
		out := make([]time.Time, len(ms))
		for i, m := range ms {
			out[i] = t0.Add(time.Duration(m) * time.Millisecond)
		}
		return out
	}
	// The helper's first frame, then the re-wiring gap: not live yet.
	if liveRhythm(at(0, 9800)) {
		t.Fatal("the frame before the gap read as a live stream")
	}
	// The idle floor (every 200 ms) once the capture runs.
	if !liveRhythm(at(0, 10000, 10200, 10400)) {
		t.Fatal("a stream at the idle floor did not read as live")
	}
	// Three frames spread over more than a second are not.
	if liveRhythm(at(0, 700, 1400, 2100)) {
		t.Fatal("frames 700 ms apart read as live")
	}
}
