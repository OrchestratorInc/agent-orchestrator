package daemon

import (
	"io"
	"os"
	"sync"
)

const (
	// daemonLogName is the file under AO_DATA_DIR that keeps the daemon's own
	// log. The desktop app reads the daemon's stderr into a small in-memory
	// buffer, so without this file a failure in the field leaves nothing to
	// diagnose once the app restarts.
	daemonLogName = "daemon.log"
	// maxDaemonLogBytes caps daemon.log before it rotates to daemon.log.1, so
	// the log never holds more than twice this on disk.
	maxDaemonLogBytes int64 = 16 << 20
)

// rotatingLogFile appends to path and, when the next write would exceed maxBytes,
// moves the current file to path.1 and starts a fresh one. Keeping one
// previous file preserves the lines leading up to a failure even when a
// rotation lands right after it.
type rotatingLogFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

func openRotatingLogFile(path string, maxBytes int64) (*rotatingLogFile, error) {
	l := &rotatingLogFile{path: path, maxBytes: maxBytes}
	if err := l.open(false); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *rotatingLogFile) open(truncate bool) error {
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if truncate {
		flags = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}
	f, err := os.OpenFile(l.path, flags, 0o600) //nolint:gosec // path is rooted in AO's own data dir
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.file, l.size = f, info.Size()
	return nil
}

func (l *rotatingLogFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil && l.size > 0 && l.size+int64(len(p)) > l.maxBytes {
		_ = l.file.Close()
		l.file = nil
		// A rename can fail when another process still holds the file open on
		// Windows. Truncating then keeps the cap instead of retrying every write.
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			if err := l.open(true); err != nil {
				return 0, err
			}
		}
	}
	if l.file == nil {
		if err := l.open(false); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

// teeLogWriter writes every record to stderr and the log file. A failing file
// never drops the stderr copy, and a dead stderr pipe never drops the file copy.
type teeLogWriter struct {
	stderr io.Writer
	file   io.Writer
}

func (w teeLogWriter) Write(p []byte) (int, error) {
	_, stderrErr := w.stderr.Write(p)
	_, fileErr := w.file.Write(p)
	if stderrErr != nil && fileErr != nil {
		return 0, stderrErr
	}
	return len(p), nil
}
