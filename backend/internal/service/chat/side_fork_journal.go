package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Only provider ownership is durable; side transcripts and launch secrets stay in RAM.
type sideForkRecord struct {
	MainConversationID string              `json:"mainConversationId"`
	SideID             string              `json:"sideId"`
	RunID              string              `json:"runId"`
	SessionID          domain.SessionID    `json:"sessionId"`
	HostID             string              `json:"hostId"`
	ForkID             string              `json:"forkId"`
	Harness            domain.AgentHarness `json:"harness"`
	Adopted            bool                `json:"adopted"`
}

func (m *sideManager) forkJournalPath(sideID string) string {
	return filepath.Join(m.service.dataDir, "side-forks", fmt.Sprintf("%x.json", sha256.Sum256([]byte(sideID))))
}

func (m *sideManager) writeForkRecord(ctx context.Context, side domain.SideConversation, id string, adopted bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.service.dataDir == "" {
		return nil
	}
	record := sideForkRecord{MainConversationID: side.MainConversationID, SideID: side.ID, RunID: side.AppRunID, SessionID: side.SessionID, HostID: side.ProviderHostID, ForkID: id, Harness: side.Harness, Adopted: adopted}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := m.forkJournalPath(side.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fork-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *sideManager) recoverForkRecords(ctx context.Context, runID string) error {
	if m.service.dataDir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(m.service.dataDir, "side-forks"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	store, ok := m.store.(*memorySideStore)
	if !ok {
		return ErrSideUnavailable
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(m.service.dataDir, "side-forks", entry.Name()))
		if err != nil {
			return err
		}
		var record sideForkRecord
		if err := json.Unmarshal(body, &record); err != nil {
			return err
		}
		if record.MainConversationID == "" || record.SideID == "" || record.RunID == "" || record.SessionID == "" || record.ForkID == "" || record.HostID != "btw-"+record.RunID+"-"+record.SideID || entry.Name() != filepath.Base(m.forkJournalPath(record.SideID)) {
			return fmt.Errorf("invalid side fork cleanup record %s", entry.Name())
		}
		// Electron restores adopted sides from this launch. An interrupted load
		// cannot be adopted: retire its host before deleting its recorded child.
		if record.Adopted && record.RunID == runID {
			continue
		}
		side := domain.SideConversation{MainConversationID: record.MainConversationID, ID: record.SideID, AppRunID: record.RunID, SessionID: record.SessionID, ProviderHostID: record.HostID, ProviderForkID: record.ForkID, Harness: record.Harness}
		store.mu.Lock()
		store.closed[side.ID] = true
		store.hostCleanup[side.ID] = side
		store.cleanup[side.ProviderForkID] = domain.SideProviderCleanup{ForkID: side.ProviderForkID, SessionID: side.SessionID, Harness: side.Harness}
		store.mu.Unlock()
	}
	return nil
}

func (m *sideManager) removeForkRecord(ctx context.Context, side domain.SideConversation, id string) error {
	if m.service.dataDir == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(m.service.dataDir, "side-forks"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(m.service.dataDir, "side-forks", entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record sideForkRecord
		if err := json.Unmarshal(body, &record); err != nil {
			return err
		}
		if record.ForkID == id && record.SessionID == side.SessionID && record.Harness == side.Harness {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
