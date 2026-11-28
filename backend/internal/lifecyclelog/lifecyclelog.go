// Package lifecyclelog keeps a small, local, crash-survivable record of why the
// daemon started and stopped: signals received, loss of the desktop supervisor
// link, shutdown requests, the error Run returned, and (through
// runtime/debug.SetCrashOutput) the traceback of a fatal panic or runtime
// fault. Before it existed the daemon logged only to stderr, which belongs to
// the desktop app and disappears with it, so an exit left no evidence.
//
// Files live in <root>/logs, where root is the run-file directory (normally
// ~/.ao, so ~/.ao/logs beside the desktop's logs): daemon-lifecycle.jsonl (rotated by size) and
// daemon-crash.log (Go crash output, rotated at startup). Nothing here is
// uploaded. Records hold fixed event names and scalar facts; free text such as
// an error message passes through Scrub, which removes URLs, credentials,
// long opaque tokens and long quoted strings, and folds the home directory to
// "~". Prompts and terminal output must never be passed in.
package lifecyclelog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// FileName is the JSONL lifecycle record inside <dataDir>/logs.
	FileName = "daemon-lifecycle.jsonl"
	// CrashFileName receives Go's fatal crash output.
	CrashFileName = "daemon-crash.log"
	// DefaultMaxBytes caps the live lifecycle file before it rotates.
	DefaultMaxBytes = 2 << 20
	// Keep is the number of rotated generations kept (.1 .. .Keep).
	Keep = 2
	// crashMaxBytes rotates the crash file at startup once it grows past this.
	crashMaxBytes = 1 << 20
	maxLineBytes  = 16 << 10
)

var (
	eventName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	fieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

	urlPattern        = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s<>"'` + "`" + `)]+`)
	authSchemePattern = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[^\s"']+`)
	secretPattern     = regexp.MustCompile(`(?i)\b([A-Za-z_]*(?:token|secret|password|passwd|apikey|api_key|api-key|authorization|cookie|credential)[A-Za-z_]*)(["']?\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`)
	doubleQuoted      = regexp.MustCompile(`"[^"\n]{40,}"`)
	singleQuoted      = regexp.MustCompile(`'[^'\n]{40,}'`)
	opaquePattern     = regexp.MustCompile(`[A-Za-z0-9+_=-]{32,}`)
)

// Scrub removes material that must never be persisted from free text and
// truncates it to limit bytes.
func Scrub(value, homeDir string, limit int) string {
	out := value
	if len(homeDir) > 1 {
		out = strings.ReplaceAll(out, homeDir, "~")
	}
	out = urlPattern.ReplaceAllString(out, "[url]")
	out = authSchemePattern.ReplaceAllString(out, "[credential]")
	out = secretPattern.ReplaceAllString(out, "$1$2[redacted]")
	out = doubleQuoted.ReplaceAllString(out, `"[text]"`)
	out = singleQuoted.ReplaceAllString(out, `'[text]'`)
	out = opaquePattern.ReplaceAllString(out, "[opaque]")
	if limit > 0 && len(out) > limit {
		out = out[:limit] + "…"
	}
	return out
}

// Log appends lifecycle records. A nil *Log is a valid no-op.
type Log struct {
	mu       sync.Mutex
	dir      string
	path     string
	maxBytes int64
	homeDir  string
	version  string
	pid      int
	now      func() time.Time
}

// Open prepares a log under <root>/logs. It never fails: an unwritable
// directory only means records are dropped, never that the daemon stops.
func Open(root, version string) *Log {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(root, "logs")
	return &Log{
		dir:      dir,
		path:     filepath.Join(dir, FileName),
		maxBytes: DefaultMaxBytes,
		homeDir:  home,
		version:  version,
		pid:      os.Getpid(),
		now:      time.Now,
	}
}

// Record appends one event. Field values must be scalars (string, bool,
// integer, float, nil); anything else is dropped. Strings are scrubbed.
func (l *Log) Record(event string, fields map[string]any) {
	l.write(event, fields, nil)
}

// RecordError appends one event with the scrubbed error text.
func (l *Log) RecordError(event string, err error, fields map[string]any) {
	l.write(event, fields, err)
}

func (l *Log) write(event string, fields map[string]any, err error) {
	if l == nil || !eventName.MatchString(event) {
		return
	}
	record := map[string]any{
		"ts":      l.now().UTC().Format(time.RFC3339Nano),
		"pid":     l.pid,
		"version": Scrub(l.version, l.homeDir, 64),
		"event":   event,
	}
	for key, value := range fields {
		if !fieldName.MatchString(key) {
			continue
		}
		if _, reserved := record[key]; reserved {
			continue
		}
		switch v := value.(type) {
		case nil, bool, int, int32, int64, uint32, uint64, float64:
			record[key] = v
		case string:
			record[key] = Scrub(v, l.homeDir, 200)
		case time.Duration:
			record[key] = v.Milliseconds()
		}
	}
	if err != nil {
		record["error"] = Scrub(err.Error(), l.homeDir, 400)
	}
	line, mErr := json.Marshal(record)
	if mErr != nil || len(line)+1 > maxLineBytes {
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return
	}
	if info, err := os.Lstat(l.path); err == nil {
		if !info.Mode().IsRegular() {
			return
		}
		if info.Size()+int64(len(line)) > l.maxBytes {
			rotate(l.path, Keep)
		}
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	// One write per record: the line reaches the kernel before Record returns,
	// so it survives the process dying immediately afterwards.
	_, _ = f.Write(line)
	_ = f.Close()
}

func rotate(path string, keep int) {
	_ = os.Remove(path + "." + strconv.Itoa(keep))
	for generation := keep - 1; generation >= 1; generation-- {
		_ = os.Rename(path+"."+strconv.Itoa(generation), path+"."+strconv.Itoa(generation+1))
	}
	_ = os.Rename(path, path+".1")
}

// InstallCrashOutput sends Go's fatal crash output (unrecovered panics,
// runtime faults such as SIGBUS/SIGSEGV, fatal errors) to daemon-crash.log in
// addition to stderr. Without it that text went only to the desktop app's
// pipe and was lost with it. The file is rotated once it exceeds 1 MiB.
func (l *Log) InstallCrashOutput() error {
	if l == nil {
		return errors.New("lifecycle log unavailable")
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(l.dir, CrashFileName)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", CrashFileName)
		}
		if info.Size() > crashMaxBytes {
			rotate(path, 1)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // SetCrashOutput keeps its own duplicate.
	// A header per daemon start lets a crash be matched to its pid.
	_, _ = fmt.Fprintf(f, "=== daemon pid %d version %s started %s\n", l.pid, Scrub(l.version, l.homeDir, 64), l.now().UTC().Format(time.RFC3339))
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

// ProcessContext reports, on Linux, the process's cgroup and OOM score
// adjustment: who it shares a systemd scope (and so an OOM kill or a scope
// stop) with. Empty elsewhere.
func ProcessContext() map[string]any {
	fields := map[string]any{"ppid": os.Getppid()}
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "0::"); ok {
				fields["cgroup"] = strings.TrimSpace(rest)
				break
			}
		}
	}
	if data, err := os.ReadFile("/proc/self/oom_score_adj"); err == nil {
		if adj, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			fields["oomScoreAdj"] = adj
		}
	}
	return fields
}
