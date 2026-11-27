package deepseekharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// A live DeepSeek Harness session log, read while the harness appends to it.
//
// DeepSeek keeps every session, a subagent's included, in an append-only log at
// <DSH_HOME>/sessions/<project>/<session id>/session.v4.jsonl, zstd-compressed
// by default as one checksummed frame per append batch. Committed lines are
// never rewritten, so a reader that remembers how far it got can pick up only
// what is new. SessionTail does that: it consumes whole frames (or whole lines
// of a plain log), never a frame the harness is still writing, and hands back
// each new event once. It only reads; it never takes the harness's lock.

const (
	// tailReadLimit bounds the stored bytes one Next reads; the rest waits for
	// the next call.
	tailReadLimit = 16 << 20
	// tailLineLimit bounds one decoded event line, as the import reader does.
	tailLineLimit = 16 << 20
	tailHeaderMax = 64 << 10
)

// errTailUnsupported is a log SessionTail cannot follow: another generation, a
// broken sequence, or a file that shrank.
var errTailUnsupported = errors.New("deepseek: session log cannot be followed")

// SessionHeader is the first line of a session log.
type SessionHeader struct {
	ID              string `json:"id"`
	CWD             string `json:"cwd"`
	ParentSession   string `json:"parentSession"`
	Origin          string `json:"origin"`
	IsSeeded        bool   `json:"isSeeded"`
	DelegationDepth int    `json:"delegationDepth"`
}

// Home is DeepSeek Harness's home for a launch environment: its DSH_HOME, the
// daemon's, or ~/.dsh.
func Home(env map[string]string) (string, error) {
	if home := strings.TrimSpace(env["DSH_HOME"]); home != "" {
		return home, nil
	}
	if home := strings.TrimSpace(os.Getenv("DSH_HOME")); home != "" {
		return home, nil
	}
	user, err := os.UserHomeDir()
	if err != nil || user == "" {
		return "", fmt.Errorf("deepseek: resolve home: %w", err)
	}
	return filepath.Join(user, ".dsh"), nil
}

// FindSessionLog returns the log of session id under home, or an error
// satisfying errors.Is(err, fs.ErrNotExist) while the harness has not written
// it yet (it creates the file on its first append). The session's project
// folder is found by its id, which is unique, rather than recomputed from a
// working directory the harness may have canonicalised differently.
func FindSessionLog(home, id string) (string, error) {
	if !importID.MatchString(id) {
		return "", fmt.Errorf("deepseek: invalid session id %q", id)
	}
	sessions := filepath.Join(home, "sessions")
	projects, err := os.ReadDir(sessions)
	if err != nil {
		return "", err
	}
	// A candidate that cannot be inspected is reported, not mistaken for a log
	// the harness has not written yet.
	var inspectErr error
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		dir := filepath.Join(sessions, project.Name(), id)
		for _, name := range []string{"session.v4.jsonl.zstd", "session.v4.jsonl"} {
			path := filepath.Join(dir, name)
			info, err := os.Lstat(path)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) && inspectErr == nil {
					inspectErr = err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("deepseek: session log %s is not a regular file", path)
			}
			return path, nil
		}
	}
	if inspectErr != nil {
		return "", inspectErr
	}
	return "", fmt.Errorf("deepseek: session %s: %w", id, fs.ErrNotExist)
}

// SessionTail follows one session log. It is not safe for concurrent use.
type SessionTail struct {
	path       string
	compressed bool
	// offset is how much of the file has been consumed: whole frames, or
	// whole lines of a plain log.
	offset int64
	// pending holds decoded bytes after the last complete line.
	pending []byte
	header  *SessionHeader
	nextSeq int64
	failed  error
}

// NewSessionTail follows the log at path from its start.
func NewSessionTail(path string) *SessionTail {
	return &SessionTail{path: path, compressed: strings.HasSuffix(path, ".zstd")}
}

// Header is the log's header, once Next has read it.
func (t *SessionTail) Header() (SessionHeader, bool) {
	if t.header == nil {
		return SessionHeader{}, false
	}
	return *t.header, true
}

// Next returns the events appended since the last call, in order. A batch is
// consumed only once all of it parses, so a call that fails leaves the log
// where it was. Cancellation is retryable; any other error is permanent: the
// log cannot be followed further.
func (t *SessionTail) Next(ctx context.Context) ([]NativeHistoryEvent, error) {
	if t.failed != nil {
		return nil, t.failed
	}
	for {
		before := t.offset
		events, err := t.next(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.failed = err
		}
		// A call that moved the cursor without producing an event (a header-only
		// frame, with the rest past the decoded budget) has more to read: an empty
		// result means nothing new, not "stopped early".
		if err != nil || len(events) > 0 || t.offset == before {
			return events, err
		}
	}
}

func (t *SessionTail) next(ctx context.Context) ([]NativeHistoryEvent, error) {
	info, err := os.Lstat(t.path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", errTailUnsupported)
	}
	if info.Size() < t.offset {
		return nil, fmt.Errorf("%w: the log shrank", errTailUnsupported)
	}
	if info.Size() == t.offset {
		return nil, nil
	}
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	want := min(info.Size()-t.offset, tailReadLimit)
	stored := make([]byte, want)
	n, err := f.ReadAt(stored, t.offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	stored = stored[:n]
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var decoded []byte
	var consumed int
	if t.compressed {
		decoded, consumed, err = decodeWholeFrames(stored)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errTailUnsupported, err)
		}
		if consumed == 0 {
			if int64(len(stored)) == tailReadLimit {
				return nil, fmt.Errorf("%w: a frame exceeds the read limit", errTailUnsupported)
			}
			return nil, nil
		}
	} else {
		last := bytes.LastIndexByte(stored, '\n')
		if last < 0 {
			if int64(len(stored)) == tailReadLimit {
				return nil, fmt.Errorf("%w: a line exceeds the read limit", errTailUnsupported)
			}
			return nil, nil
		}
		decoded, consumed = stored[:last+1], last+1
	}

	// Parse into a copy of the cursor and commit it only once the whole batch
	// parses, so nothing is skipped past.
	header, nextSeq := t.header, t.nextSeq
	buffer := make([]byte, 0, len(t.pending)+len(decoded))
	buffer = append(buffer, t.pending...)
	buffer = append(buffer, decoded...)
	var events []NativeHistoryEvent
	for {
		line, rest, found := bytes.Cut(buffer, []byte("\n"))
		if !found {
			break
		}
		buffer = rest
		if len(line) > tailLineLimit {
			return nil, fmt.Errorf("%w: an event exceeds the line limit", errTailUnsupported)
		}
		event, ok, err := parseTailLine(line, &header, &nextSeq)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	if len(buffer) > tailLineLimit {
		return nil, fmt.Errorf("%w: an event exceeds the line limit", errTailUnsupported)
	}
	t.offset += int64(consumed)
	t.header, t.nextSeq = header, nextSeq
	t.pending = append([]byte(nil), buffer...)
	return events, nil
}

// parseTailLine parses one line: the header while header is nil, then events
// in sequence. It advances header and nextSeq, which the caller commits.
func parseTailLine(line []byte, header **SessionHeader, nextSeq *int64) (NativeHistoryEvent, bool, error) {
	if len(line) == 0 {
		return NativeHistoryEvent{}, false, fmt.Errorf("%w: empty line", errTailUnsupported)
	}
	if *header == nil {
		if len(line) > tailHeaderMax {
			return NativeHistoryEvent{}, false, fmt.Errorf("%w: oversized header", errTailUnsupported)
		}
		var h struct {
			SessionHeader
			Type    string `json:"type"`
			Version int    `json:"version"`
		}
		if err := json.Unmarshal(line, &h); err != nil {
			return NativeHistoryEvent{}, false, fmt.Errorf("%w: header: %w", errTailUnsupported, err)
		}
		if h.Type != "session" || h.Version != 4 || !importID.MatchString(h.ID) {
			return NativeHistoryEvent{}, false, fmt.Errorf("%w: unsupported header", errTailUnsupported)
		}
		*header = &h.SessionHeader
		return NativeHistoryEvent{}, false, nil
	}
	var e importEvent
	if err := json.Unmarshal(line, &e); err != nil {
		return NativeHistoryEvent{}, false, fmt.Errorf("%w: event: %w", errTailUnsupported, err)
	}
	if e.Seq == nil || *e.Seq != *nextSeq || e.Type == "" {
		return NativeHistoryEvent{}, false, fmt.Errorf("%w: broken event sequence", errTailUnsupported)
	}
	*nextSeq++
	var at int64
	if e.Time != nil {
		at = *e.Time
	}
	return NativeHistoryEvent{Type: e.Type, Seq: *e.Seq, Time: at, Data: e.Data, SurfaceOp: e.SurfaceOp}, true, nil
}

// decodeWholeFrames decodes the whole zstd frames b starts with, and returns
// how many bytes of b they took. A frame cut short, as one the harness is still
// writing, is left for the next call, and so are frames past the decoded
// budget; only a single frame that cannot fit the budget on its own fails.
func decodeWholeFrames(b []byte) ([]byte, int, error) {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxImportDecoded))
	if err != nil {
		return nil, 0, err
	}
	defer decoder.Close()
	var decoded []byte
	consumed := 0
	for consumed < len(b) {
		n, err := frameLength(b[consumed:])
		if err != nil {
			return nil, 0, err
		}
		if n == 0 {
			break
		}
		frame, err := decoder.DecodeAll(b[consumed:consumed+n], nil)
		if err != nil {
			return nil, 0, err
		}
		if len(decoded)+len(frame) > maxImportDecoded {
			if consumed == 0 {
				return nil, 0, errors.New("a frame exceeds the decoded budget")
			}
			break
		}
		decoded = append(decoded, frame...)
		consumed += n
	}
	return decoded, consumed, nil
}

// frameLength is the length of the zstd frame b starts with, or 0 when b holds
// only part of it.
func frameLength(b []byte) (int, error) {
	var h zstd.Header
	if err := h.Decode(b); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, nil
		}
		return 0, err
	}
	if h.Skippable {
		size := h.HeaderSize + int(h.SkippableSize)
		if len(b) < size {
			return 0, nil
		}
		return size, nil
	}
	offset := h.HeaderSize
	for {
		if len(b) < offset+3 {
			return 0, nil
		}
		block := uint32(b[offset]) | uint32(b[offset+1])<<8 | uint32(b[offset+2])<<16
		offset += 3
		last := block&1 == 1
		size := int(block >> 3)
		switch (block >> 1) & 3 {
		case 1: // RLE: one byte repeated size times.
			size = 1
		case 3:
			return 0, errors.New("reserved zstd block type")
		}
		offset += size
		if len(b) < offset {
			return 0, nil
		}
		if last {
			break
		}
	}
	if h.HasCheckSum {
		offset += 4
		if len(b) < offset {
			return 0, nil
		}
	}
	return offset, nil
}
