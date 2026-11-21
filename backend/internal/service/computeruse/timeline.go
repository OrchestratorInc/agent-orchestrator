package computeruse

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// timelineFile is the per-session log of computer-use calls, kept beside the
// session's screenshots so the inspector can show each step with its image.
const timelineFile = "timeline.jsonl"

// keepTimelineEntries matches keepImages: older steps have lost their
// screenshot anyway.
const keepTimelineEntries = keepImages

// compactTimelineBytes is the size past which the log is rewritten to its
// newest keepTimelineEntries entries.
const compactTimelineBytes = 256 << 10

// maxTimelineError bounds the error text one entry stores.
const maxTimelineError = 500

var screenshotNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(png|jpg|gif|webp)$`)

// TimelineEntry is one computer-use call an AO session made.
type TimelineEntry struct {
	ID        string         `json:"id"`
	At        time.Time      `json:"at"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty" description:"The arguments the agent passed, without the ones AO sets itself."`
	OK        bool           `json:"ok"`
	Error     string         `json:"error,omitempty"`
	// RanOn names the other computer the call ran on; empty is this computer.
	RanOn string `json:"ranOn,omitempty" description:"The other computer the call ran on, chosen under Run computer use on; absent when it ran on this computer."`
	// Screenshots are file names in the session's screenshot folder, oldest
	// first; ones already pruned are left out on read.
	Screenshots []string `json:"screenshots" description:"Screenshot file names, served by GET /api/v1/computer-use/sessions/{sessionId}/screenshots/{name}."`
}

// timelineLog serializes writes to every session's log.
type timelineLog struct {
	mu sync.Mutex
}

// recordCall appends one call to its session's timeline. Best effort: a
// failure to log never fails the call.
func (s *Service) recordCall(scope, tool string, arguments map[string]any, started time.Time, images []Image, callErr error) {
	s.recordCallOn(scope, "", tool, arguments, started, images, callErr)
}

// recordCallOn is recordCall for a call that ran on another computer, named
// ranOn.
func (s *Service) recordCallOn(scope, ranOn, tool string, arguments map[string]any, started time.Time, images []Image, callErr error) {
	entry := TimelineEntry{
		ID:          timelineID(started),
		At:          started.UTC(),
		Tool:        tool,
		Arguments:   timelineArguments(arguments),
		OK:          callErr == nil,
		RanOn:       ranOn,
		Screenshots: []string{},
	}
	if callErr != nil {
		entry.Error = timelineError(callErr)
	}
	for _, image := range images {
		if name := filepath.Base(image.Path); screenshotNamePattern.MatchString(name) {
			entry.Screenshots = append(entry.Screenshots, name)
		}
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	s.timeline.mu.Lock()
	defer s.timeline.mu.Unlock()
	path := filepath.Join(scope, timelineFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, writeErr := file.Write(append(line, '\n'))
	info, statErr := file.Stat()
	_ = file.Close()
	if writeErr == nil && statErr == nil && info.Size() > compactTimelineBytes {
		compactTimeline(path)
	}
}

// compactTimeline rewrites the log to its newest lines: at most
// keepTimelineEntries, and no more than half of compactTimelineBytes so a log
// of long entries is not rewritten on every call.
func compactTimeline(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	start, size := len(lines), 0
	for start > 0 && len(lines)-start < keepTimelineEntries {
		size += len(lines[start-1]) + 1
		if size > compactTimelineBytes/2 && start < len(lines) {
			break
		}
		start--
	}
	lines = lines[start:]
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		return
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
	}
}

// Timeline returns sessionID's computer-use calls, oldest first. A session
// that never made one has an empty timeline.
func (s *Service) Timeline(_ context.Context, sessionID string) ([]TimelineEntry, error) {
	dir, err := s.images.existingScope(sessionID)
	if err != nil {
		return nil, err
	}
	entries := []TimelineEntry{}
	if dir == "" {
		return entries, nil
	}
	s.timeline.mu.Lock()
	file, err := os.Open(filepath.Join(dir, timelineFile))
	if err != nil {
		s.timeline.mu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return entries, nil
		}
		return nil, apierr.InternalCause("COMPUTER_USE_TIMELINE_UNREADABLE", "The computer-use timeline could not be read.", err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		var entry TimelineEntry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil && entry.ID != "" {
			entries = append(entries, entry)
		}
	}
	_ = file.Close()
	s.timeline.mu.Unlock()
	if len(entries) > keepTimelineEntries {
		entries = entries[len(entries)-keepTimelineEntries:]
	}
	present := screenshotNames(dir)
	for index := range entries {
		kept := []string{}
		for _, name := range entries[index].Screenshots {
			if present[name] {
				kept = append(kept, name)
			}
		}
		entries[index].Screenshots = kept
	}
	return entries, nil
}

// ScreenshotPath returns the file of one of sessionID's screenshots.
func (s *Service) ScreenshotPath(_ context.Context, sessionID, name string) (string, error) {
	if !screenshotNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return "", screenshotNotFound()
	}
	dir, err := s.images.existingScope(sessionID)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", screenshotNotFound()
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", screenshotNotFound()
	}
	return path, nil
}

func screenshotNotFound() error {
	return apierr.NotFound("COMPUTER_USE_SCREENSHOT_NOT_FOUND", "The screenshot is no longer available.")
}

// existingScope returns sessionID's screenshot folder without creating it,
// or "" when the session has none.
func (s imageStore) existingScope(sessionID string) (string, error) {
	name := strings.TrimSpace(sessionID)
	if !scopePattern.MatchString(name) {
		return "", apierr.Invalid("COMPUTER_USE_INVALID_SESSION", "sessionId may contain only letters, digits, '.', '_' and '-'.", nil)
	}
	dir := filepath.Join(s.root, name)
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", apierr.InternalCause("COMPUTER_USE_TIMELINE_UNREADABLE", "The computer-use timeline could not be read.", err)
	}
	if !info.IsDir() {
		return "", nil
	}
	return dir, nil
}

func screenshotNames(dir string) map[string]bool {
	names := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && screenshotNamePattern.MatchString(entry.Name()) {
			names[entry.Name()] = true
		}
	}
	return names
}

// timelineArguments copies the agent's arguments without the ones AO or the
// driver set for their own use.
func timelineArguments(arguments map[string]any) map[string]any {
	if len(arguments) == 0 {
		return nil
	}
	copied := maps.Clone(arguments)
	for _, key := range []string{"session", "screenshot_out_file", "debug_image_out"} {
		delete(copied, key)
	}
	return copied
}

func timelineError(err error) string {
	message := err.Error()
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		message = apiErr.Message
	}
	message = strings.TrimSpace(message)
	if len(message) > maxTimelineError {
		cut := maxTimelineError
		for cut > 0 && !utf8.RuneStart(message[cut]) {
			cut--
		}
		message = strings.TrimSpace(message[:cut]) + "…"
	}
	return message
}

func timelineID(at time.Time) string {
	nonce := make([]byte, 4)
	_, _ = rand.Read(nonce)
	return at.UTC().Format("20060102T150405.000Z") + "-" + hex.EncodeToString(nonce)
}
