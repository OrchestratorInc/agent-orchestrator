package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Browser targets a session's commands can go to.
const (
	// TargetPanel is AO's embedded Browser panel (the default).
	TargetPanel = "panel"
	// TargetChrome is the user's own Chrome, through the AO extension.
	TargetChrome = "chrome"
)

// ValidTarget reports whether target names a supported browser target.
func ValidTarget(target string) bool {
	return target == TargetPanel || target == TargetChrome
}

// TargetStore persists each session's browser target. Sessions without an
// entry use the panel. The file lives in AO's data directory.
type TargetStore struct {
	path string

	mu      sync.Mutex
	targets map[domain.SessionID]string
}

// OpenTargetStore loads the store at path. A missing file is an empty store;
// an unreadable or corrupt file is an error so a chosen Chrome target is never
// silently dropped back to the panel.
func OpenTargetStore(path string) (*TargetStore, error) {
	store := &TargetStore{path: path, targets: map[domain.SessionID]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read browser targets: %w", err)
	}
	var saved map[domain.SessionID]string
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("parse browser targets: %w", err)
	}
	for sessionID, target := range saved {
		if ValidTarget(target) && target != TargetPanel {
			store.targets[sessionID] = target
		}
	}
	return store, nil
}

// Get returns the session's target, defaulting to the panel.
func (s *TargetStore) Get(sessionID domain.SessionID) string {
	if s == nil {
		return TargetPanel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if target, ok := s.targets[sessionID]; ok {
		return target
	}
	return TargetPanel
}

// Set records the session's target and persists the store.
func (s *TargetStore) Set(sessionID domain.SessionID, target string) error {
	if !ValidTarget(target) {
		return fmt.Errorf("unknown browser target %q", target)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, had := s.targets[sessionID]
	if target == TargetPanel {
		delete(s.targets, sessionID)
	} else {
		s.targets[sessionID] = target
	}
	if err := s.saveLocked(); err != nil {
		if had {
			s.targets[sessionID] = previous
		} else {
			delete(s.targets, sessionID)
		}
		return err
	}
	return nil
}

// Delete forgets a session, for example when it is destroyed.
func (s *TargetStore) Delete(sessionID domain.SessionID) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.targets[sessionID]; !ok {
		return nil
	}
	delete(s.targets, sessionID)
	return s.saveLocked()
}

func (s *TargetStore) saveLocked() error {
	data, err := json.MarshalIndent(s.targets, "", "  ")
	if err != nil {
		return fmt.Errorf("encode browser targets: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create browser targets directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".browser-targets-*.json")
	if err != nil {
		return fmt.Errorf("write browser targets: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write browser targets: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write browser targets: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write browser targets: %w", err)
	}
	return nil
}
