package project

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Clone phases surfaced by GET /api/v1/projects/clone/progress. They follow
// the progress sections `git clone --progress` writes on stderr.
const (
	clonePhaseConnecting  = "connecting"
	clonePhaseEnumerating = "enumerating"
	clonePhaseCounting    = "counting"
	clonePhaseCompressing = "compressing"
	clonePhaseReceiving   = "receiving"
	clonePhaseResolving   = "resolving"
	clonePhaseUpdating    = "updating"
	clonePhaseFinalizing  = "finalizing"
)

// cloneProgressRegistry holds the latest snapshot of every in-flight clone,
// keyed by the destination path the checkout will land at. Entries live only
// for the duration of the git process: prepareClone registers one before it
// starts git and removes it (deferred) when the preparation returns.
type cloneProgressRegistry struct {
	mu      sync.RWMutex
	entries map[string]*cloneProgressTracker
}

func newCloneProgressRegistry() *cloneProgressRegistry {
	return &cloneProgressRegistry{entries: map[string]*cloneProgressTracker{}}
}

func (r *cloneProgressRegistry) add(key string, tracker *cloneProgressTracker) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[key] = tracker
}

// remove drops the entry only when it is still the registered tracker, so a
// second clone that lost the race to the same destination cannot unregister
// the first one's progress.
func (r *cloneProgressRegistry) remove(key string, tracker *cloneProgressTracker) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[key] == tracker {
		delete(r.entries, key)
	}
}

func (r *cloneProgressRegistry) get(key string) (CloneProgress, bool) {
	if r == nil {
		return CloneProgress{}, false
	}
	r.mu.RLock()
	tracker, ok := r.entries[key]
	r.mu.RUnlock()
	if !ok {
		return CloneProgress{}, false
	}
	return tracker.snapshot(), true
}

// cloneProgressTracker consumes `git clone --progress` stderr and keeps the
// latest parsed snapshot. git rewrites progress lines in place by separating
// updates with carriage returns, so the tracker buffers partial writes and
// treats both \r and \n as line terminators.
type cloneProgressTracker struct {
	mu   sync.Mutex
	buf  []byte
	now  func() time.Time
	snap CloneProgress
}

// maxCloneProgressLine bounds the unparsed buffer. Progress lines are short;
// anything this long without a terminator is not progress output.
const maxCloneProgressLine = 8 << 10

func newCloneProgressTracker(now func() time.Time) *cloneProgressTracker {
	if now == nil {
		now = time.Now
	}
	return &cloneProgressTracker{now: now, snap: CloneProgress{Phase: clonePhaseConnecting, StartedAt: now(), UpdatedAt: now()}}
}

// Write implements io.Writer for cmd.Stderr.
func (t *cloneProgressTracker) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > maxCloneProgressLine {
		t.buf = t.buf[:0]
	}
	for {
		idx := bytes.IndexAny(t.buf, "\r\n")
		if idx < 0 {
			break
		}
		line := t.buf[:idx]
		t.buf = t.buf[idx+1:]
		t.applyLine(string(line))
	}
	return len(p), nil
}

// finish records that git exited and the preparation is completing the
// checkout locally (marker write and rename).
func (t *cloneProgressTracker) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snap.Phase = clonePhaseFinalizing
	t.snap.Percent = 100
	t.snap.SpeedBytesPerSecond = 0
	t.snap.UpdatedAt = t.now()
}

func (t *cloneProgressTracker) snapshot() CloneProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snap
}

var cloneProgressTask = regexp.MustCompile(`^(?:remote:\s*)?(Enumerating objects|Counting objects|Compressing objects|Receiving objects|Resolving deltas|Updating files):\s*(.*)$`)

var cloneProgressPercent = regexp.MustCompile(`^(\d+)% \((\d+)/(\d+)\)`)

// cloneProgressStat matches git's humanised sizes and rates, e.g. "240.00 KiB"
// and "44.00 MiB/s". Percent groups like "(1234/2748)" never carry a unit and
// so never match.
var cloneProgressStat = regexp.MustCompile(`(\d+(?:\.\d+)?)\s?(bytes|B|KiB|MiB|GiB|TiB|KB|MB|GB)(/s)?`)

var cloneProgressPhases = map[string]string{
	"Enumerating objects": clonePhaseEnumerating,
	"Counting objects":    clonePhaseCounting,
	"Compressing objects": clonePhaseCompressing,
	"Receiving objects":   clonePhaseReceiving,
	"Resolving deltas":    clonePhaseResolving,
	"Updating files":      clonePhaseUpdating,
}

// applyLine folds one carriage-return- or newline-terminated stderr chunk into
// the snapshot. Unrecognised lines (errors, remote banners, totals) are
// ignored: the prepare call itself reports failures.
func (t *cloneProgressTracker) applyLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if strings.HasPrefix(line, "Cloning into ") {
		t.setPhase(clonePhaseConnecting)
		return
	}
	match := cloneProgressTask.FindStringSubmatch(line)
	if match == nil {
		return
	}
	phase := cloneProgressPhases[match[1]]
	rest := strings.TrimSpace(match[2])
	if t.snap.Phase != phase {
		t.setPhase(phase)
	}
	if percent := cloneProgressPercent.FindStringSubmatch(rest); percent != nil {
		t.snap.Percent = parseCloneProgressFloat(percent[1])
		t.snap.ItemsDone = parseCloneProgressUint(percent[2])
		t.snap.ItemsTotal = parseCloneProgressUint(percent[3])
	}
	// git reports cumulative sizes and the current rate on each update; take
	// the first of each this line carries (a phase change resets both above).
	sawTransferred, sawSpeed := false, false
	for _, stat := range cloneProgressStat.FindAllStringSubmatch(rest, -1) {
		value := parseCloneProgressFloat(stat[1]) * cloneProgressUnit(stat[2])
		if stat[3] == "/s" {
			if !sawSpeed {
				t.snap.SpeedBytesPerSecond = value
				sawSpeed = true
			}
		} else if !sawTransferred {
			t.snap.TransferredBytes = uint64(value)
			sawTransferred = true
		}
	}
	if strings.Contains(rest, "done") && cloneProgressPercent.FindStringSubmatch(rest) == nil {
		t.snap.Percent = 100
	}
	t.snap.UpdatedAt = t.now()
}

// setPhase resets the per-phase counters so a completed phase's numbers never
// leak into the next one's indeterminate start.
func (t *cloneProgressTracker) setPhase(phase string) {
	now := t.now()
	t.snap.Phase = phase
	t.snap.Percent = 0
	t.snap.ItemsDone = 0
	t.snap.ItemsTotal = 0
	t.snap.TransferredBytes = 0
	t.snap.SpeedBytesPerSecond = 0
	t.snap.UpdatedAt = now
}

func cloneProgressUnit(unit string) float64 {
	switch unit {
	case "KiB", "KB":
		return 1 << 10
	case "MiB", "MB":
		return 1 << 20
	case "GiB", "GB":
		return 1 << 30
	case "TiB":
		return 1 << 40
	default: // "bytes" and "B"
		return 1
	}
}

func parseCloneProgressUint(raw string) uint64 {
	value, _ := strconv.ParseUint(raw, 10, 64)
	return value
}

func parseCloneProgressFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(raw, 64)
	return value
}
