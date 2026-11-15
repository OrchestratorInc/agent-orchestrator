package daemon

import (
	"io"
	"os"
	"sync"
)

const (
	daemonLogName           = "daemon.log"
	maxDaemonLogBytes int64 = 16 << 20
)

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
	record := p
	if l.maxBytes > 0 && int64(len(record)) > l.maxBytes {
		record = append(record[:l.maxBytes-1:l.maxBytes-1], '\n')
	}
	if l.file != nil && l.size > 0 && l.size+int64(len(record)) > l.maxBytes {
		_ = l.file.Close()
		l.file = nil
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
	n, err := l.file.Write(record)
	l.size += int64(n)
	if err != nil {
		return min(n, len(p)), err
	}
	return len(p), nil
}

type deferredLogFile struct {
	mu   sync.Mutex
	file io.Writer
}

func (d *deferredLogFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	file := d.file
	d.mu.Unlock()
	if file == nil {
		return len(p), nil
	}
	return file.Write(p)
}

func (d *deferredLogFile) attach(file io.Writer) {
	d.mu.Lock()
	d.file = file
	d.mu.Unlock()
}

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
