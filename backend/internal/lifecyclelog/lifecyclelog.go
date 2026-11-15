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
	FileName        = "daemon-lifecycle.jsonl"
	CrashFileName   = "daemon-crash.log"
	DefaultMaxBytes = 2 << 20
	Keep            = 2
	crashMaxBytes   = 1 << 20
	maxLineBytes    = 16 << 10
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

func (l *Log) Record(event string, fields map[string]any) {
	l.write(event, fields, nil)
}

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
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "=== daemon pid %d version %s started %s\n", l.pid, Scrub(l.version, l.homeDir, 64), l.now().UTC().Format(time.RFC3339))
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

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
