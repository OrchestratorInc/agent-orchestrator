package clienttz

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const Header = "X-AO-Client-Time-Zone"

const fileName = "client-time-zone"

type Recorder struct {
	path string
	mu   sync.Mutex
	last string
}

func NewRecorder(ctx context.Context, dataDir string) *Recorder {
	if dataDir == "" {
		return &Recorder{}
	}
	return &Recorder{path: filepath.Join(dataDir, fileName), last: Read(ctx, dataDir)}
}

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

func Valid(name string) bool {
	if name == "" || len(name) > 64 || name == "Local" || filepath.IsAbs(name) || strings.Contains(name, "..") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
