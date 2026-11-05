package project

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func newTestCloneProgressTracker() *cloneProgressTracker {
	return newCloneProgressTracker(func() time.Time { return time.Unix(0, 0).UTC() })
}

func TestCloneProgressTrackerParsesGitProgressLines(t *testing.T) {
	t.Parallel()

	mib := float64(1 << 20)
	kib := float64(1 << 10)
	tests := []struct {
		name  string
		lines []string
		want  CloneProgress
	}{
		{
			name:  "cloning into",
			lines: []string{"Cloning into '/tmp/dest/.ao-clone-123'..."},
			want:  CloneProgress{Phase: clonePhaseConnecting},
		},
		{
			name:  "enumerating done without percent",
			lines: []string{"remote: Enumerating objects: 1234, done.        "},
			want:  CloneProgress{Phase: clonePhaseEnumerating, Percent: 100},
		},
		{
			name:  "counting partial with aligned percent",
			lines: []string{"remote: Counting objects:  45% (12/27)"},
			want:  CloneProgress{Phase: clonePhaseCounting, Percent: 45, ItemsDone: 12, ItemsTotal: 27},
		},
		{
			name:  "counting done with size and rate",
			lines: []string{"remote: Counting objects: 100% (2748/2748), 240.00 KiB | 44.00 MiB/s, done."},
			want: CloneProgress{
				Phase: clonePhaseCounting, Percent: 100, ItemsDone: 2748, ItemsTotal: 2748,
				TransferredBytes: uint64(240 * kib), SpeedBytesPerSecond: 44 * mib,
			},
		},
		{
			name:  "compressing done",
			lines: []string{"remote: Compressing objects: 100% (600/600), done."},
			want:  CloneProgress{Phase: clonePhaseCompressing, Percent: 100, ItemsDone: 600, ItemsTotal: 600},
		},
		{
			name:  "receiving partial",
			lines: []string{"Receiving objects:  45% (1234/2748), 2.00 MiB | 1.50 MiB/s"},
			want: CloneProgress{
				Phase: clonePhaseReceiving, Percent: 45, ItemsDone: 1234, ItemsTotal: 2748,
				TransferredBytes: 2 << 20, SpeedBytesPerSecond: 1.5 * mib,
			},
		},
		{
			name:  "receiving done",
			lines: []string{"Receiving objects: 100% (2748/2748), 5.00 MiB | 2.00 MiB/s, done."},
			want: CloneProgress{
				Phase: clonePhaseReceiving, Percent: 100, ItemsDone: 2748, ItemsTotal: 2748,
				TransferredBytes: 5 << 20, SpeedBytesPerSecond: 2 * mib,
			},
		},
		{
			name:  "resolving deltas keeps completed-with note out of the stats",
			lines: []string{"Resolving deltas: 100% (1500/1500), completed with 100 local objects."},
			want:  CloneProgress{Phase: clonePhaseResolving, Percent: 100, ItemsDone: 1500, ItemsTotal: 1500},
		},
		{
			name:  "updating files",
			lines: []string{"Updating files:  67% (335/500)"},
			want:  CloneProgress{Phase: clonePhaseUpdating, Percent: 67, ItemsDone: 335, ItemsTotal: 500},
		},
		{
			name: "totals, banners, and errors are ignored",
			lines: []string{
				"Receiving objects:  45% (1234/2748)",
				"remote: Total 2748 (delta 0), reused 0 (delta 0), pack-reused 0",
				"fatal: could not read from remote repository",
			},
			want: CloneProgress{Phase: clonePhaseReceiving, Percent: 45, ItemsDone: 1234, ItemsTotal: 2748},
		},
		{
			name: "sizes advance across updates within one phase",
			lines: []string{
				"Receiving objects:  20% (550/2748), 1.00 MiB | 1.00 MiB/s",
				"Receiving objects:  40% (1099/2748), 2.00 MiB | 1.00 MiB/s",
			},
			want: CloneProgress{
				Phase: clonePhaseReceiving, Percent: 40, ItemsDone: 1099, ItemsTotal: 2748,
				TransferredBytes: 2 << 20, SpeedBytesPerSecond: mib,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tracker := newTestCloneProgressTracker()
			for _, line := range tt.lines {
				tracker.applyLine(line)
			}
			got := tracker.snapshot()
			if got.Phase != tt.want.Phase || got.Percent != tt.want.Percent ||
				got.ItemsDone != tt.want.ItemsDone || got.ItemsTotal != tt.want.ItemsTotal ||
				got.TransferredBytes != tt.want.TransferredBytes || got.SpeedBytesPerSecond != tt.want.SpeedBytesPerSecond {
				t.Fatalf("snapshot = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCloneProgressTrackerSplitsCarriageReturnsAcrossWrites(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	_, _ = tracker.Write([]byte("remote: Counting objects:  4"))
	_, _ = tracker.Write([]byte("5% (12/27)\rremote: Counting objects: 100% (27/27), done.\n"))

	got := tracker.snapshot()
	if got.Phase != clonePhaseCounting || got.Percent != 100 || got.ItemsDone != 27 || got.ItemsTotal != 27 {
		t.Fatalf("snapshot = %+v, want counting 100%% (27/27)", got)
	}
}

func TestCloneProgressTrackerResetsCountersOnPhaseChange(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	tracker.applyLine("remote: Counting objects: 100% (2748/2748), 240.00 KiB | 44.00 MiB/s, done.")
	tracker.applyLine("Receiving objects:   1% (27/2748)")

	got := tracker.snapshot()
	if got.Phase != clonePhaseReceiving || got.Percent != 1 {
		t.Fatalf("snapshot = %+v, want receiving at 1 percent", got)
	}
	if got.TransferredBytes != 0 || got.SpeedBytesPerSecond != 0 {
		t.Fatalf("counting-phase byte stats leaked into receiving: %+v", got)
	}
}

func TestCloneProgressTrackerFinishReportsFinalizing(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	tracker.applyLine("Receiving objects: 100% (2748/2748), 5.00 MiB | 2.00 MiB/s, done.")
	tracker.finish()

	got := tracker.snapshot()
	if got.Phase != clonePhaseFinalizing || got.Percent != 100 || got.SpeedBytesPerSecond != 0 {
		t.Fatalf("snapshot = %+v, want finalizing at 100 percent with no live rate", got)
	}
	if got.TransferredBytes == 0 {
		t.Fatal("finish dropped the transferred byte count")
	}
}

func TestCloneProgressRegistryLifecycle(t *testing.T) {
	t.Parallel()

	registry := newCloneProgressRegistry()
	if _, ok := registry.get("/repos/example"); ok {
		t.Fatal("empty registry returned a snapshot")
	}

	tracker := newTestCloneProgressTracker()
	registry.add("/repos/example", tracker)
	if _, ok := registry.get("/repos/example"); !ok {
		t.Fatal("registered clone not found")
	}

	// A different tracker for the same key must not remove the live entry.
	other := newTestCloneProgressTracker()
	registry.remove("/repos/example", other)
	if _, ok := registry.get("/repos/example"); !ok {
		t.Fatal("remove with a different tracker dropped the live entry")
	}

	registry.remove("/repos/example", tracker)
	if _, ok := registry.get("/repos/example"); ok {
		t.Fatal("remove kept the entry alive")
	}
}

func TestCloneProgressRegistryNilIsInert(t *testing.T) {
	t.Parallel()

	var registry *cloneProgressRegistry
	if _, ok := registry.get("/repos/example"); ok {
		t.Fatal("nil registry returned a snapshot")
	}
	registry.add("/repos/example", newTestCloneProgressTracker())
	registry.remove("/repos/example", newTestCloneProgressTracker())
}

func TestCloneProgressKeyDerivesThePrepareTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		remoteURL   string
		parent      string
		want        string
		wantInvalid bool
	}{
		{name: "https", remoteURL: "https://github.com/acme/repo.git", parent: filepath.Join(t.TempDir(), "dest"), want: "repo"},
		{name: "trailing slash", remoteURL: "https://github.com/acme/repo.git", parent: filepath.Join(t.TempDir(), "dest") + string(os.PathSeparator), want: "repo"},
		{name: "invalid url", remoteURL: "not a url", parent: t.TempDir(), wantInvalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, err := cloneProgressKey(tt.remoteURL, tt.parent)
			if tt.wantInvalid {
				var apiErr *apierr.Error
				if !errors.As(err, &apiErr) {
					t.Fatalf("cloneProgressKey(%q) error = %v, want *apierr.Error", tt.remoteURL, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("cloneProgressKey(%q): %v", tt.remoteURL, err)
			}
			if filepath.Base(key) != tt.want {
				t.Fatalf("cloneProgressKey(%q) = %q, want base %q", tt.remoteURL, key, tt.want)
			}
			if !strings.HasPrefix(key, filepath.Clean(tt.parent)) {
				t.Fatalf("cloneProgressKey(%q) = %q, want it under %q", tt.remoteURL, key, tt.parent)
			}
		})
	}
}

// TestPrepareCloneExposesProgressWhileCloning runs a real clone of a repo with
// incompressible content and polls the progress registry from another
// goroutine, mirroring the daemon's HTTP polling clients.
func TestPrepareCloneExposesProgressWhileCloning(t *testing.T) {
	source := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", source}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "AO Test")
	blob := make([]byte, 40<<20)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "blob.bin"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "blob.bin")
	run("commit", "-m", "initial")

	m := New(nil)
	parent := t.TempDir()
	remote := "file://" + filepath.ToSlash(source)

	type poll struct {
		snapshot CloneProgress
		found    bool
	}
	var pollsMu sync.Mutex
	polls := []poll{}
	done := make(chan struct{})
	var poller sync.WaitGroup
	poller.Add(1)
	go func() {
		defer poller.Done()
		query := CloneProgressQuery{RemoteURL: remote, DestinationParent: parent}
		for {
			select {
			case <-done:
				return
			default:
			}
			snapshot, err := m.CloneProgress(context.Background(), query)
			pollsMu.Lock()
			polls = append(polls, poll{snapshot: snapshot, found: err == nil})
			pollsMu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()

	prepared, err := m.PrepareClone(context.Background(), CloneInput{RemoteURL: remote, DestinationParent: parent})
	close(done)
	poller.Wait()
	pollsMu.Lock()
	defer pollsMu.Unlock()
	if err != nil {
		t.Fatalf("PrepareClone: %v", err)
	}
	if prepared.Path == "" || prepared.PreparationID == "" {
		t.Fatalf("PrepareClone returned %+v", prepared)
	}

	sawSnapshot := false
	sawTransfer := false
	validPhases := map[string]bool{
		clonePhaseConnecting: true, clonePhaseEnumerating: true, clonePhaseCounting: true,
		clonePhaseCompressing: true, clonePhaseReceiving: true, clonePhaseResolving: true,
		clonePhaseUpdating: true, clonePhaseFinalizing: true,
	}
	for _, p := range polls {
		if !p.found {
			continue
		}
		sawSnapshot = true
		if !validPhases[p.snapshot.Phase] {
			t.Fatalf("poll observed invalid phase %q", p.snapshot.Phase)
		}
		if p.snapshot.Phase == clonePhaseReceiving || p.snapshot.Phase == clonePhaseResolving ||
			p.snapshot.Phase == clonePhaseUpdating || p.snapshot.Phase == clonePhaseFinalizing {
			sawTransfer = true
		}
	}
	if !sawSnapshot {
		t.Fatal("no poll observed the running clone")
	}
	if !sawTransfer {
		t.Fatal("no poll observed a transfer phase (receiving, resolving, updating, or finalizing)")
	}

	// After the preparation returns the entry is gone, so late pollers see a
	// clean not-found instead of a stale snapshot.
	if _, ok := m.cloneProgress.get(prepared.Path); ok {
		t.Fatal("progress entry survived PrepareClone")
	}
	_, err = m.CloneProgress(context.Background(), CloneProgressQuery{RemoteURL: remote, DestinationParent: parent})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "CLONE_PROGRESS_NOT_FOUND" {
		t.Fatalf("CloneProgress after completion = %v, want CLONE_PROGRESS_NOT_FOUND", err)
	}
}

func TestCloneProgressTrackerLogTailCollapsesTaskUpdates(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	tracker.applyLine("Cloning into '/tmp/dest/.ao-clone-123'...")
	for percent := 1; percent <= 3; percent++ {
		tracker.applyLine(fmt.Sprintf("Receiving objects:  %d%% (%d/2748)", percent, percent*27))
	}
	tracker.applyLine("warning: remote banner kept verbatim")

	got := tracker.snapshot()
	want := []string{
		"Cloning into '/tmp/dest/.ao-clone-123'...",
		"Receiving objects:  3% (81/2748)",
		"warning: remote banner kept verbatim",
	}
	if !slices.Equal(got.Log, want) {
		t.Fatalf("log tail = %q, want %q", got.Log, want)
	}
}

func TestCloneProgressTrackerLogTailIsBounded(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	for i := 0; i < cloneLogTailMaxLines+10; i++ {
		tracker.applyLine(fmt.Sprintf("remote: banner-%02d", i))
	}

	got := tracker.snapshot()
	if len(got.Log) != cloneLogTailMaxLines {
		t.Fatalf("log tail length = %d, want %d", len(got.Log), cloneLogTailMaxLines)
	}
	if got.Log[0] != "remote: banner-10" || got.Log[len(got.Log)-1] != fmt.Sprintf("remote: banner-%02d", cloneLogTailMaxLines+9) {
		t.Fatalf("log tail kept the wrong window: first=%q last=%q", got.Log[0], got.Log[len(got.Log)-1])
	}
}

func TestCloneProgressTrackerLogTailTruncatesLongLines(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	long := strings.Repeat("x", cloneLogTailMaxRunes+50)
	tracker.applyLine(long)

	got := tracker.snapshot()
	if len(got.Log) != 1 || len([]rune(got.Log[0])) != cloneLogTailMaxRunes {
		t.Fatalf("log tail line length = %d, want %d", len([]rune(got.Log[0])), cloneLogTailMaxRunes)
	}
}

func TestCloneProgressTrackerLogTailSnapshotIsACopy(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	tracker.applyLine("Receiving objects: 100% (2748/2748), done.")
	snap := tracker.snapshot()
	snap.Log[0] = "mutated"

	if got := tracker.snapshot(); got.Log[0] == "mutated" {
		t.Fatal("snapshot exposed the tracker's internal log tail")
	}
}

func TestCloneProgressTrackerParsesAChunkLargerThanTheLineBound(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	banner := strings.Repeat("remote: long server banner\n", maxCloneProgressLine/20)
	_, _ = tracker.Write([]byte(banner + "Receiving objects:  45% (1234/2748), 1.00 MiB | 2.00 MiB/s\r"))

	snap := tracker.snapshot()
	if snap.Phase != clonePhaseReceiving || snap.Percent != 45 {
		t.Fatalf("snapshot = %+v, want receiving at 45%%", snap)
	}
}

func TestCloneProgressTrackerDropsAnUnterminatedOverlongLine(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	_, _ = tracker.Write([]byte(strings.Repeat("x", maxCloneProgressLine+1)))
	if got := len(tracker.buf); got != 0 {
		t.Fatalf("buffered %d bytes of an overlong line", got)
	}
}

func TestCloneProgressTrackerLogTailRemovesURLCredentials(t *testing.T) {
	t.Parallel()

	tracker := newTestCloneProgressTracker()
	padding := strings.Repeat("x", cloneLogTailMaxRunes-30)
	_, _ = tracker.Write([]byte("fatal: unable to access 'https://user:s3cret@example.com/repo.git/'\n"))
	// The secret straddles the truncation point, so it must go before the cut.
	_, _ = tracker.Write([]byte("warning: " + padding + " https://user:s3cret-that-runs-past-the-cut@example.com/repo.git\n"))
	_, _ = tracker.Write([]byte("warning: ssh://git@example.com/repo.git moved\n"))

	log := strings.Join(tracker.snapshot().Log, "\n")
	if strings.Contains(log, "s3cret") || strings.Contains(log, "git@") {
		t.Fatalf("log tail kept credentials: %q", log)
	}
	if !strings.Contains(log, "https://***@example.com/repo.git") {
		t.Fatalf("log tail lost the URL: %q", log)
	}
}

// A failed attempt must stay readable through the backoff: the entry keeps
// answering polls, its numbers are reset, and the reason for the retry stays
// in the log.
func TestPrepareCloneKeepsProgressAndLogAcrossARetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git is a shell script")
	}
	bin := t.TempDir()
	attempts := filepath.Join(bin, "attempts")
	script := `#!/bin/sh
if [ ! -e "` + attempts + `" ]; then
	: > "` + attempts + `"
	printf 'Receiving objects:  45%% (1234/2748), 1.00 MiB | 2.00 MiB/s\r' >&2
	echo "fatal: unable to access 'x': Could not resolve host: example.com" >&2
	exit 128
fi
for destination; do :; done
mkdir -p "$destination/.git"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := New(nil)
	parent := t.TempDir()
	remote := "https://example.com/repo.git"
	prepared := make(chan error, 1)
	go func() {
		_, err := m.PrepareClone(context.Background(), CloneInput{RemoteURL: remote, DestinationParent: parent})
		prepared <- err
	}()

	query := CloneProgressQuery{RemoteURL: remote, DestinationParent: parent}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(attempts); err == nil {
			progress, err := m.CloneProgress(context.Background(), query)
			if err == nil && strings.Contains(strings.Join(progress.Log, "\n"), "Could not resolve host") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the failed attempt's log never appeared")
		}
		time.Sleep(time.Millisecond)
	}
	// Backoff is one second, so these polls land between the two attempts.
	time.Sleep(100 * time.Millisecond)
	progress, err := m.CloneProgress(context.Background(), query)
	if err != nil {
		t.Fatalf("poll during the retry backoff: %v", err)
	}
	if progress.Phase != clonePhaseConnecting || progress.Percent != 0 || progress.SpeedBytesPerSecond != 0 {
		t.Fatalf("snapshot during backoff = %+v, want reset to connecting", progress)
	}
	if !strings.Contains(strings.Join(progress.Log, "\n"), "Could not resolve host") {
		t.Fatalf("log during backoff = %q, want the failure", progress.Log)
	}
	if err := <-prepared; err != nil {
		t.Fatalf("PrepareClone: %v", err)
	}
}
