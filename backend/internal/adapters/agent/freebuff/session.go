package freebuff

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sessionRecord is what AO remembers about the Freebuff process a session ran:
// the config directory it actually used, so restore and session info search
// that session's chats even when the project's FREEBUFF_CONFIG_DIR changes or
// is absent from the caller's environment.
type sessionRecord struct {
	ConfigDir string `json:"configDir"`
}

func sessionRecordPath(dataDir, sessionID string) string {
	if dataDir == "" || sessionID == "" || sessionID != filepath.Base(sessionID) {
		return ""
	}
	return filepath.Join(dataDir, "freebuff", "sessions", sessionID+".json")
}

func writeSessionRecord(dataDir, sessionID, dir string) error {
	path := sessionRecordPath(dataDir, sessionID)
	if path == "" || dir == "" {
		return nil
	}
	data, err := json.Marshal(sessionRecord{ConfigDir: dir})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("freebuff: record session config: %w", err)
	}
	if err := writeFileAtomic(filepath.Dir(path), path, data); err != nil {
		return fmt.Errorf("freebuff: record session config: %w", err)
	}
	return nil
}

// sessionConfigDir resolves the config directory a session's Freebuff used:
// the recorded launch directory, else the daemon environment's.
func sessionConfigDir(dataDir, sessionID string) string {
	if path := sessionRecordPath(dataDir, sessionID); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var record sessionRecord
			if json.Unmarshal(data, &record) == nil && filepath.IsAbs(record.ConfigDir) {
				return record.ConfigDir
			}
		}
	}
	return configDir(nil)
}

// modelLeaseTTL bounds how long a launch may hold a config directory's model
// selection when AO never observes the TUI ready (a promptless launch, or one
// that failed before starting).
const modelLeaseTTL = 45 * time.Second

const modelLeasePoll = 200 * time.Millisecond

// modelLeases keeps concurrent launches from running each other's model.
// Freebuff reads settings.json once at startup, so a launch's selection must
// stay in the file until that process has read it. Launches that want the same
// selection share the lease; a different selection waits for it.
type modelLeases struct {
	mu     sync.Mutex
	byDir  map[string]*modelLease
	now    func() time.Time
	poll   time.Duration
	expiry time.Duration
}

type modelLease struct {
	selection string
	holders   map[string]time.Time
}

func newModelLeases() *modelLeases {
	return &modelLeases{byDir: map[string]*modelLease{}, now: time.Now, poll: modelLeasePoll, expiry: modelLeaseTTL}
}

// acquire waits until dir is free or already holds selection, then runs write
// while still holding the lease for sessionID.
func (l *modelLeases) acquire(ctx context.Context, dir, sessionID, selection string, write func() error) error {
	// The longest wait is one other launch's full lease.
	ctx, cancel := context.WithTimeout(ctx, l.expiry+5*time.Second)
	defer cancel()
	for {
		l.mu.Lock()
		lease := l.byDir[dir]
		if lease != nil {
			now := l.now()
			for holder, expires := range lease.holders {
				if !now.Before(expires) || holder == sessionID {
					delete(lease.holders, holder)
				}
			}
			if len(lease.holders) == 0 {
				lease = nil
			}
		}
		if lease == nil || lease.selection == selection {
			if lease == nil {
				lease = &modelLease{selection: selection, holders: map[string]time.Time{}}
				l.byDir[dir] = lease
			}
			err := write()
			if err == nil {
				lease.holders[sessionID] = l.now().Add(l.expiry)
			} else if len(lease.holders) == 0 {
				delete(l.byDir, dir)
			}
			l.mu.Unlock()
			return err
		}
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("freebuff: another session is still starting with a different model: %w", ctx.Err())
		case <-time.After(l.poll):
		}
	}
}

// release ends sessionID's hold on every directory.
func (l *modelLeases) release(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for dir, lease := range l.byDir {
		delete(lease.holders, sessionID)
		if len(lease.holders) == 0 {
			delete(l.byDir, dir)
		}
	}
}

func modelSelection(cfg ports.AgentConfig) string {
	return strings.TrimSpace(cfg.Model) + "\x00" + strings.TrimSpace(cfg.Effort)
}
