// Package clienttz remembers the time zone the user's own client reported.
//
// The daemon may run on a remote host or a cloud machine whose zone says
// nothing about the user, so agent prompts prefer the zone the desktop app
// sends with its requests. The latest report is kept in the data directory so
// sessions spawned later by the CLI or an orchestrator, or after a daemon
// restart, still use it.
package clienttz

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Header carries the client's IANA time zone, e.g. "Asia/Kolkata".
const Header = "X-AO-Client-Time-Zone"

const fileName = "client-time-zone"

// Recorder persists the most recent valid zone a client reported.
type Recorder struct {
	path string
	mu   sync.Mutex
	last string
}

// NewRecorder returns a Recorder backed by dataDir. An empty dataDir yields a
// Recorder that ignores reports.
func NewRecorder(ctx context.Context, dataDir string) *Recorder {
	if dataDir == "" {
		return &Recorder{}
	}
	return &Recorder{path: filepath.Join(dataDir, fileName), last: Read(ctx, dataDir)}
}

// Observe records name when it is a valid zone that differs from the last
// one recorded. Writes are best-effort: a failure leaves the previous value.
func (r *Recorder) Observe(ctx context.Context, name string) {
	if r == nil || r.path == "" || ctx.Err() != nil {
		return
	}
	name = strings.TrimSpace(name)
	if !Valid(name) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == r.last || ctx.Err() != nil {
		return
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(name+"\n"), 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	r.last = name
}

// Read returns the zone last recorded under dataDir, or "" when none is.
func Read(ctx context.Context, dataDir string) string {
	if dataDir == "" || ctx.Err() != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dataDir, fileName))
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if !Valid(name) {
		return ""
	}
	return name
}

// Valid reports whether name is a loadable IANA zone name. Unlike the
// daemon's own zone, a client-reported UTC is accepted: it is the user's
// actual setting.
func Valid(name string) bool {
	if name == "" || len(name) > 64 || name == "Local" || filepath.IsAbs(name) || strings.Contains(name, "..") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
