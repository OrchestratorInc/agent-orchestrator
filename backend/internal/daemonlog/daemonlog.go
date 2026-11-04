// Package daemonlog keeps the daemon's own size-capped log file under the AO
// state dir. The desktop app ships without a console, so stderr alone leaves
// nothing to inspect after a failure; the daemon writes every record to
// <state dir>/logs/daemon.log itself, in every launch mode (desktop, AO_KEEP_DAEMON,
// `ao daemon`), and still mirrors it to stderr for development.
package daemonlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
)

const (
	// DirName is the log directory under the AO state dir, shared with the
	// desktop app's main.log and updater.jsonl.
	DirName = "logs"
	// FileName is the daemon's current log file.
	FileName = "daemon.log"
	// MaxBytes caps one log file before it rotates.
	MaxBytes = 10 << 20
	// Backups is how many rotated files (daemon.log.1 ... daemon.log.N) to keep.
	Backups = 3
)

// Dir is the log directory for a daemon whose run-file is runFilePath. The
// run-file sits at the root of the AO state dir (~/.ao, or ~/.ao/dev for a dev
// build), which the desktop app also uses for its own logs. AO_DATA_DIR is not
// used: the desktop app sets it on every daemon it spawns.
func Dir(runFilePath string) string {
	return filepath.Join(filepath.Dir(runFilePath), DirName)
}

// File is an append-only log file that rotates to numbered backups once it
// would exceed its size cap. It is safe for concurrent use. Another process
// (a predecessor daemon still draining during a takeover) may share the path:
// a write that finds the path rotated underneath it reopens the new file.
type File struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	f        *os.File
	info     os.FileInfo
	size     int64
	crash    bool
}

// Open opens path for appending, creating it and its directory as needed. The
// file is owner-only (0600); a symlink at path is refused.
func Open(path string, maxBytes int64, backups int) (*File, error) {
	if maxBytes <= 0 || backups < 1 {
		return nil, fmt.Errorf("daemon log: invalid limits %d bytes, %d backups", maxBytes, backups)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("daemon log: create dir: %w", err)
	}
	l := &File{path: path, maxBytes: maxBytes, backups: backups}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

// Path is the current log file's path.
func (l *File) Path() string { return l.path }

// SetCrashOutput makes the Go runtime also write fatal panics and runtime
// errors to this file, which is otherwise lost when nothing reads stderr. It
// follows the file across rotations.
func (l *File) SetCrashOutput() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return os.ErrClosed
	}
	if err := debug.SetCrashOutput(l.f, debug.CrashOptions{}); err != nil {
		return err
	}
	l.crash = true
	return nil
}

// Write appends p, rotating first when p would push the file past its cap.
// Each call is one log record, so a record is never split across files.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	if current, err := os.Stat(l.path); err != nil || !os.SameFile(current, l.info) {
		// Another writer rotated the file, or it was deleted: follow the path.
		if err := l.reopen(); err != nil {
			return 0, err
		}
	}
	if l.size > 0 && l.size+int64(len(p)) > l.maxBytes {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// Close closes the file. Later writes fail with os.ErrClosed.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	if l.crash {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		l.crash = false
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func (l *File) open() error {
	if info, err := os.Lstat(l.path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("daemon log: %s is a symlink", l.path)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path is AO's own log file under its state dir
	if err != nil {
		return fmt.Errorf("daemon log: open: %w", err)
	}
	// A file created by an older build or another tool may be wider than 0600.
	_ = f.Chmod(0o600)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("daemon log: stat: %w", err)
	}
	l.f, l.info, l.size = f, info, info.Size()
	if l.crash {
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	return nil
}

func (l *File) reopen() error {
	_ = l.f.Close()
	l.f = nil
	return l.open()
}

// rotate shifts daemon.log.(N-1) -> .N ... daemon.log -> .1 and starts a new
// file. When the rename fails (Windows refuses to rename a file another process
// holds open) it truncates instead, so the cap holds either way.
func (l *File) rotate() error {
	_ = l.f.Close()
	l.f = nil
	_ = os.Remove(l.backup(l.backups))
	for i := l.backups - 1; i >= 1; i-- {
		_ = os.Rename(l.backup(i), l.backup(i+1))
	}
	if err := os.Rename(l.path, l.backup(1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		if err := l.open(); err != nil {
			return err
		}
		if err := l.f.Truncate(0); err != nil {
			return fmt.Errorf("daemon log: truncate: %w", err)
		}
		l.size = 0
		return nil
	}
	return l.open()
}

func (l *File) backup(i int) string { return l.path + "." + strconv.Itoa(i) }

// Tee writes each record to the file (redacted) and then to the mirror. A
// failing mirror never stops the file write: a daemon whose supervisor died
// gets EPIPE on stderr but must keep logging to disk.
func Tee(file, mirror io.Writer) io.Writer {
	return tee{file: file, mirror: mirror}
}

type tee struct {
	file   io.Writer
	mirror io.Writer
}

func (t tee) Write(p []byte) (int, error) {
	_, _ = t.file.Write(redact(p))
	if t.mirror != nil {
		_, _ = t.mirror.Write(p)
	}
	return len(p), nil
}
