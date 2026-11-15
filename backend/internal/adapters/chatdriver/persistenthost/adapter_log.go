package persistenthost

import (
	"os"
	"path/filepath"
	"sync"
)

const (
	adapterLogName           = "adapter.log"
	adapterLogMaxBytes int64 = 1 << 20
)

type adapterLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

func openAdapterLog(dir string, maxBytes int64) *adapterLog {
	l := &adapterLog{path: filepath.Join(dir, adapterLogName), maxBytes: maxBytes}
	l.open(false)
	return l
}

func (l *adapterLog) open(truncate bool) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if truncate {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(l.path, flags, 0o600)
	if err != nil {
		l.file = nil
		return
	}
	_ = file.Chmod(0o600)
	l.file = file
	l.size = 0
	if info, statErr := file.Stat(); statErr == nil {
		l.size = info.Size()
	}
}

func (l *adapterLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return len(p), nil
	}
	chunk := p
	if int64(len(chunk)) > l.maxBytes {
		chunk = chunk[int64(len(chunk))-l.maxBytes:]
	}
	if l.size+int64(len(chunk)) > l.maxBytes && l.size > 0 {
		l.rotate()
		if l.file == nil {
			return len(p), nil
		}
	}
	n, err := l.file.Write(chunk)
	l.size += int64(n)
	if err != nil {
		_ = l.file.Close()
		l.file = nil
	}
	return len(p), nil
}

func (l *adapterLog) rotate() {
	_ = l.file.Close()
	previous := l.path + ".1"
	_ = os.Remove(previous)
	if err := os.Rename(l.path, previous); err != nil {
		l.file = nil
		return
	}
	l.open(true)
}

func (l *adapterLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
