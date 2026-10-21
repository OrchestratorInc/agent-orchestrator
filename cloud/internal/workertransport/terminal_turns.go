package workertransport

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// Some terminal turn ends fire no hook. Codex runs its Stop hook only after a
// turn whose model request succeeded, so a turn that ends on an error (usage
// limit, an unsupported model, expired sign-in) reports nothing; the Codex
// AO pins (0.147) also has no Interrupt hook. Claude Code fires neither Stop
// nor StopFailure when the user presses Esc to interrupt a turn. Without an
// idle signal the session stayed "working": a switch to Chat waited forever
// for a Stop that never came, and queued messages waited out the control
// plane's five minute staleness valve. Both harnesses persist the turn end to
// their native transcript (a Codex task_complete with an error or a
// turn_aborted, Claude's "[Request interrupted by user]" marker), so the worker
// follows the terminal agent's transcript and reports that turn end as idle,
// the same proof a Stop hook writes.
const (
	transcriptScanEvery   = 5
	maxTranscriptReadSize = 4 << 20
)

// terminalTurnPollInterval is a variable so tests can poll quickly.
var terminalTurnPollInterval = 2 * time.Second

// activityPublisher is optional so alternate worker controls keep the
// existing transport contract. The Cloud client implements it.
type activityPublisher interface {
	PublishActivity(context.Context, worker.ActivityEvent) error
}

// environmentValue returns the value an exec.Cmd environment sets for key.
// Later entries win, as they do for the process.
func environmentValue(environment []string, key string) string {
	found := ""
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			found = value
		}
	}
	return strings.TrimSpace(found)
}

// turnEnd is a terminal turn that ended without a hook, and when it ended.
type turnEnd struct {
	message string
	at      time.Time
	// interrupted is true for a turn the user interrupted (Esc), false for
	// one that ended on an error.
	interrupted bool
}

// turnRecordKind classifies one transcript record.
type turnRecordKind int

const (
	turnRecordOther turnRecordKind = iota
	// turnRecordStart is a later turn starting: a turn end before it in the
	// same read is history.
	turnRecordStart
	turnRecordEnd
)

// transcriptTail follows the newest transcript the terminal agent writes under
// root. Only bytes written after the watcher started count: history from an
// earlier process (a resumed conversation, or an agent that outlived its
// worker) must not mark the new turn idle.
type transcriptTail struct {
	root    string
	started time.Time
	// maxDepth bounds how deep under root a transcript sits; 0 is unbounded.
	maxDepth int
	// owns reports whether a transcript is the terminal agent's own. known is
	// false while that cannot be decided yet (its first record is unwritten).
	owns func(path, name string) (owned, known bool)
	// record classifies one complete transcript line.
	record func(line []byte) (turnEnd, turnRecordKind)
	path   string
	// offsets is how far each transcript has been read. It starts at every
	// transcript's size when the watcher started, so a resumed conversation is
	// read from where it was, and one created later from its start.
	offsets map[string]int64
	partial []byte
	// skipping drops the rest of a record too large to read, up to its end.
	skipping bool
	// owned remembers which transcripts are the terminal agent's, so a rescan
	// opens each at most once.
	owned map[string]bool
	scans int
	// refresh, if set, runs before each poll and may retarget the tail.
	refresh func()
	// notBefore drops turn ends older than the latest retarget: a
	// conversation the agent switched back to holds turns from before.
	notBefore time.Time
}

// newTranscriptTail records where every existing transcript ends. Call it
// before the agent starts, so nothing it writes counts as history.
func newTranscriptTail(root string, maxDepth int, started time.Time,
	owns func(path, name string) (bool, bool),
	record func([]byte) (turnEnd, turnRecordKind),
) *transcriptTail {
	t := &transcriptTail{
		root: root, started: started, maxDepth: maxDepth, owns: owns, record: record,
		offsets: make(map[string]int64),
		owned:   make(map[string]bool),
	}
	t.walk(func(path string, info os.FileInfo) {
		t.offsets[path] = info.Size()
	})
	return t
}

// walk visits every transcript file under root within maxDepth.
func (t *transcriptTail) walk(visit func(path string, info os.FileInfo)) {
	_ = filepath.WalkDir(t.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if t.maxDepth > 0 && path != t.root && t.depth(path) >= t.maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			visit(path, info)
		}
		return nil
	})
}

func (t *transcriptTail) depth(path string) int {
	rel, err := filepath.Rel(t.root, path)
	if err != nil {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// poll returns each turn end since the previous poll that no later turn start
// in the same read superseded.
func (t *transcriptTail) poll() []turnEnd {
	if t.refresh != nil {
		t.refresh()
	}
	if t.path == "" || t.scans%transcriptScanEvery == 0 {
		t.follow(t.newestOwnedTranscript())
	}
	t.scans++
	if t.path == "" {
		return nil
	}
	file, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	offset := t.offsets[t.path]
	if info.Size() < offset {
		// Truncated or replaced: start over from its current end.
		t.offsets[t.path], t.partial = info.Size(), nil
		return nil
	}
	if info.Size() == offset {
		return nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	chunk, err := io.ReadAll(io.LimitReader(file, maxTranscriptReadSize))
	if err != nil {
		return nil
	}
	t.offsets[t.path] = offset + int64(len(chunk))
	data := append(t.partial, chunk...)
	t.partial = nil
	if t.skipping {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			return nil
		}
		data, t.skipping = data[end+1:], false
	}
	last := bytes.LastIndexByte(data, '\n')
	if rest := data[last+1:]; len(rest) > maxTranscriptReadSize {
		// An oversized record (a large tool output) is never a turn end.
		t.skipping = true
	} else if len(rest) > 0 {
		// Keep an unfinished record for the next poll.
		t.partial = append([]byte(nil), rest...)
	}
	var ends []turnEnd
	for _, line := range bytes.Split(data[:last+1], []byte{'\n'}) {
		if len(line) == 0 || len(line) > maxTranscriptReadSize {
			continue
		}
		end, kind := t.record(line)
		switch kind {
		case turnRecordStart:
			ends = ends[:0]
		case turnRecordEnd:
			if !end.at.Before(t.notBefore) {
				ends = append(ends, end)
			}
		}
	}
	return ends
}

// retarget forgets the followed transcript and which transcripts are the
// agent's, after the rule deciding that changed at since. The transcript left
// behind is no longer read, so nothing written to it later counts.
func (t *transcriptTail) retarget(since time.Time) {
	t.path, t.partial, t.skipping = "", nil, false
	t.owned = make(map[string]bool)
	t.notBefore = since
}

// follow switches to path, read from where the watcher left it: a transcript
// that existed when the watcher started from its size then, a new one from
// its start.
func (t *transcriptTail) follow(path string) {
	if path == "" || path == t.path {
		return
	}
	t.path, t.partial, t.skipping = path, nil, false
}

// newestOwnedTranscript is the most recently written transcript of the
// terminal agent.
func (t *transcriptTail) newestOwnedTranscript() string {
	var newest string
	var newestMod time.Time
	t.walk(func(path string, info os.FileInfo) {
		// A transcript untouched since the watcher started holds nothing new.
		// The slack covers file times the kernel stamps from a coarser clock.
		if info.ModTime().Before(t.started.Add(-time.Second)) || info.ModTime().Before(newestMod) {
			return
		}
		if path != t.path && !t.ownedTranscript(path, filepath.Base(path)) {
			return
		}
		newest, newestMod = path, info.ModTime()
	})
	return newest
}

func (t *transcriptTail) ownedTranscript(path, name string) bool {
	if owned, known := t.owned[path]; known {
		return owned
	}
	owned, known := t.owns(path, name)
	if known {
		t.owned[path] = owned
	}
	return owned
}

// noteAgentTerminalInput records terminal input to the agent. Input that
// cannot change what the agent does (see mayChangeAgentTerminal) is not
// newer than a stop proof: a second Esc after an interrupt must not hold a
// Chat switch. Callers hold s.mu.
func (s *Supervisor) noteAgentTerminalInput(data []byte) {
	now := time.Now()
	if mayChangeAgentTerminal(data) {
		s.lastTUIInputAt = now
	}
	if mayStartTurn(data) {
		s.lastTUISubmitAt = now
	}
}

// watchTerminalTurnEnds runs for the life of the terminal agent's process,
// reading the transcript every interval (terminalTurnPollInterval, read by the
// caller when the terminal opens). checkpointSocket is the agent's
// AO_CHECKPOINT_SOCKET, poked as its Stop hook would be, so the files the turn
// edited are checkpointed.
func (s *Supervisor) watchTerminalTurnEnds(ctx context.Context, harness string, tail *transcriptTail, checkpointSocket string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ends := tail.poll()
		if len(ends) == 0 {
			continue
		}
		worker.PokeCheckpoint(ctx, checkpointSocket)
		s.reportTerminalTurnEnd(ctx, harness, ends[len(ends)-1])
	}
}

func (s *Supervisor) reportTerminalTurnEnd(ctx context.Context, harness string, end turnEnd) {
	event, what := "stop-failure", "ended on an error"
	if end.interrupted {
		event, what = "interrupt", "was interrupted"
	}
	if s.DataDir != "" {
		// Stamped when the turn ended: input typed after that, before this
		// poll saw it, is newer than the proof and keeps a handoff waiting.
		if err := worker.RecordTUIStop(s.DataDir, end.at); err != nil {
			s.Logger.Warn("record terminal turn end as terminal stop", "harness", harness, "error", err)
		}
	}
	s.mu.Lock()
	submitAt := s.lastTUISubmitAt
	s.mu.Unlock()
	// Transcript times have millisecond precision; input inside the turn end's
	// own millisecond (the Enter that rejected a permission prompt) came first.
	if submitAt.Sub(end.at) > time.Millisecond {
		// A prompt submitted after the turn ended, before this poll saw it,
		// started a turn this idle would wrongly end.
		s.Logger.Info("terminal turn "+what+" before the latest submitted input; leaving the agent working",
			"harness", harness, "error_message", end.message)
		return
	}
	s.Logger.Info("terminal turn "+what+"; marking the terminal agent idle",
		"harness", harness, "error_message", end.message)
	publisher, ok := s.Control.(activityPublisher)
	if !ok {
		return
	}
	if err := publisher.PublishActivity(ctx, worker.ActivityEvent{
		Harness:         harness,
		Event:           event,
		State:           contract.ActivityIdle,
		SourceInterface: "tui",
	}); err != nil && ctx.Err() == nil {
		s.Logger.Warn("publish terminal turn end activity", "harness", harness, "error", err)
	}
}
