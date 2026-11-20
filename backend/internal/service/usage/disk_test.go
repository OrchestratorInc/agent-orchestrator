package usage

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var diskRoot = filepath.Join(string(filepath.Separator), "ao", "worktrees")

func worktreeSession(id, path string) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), Metadata: domain.SessionMetadata{WorkspacePath: path}}
}

// diskHarness is a DiskReader over a fake clock and a fake walk that records
// which worktrees it was asked to measure.
type diskHarness struct {
	*DiskReader
	store *memStore
	now   time.Time
	// onDisk is what the fake walk reports for each worktree.
	onDisk map[string]int64
	walks  []string
}

func newDiskHarness(recs ...domain.SessionRecord) *diskHarness {
	h := &diskHarness{store: &memStore{recs: recs}, now: time.Unix(1000, 0), onDisk: map[string]int64{}}
	h.DiskReader = NewDiskReader(DiskReaderDeps{
		Store: h.store, ManagedRoot: diskRoot, TTL: time.Minute,
		Now: func() time.Time { return h.now },
		Measure: func(_ context.Context, root string) int64 {
			h.walks = append(h.walks, root)
			return h.onDisk[root]
		},
	})
	return h
}

func (h *diskHarness) list(t *testing.T) domain.DiskReport {
	t.Helper()
	report, err := h.ListDisk(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestListDiskServesTheCacheAndNeverWalks(t *testing.T) {
	path := filepath.Join(diskRoot, "p", "s-a")
	h := newDiskHarness(worktreeSession("s-a", path))
	h.onDisk[path] = 4096

	report := h.list(t)
	if len(report.Sessions) != 0 || report.TotalBytes != 0 || report.Measuring != 1 {
		t.Fatalf("before any measurement: %+v, want no rows and one worktree measuring", report)
	}
	if len(h.walks) != 0 {
		t.Fatalf("ListDisk walked %v", h.walks)
	}
	if len(h.wake) != 1 {
		t.Fatal("an unmeasured worktree did not wake the background run")
	}
	<-h.wake

	h.refresh(context.Background())
	report = h.list(t)
	want := domain.SessionDisk{SessionID: "s-a", SizeBytes: 4096, MeasuredAt: time.Unix(1000, 0)}
	if len(report.Sessions) != 1 || report.Sessions[0] != want || report.TotalBytes != 4096 || report.Measuring != 0 {
		t.Fatalf("after measuring: %+v, want %+v", report, want)
	}
	if len(h.wake) != 0 {
		t.Fatal("a fresh size woke the background run")
	}

	// Inside the TTL the worktree is not walked again, whatever it now holds.
	h.onDisk[path] = 8192
	h.now = h.now.Add(59 * time.Second)
	h.refresh(context.Background())
	if report = h.list(t); report.Sessions[0].SizeBytes != 4096 || len(h.walks) != 1 {
		t.Fatalf("inside the TTL: %+v after walks %v", report, h.walks)
	}

	// At the TTL the old size is still served, and marked as being measured.
	h.now = h.now.Add(time.Second)
	report = h.list(t)
	if report.Sessions[0] != want || report.Measuring != 1 || len(h.walks) != 1 {
		t.Fatalf("at the TTL: %+v after walks %v, want the old size and one measuring", report, h.walks)
	}
	h.refresh(context.Background())
	report = h.list(t)
	want = domain.SessionDisk{SessionID: "s-a", SizeBytes: 8192, MeasuredAt: time.Unix(1060, 0)}
	if report.Sessions[0] != want || report.Measuring != 0 || len(h.walks) != 2 {
		t.Fatalf("after the TTL: %+v after walks %v, want %+v", report, h.walks, want)
	}
}

func TestListDiskMeasuresOnlyLiveManagedWorktrees(t *testing.T) {
	live := filepath.Join(diskRoot, "p", "s-live")
	terminated := worktreeSession("s-terminated", filepath.Join(diskRoot, "p", "s-terminated"))
	terminated.IsTerminated = true
	preparation := worktreeSession("s-preparation", filepath.Join(diskRoot, "p", "s-preparation"))
	preparation.IsTaskPreparation = true
	h := newDiskHarness(
		worktreeSession("s-live", live),
		terminated,
		preparation,
		worktreeSession("s-own-checkout", filepath.Join(string(filepath.Separator), "home", "me", "repo")),
		worktreeSession("s-escapes", filepath.Join(diskRoot, "..", "elsewhere")),
		worktreeSession("s-root", diskRoot),
		worktreeSession("s-none", ""),
	)
	h.onDisk[live] = 100

	if report := h.list(t); report.Measuring != 1 {
		t.Fatalf("measuring = %d, want only the live managed worktree", report.Measuring)
	}
	h.refresh(context.Background())
	if !slices.Equal(h.walks, []string{live}) {
		t.Fatalf("walked %v, want only %s", h.walks, live)
	}
	report := h.list(t)
	if len(report.Sessions) != 1 || report.Sessions[0].SessionID != "s-live" || report.TotalBytes != 100 {
		t.Fatalf("report = %+v, want only s-live", report)
	}
}

func TestListDiskCountsASharedWorktreeOnce(t *testing.T) {
	path := filepath.Join(diskRoot, "p", "shared")
	h := newDiskHarness(worktreeSession("s-a", path), worktreeSession("s-b", path))
	h.onDisk[path] = 700

	if report := h.list(t); report.Measuring != 1 {
		t.Fatalf("measuring = %d, want the shared worktree once", report.Measuring)
	}
	h.refresh(context.Background())
	report := h.list(t)
	if len(report.Sessions) != 2 || report.TotalBytes != 700 || len(h.walks) != 1 {
		t.Fatalf("report = %+v after walks %v, want two rows, one walk, a total of 700", report, h.walks)
	}
}

// A worktree missing from disk measures zero: it has no row, and it is not
// walked again on every request either.
func TestListDiskOmitsAWorktreeThatIsNotOnDisk(t *testing.T) {
	h := newDiskHarness(worktreeSession("s-gone", filepath.Join(diskRoot, "p", "s-gone")))
	h.list(t)
	h.refresh(context.Background())
	if report := h.list(t); len(report.Sessions) != 0 || report.Measuring != 0 {
		t.Fatalf("report = %+v, want no row and nothing measuring", report)
	}
}

func TestRefreshForgetsWorktreesNoLiveSessionHas(t *testing.T) {
	h := newDiskHarness(worktreeSession("s-a", filepath.Join(diskRoot, "p", "s-a")))
	h.refresh(context.Background())
	if len(h.sizes) != 1 {
		t.Fatalf("cache = %v, want the one worktree", h.sizes)
	}
	h.store.recs[0].IsTerminated = true
	h.refresh(context.Background())
	if len(h.sizes) != 0 {
		t.Fatalf("cache = %v, want it empty once the session is terminated", h.sizes)
	}
}

func TestRefreshKeepsTheOldSizeWhenCancelled(t *testing.T) {
	path := filepath.Join(diskRoot, "p", "s-a")
	h := newDiskHarness(worktreeSession("s-a", path))
	h.onDisk[path] = 4096
	h.refresh(context.Background())

	h.onDisk[path] = 1
	h.now = h.now.Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.refresh(ctx)
	want := domain.SessionDisk{SessionID: "s-a", SizeBytes: 4096, MeasuredAt: time.Unix(1000, 0)}
	if report := h.list(t); len(report.Sessions) != 1 || report.Sessions[0] != want {
		t.Fatalf("report = %+v, want the size from before the cancelled walk", report)
	}
}

func TestRunMeasuresAfterListDiskAsks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(diskRoot, "p", "s-a")
		h := newDiskHarness(worktreeSession("s-a", path))
		h.onDisk[path] = 4096
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go h.Run(ctx)

		if report := h.list(t); len(report.Sessions) != 0 {
			t.Fatalf("first request = %+v, want nothing measured yet", report)
		}
		synctest.Wait()
		if report := h.list(t); len(report.Sessions) != 1 || report.Sessions[0].SizeBytes != 4096 {
			t.Fatalf("after the background run = %+v, want s-a at 4096", report)
		}
	})
}

// The workspace adapters record resolved paths, so a root configured through
// a symlink must still match them.
func TestListDiskMatchesAManagedRootBehindASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege on Windows")
	}
	physical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatal(err)
	}
	reader := NewDiskReader(DiskReaderDeps{
		Store:       memStore{recs: []domain.SessionRecord{worktreeSession("s-a", filepath.Join(physical, "worktrees", "p", "s-a"))}},
		ManagedRoot: filepath.Join(link, "worktrees"),
		Measure:     func(context.Context, string) int64 { return 1 },
	})
	report, err := reader.ListDisk(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Measuring != 1 {
		t.Fatalf("measuring = %d, want the worktree under the resolved root", report.Measuring)
	}
}

func TestTreeBytesCountsAllocatedSpace(t *testing.T) {
	dir := t.TempDir()
	megabyte := make([]byte, 1<<20)
	if _, err := rand.Read(megabyte); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", filepath.Join("sub", "b")} {
		if err := os.WriteFile(filepath.Join(dir, name), megabyte, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		// A second name for a, and a symlink that would loop if followed.
		if err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "a-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dir, filepath.Join(dir, "sub", "loop")); err != nil {
			t.Fatal(err)
		}
	}

	if got := treeBytes(context.Background(), dir); got < 2<<20 || got >= 3<<20 {
		t.Fatalf("treeBytes = %d, want the two 1 MiB files with the hard link counted once", got)
	}
	if got := treeBytes(context.Background(), filepath.Join(dir, "missing")); got != 0 {
		t.Fatalf("missing root = %d, want 0", got)
	}
}
