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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forkCleanupCompleteLocked(side.ID, id) {
		return ErrSideClosed
	}
	closed := m.sideClosedLocked(side.ID) || side.AppRunID != m.runID
	if err := m.writeForkRecordLocked(ctx, side, id, adopted && !closed); err != nil {
		return err
	}
	if closed {
		if err := m.enqueueForkCleanupLocked(side, id); err != nil {
			return err
		}
		if adopted {
			return ErrSideClosed
		}
	}
	return nil
}

// Call with m.mu held; writes and cleanup removal share this metadata gate.
func (m *sideManager) writeForkRecordLocked(ctx context.Context, side domain.SideConversation, id string, adopted bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.service.dataDir == "" {
		return nil
	}
	if previous, err := m.readForkRecordLocked(side.ID); err == nil {
		if previous.ForkID != id || previous.RunID != side.AppRunID || previous.SessionID != side.SessionID || previous.HostID != side.ProviderHostID || previous.Harness != side.Harness || previous.MainConversationID != side.MainConversationID {
			return ErrSideUnavailable
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
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

func (m *sideManager) readForkRecordLocked(sideID string) (sideForkRecord, error) {
	body, err := os.ReadFile(m.forkJournalPath(sideID))
	if err != nil {
		return sideForkRecord{}, err
	}
	var record sideForkRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return record, err
	}
	if record.SideID != sideID || record.MainConversationID == "" || record.RunID == "" || record.SessionID == "" || record.ForkID == "" || record.HostID != "btw-"+record.RunID+"-"+record.SideID || (record.Harness != "" && !record.Harness.IsKnown()) {
		return record, ErrSideUnavailable
	}
	return record, nil
}

func (m *sideManager) sideClosedLocked(id string) bool {
	if store, ok := m.store.(*memorySideStore); ok {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.closed[id]
	}
	return false
}

func (m *sideManager) forkCleanupCompleteLocked(id, fork string) bool {
	if store, ok := m.store.(*memorySideStore); ok {
		store.mu.Lock()
		defer store.mu.Unlock()
		return fork != "" && store.completedForks[id] == fork
	}
	return false
}

func (m *sideManager) enqueueForkCleanupLocked(side domain.SideConversation, id string) error {
	store, ok := m.store.(*memorySideStore)
	if !ok {
		return ErrSideUnavailable
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if previous, exists := store.hostCleanup[side.ID]; exists {
		if previous.ProviderForkID != "" && previous.ProviderForkID != id {
			return ErrSideUnavailable
		}
		if previous.ProviderForkID == id {
			return nil
		}
	}
	side.ProviderForkID = id
	store.closed[side.ID] = true
	store.hostCleanup[side.ID] = side
	store.cleanupEpoch[side.ID]++
	// A newly reported child is new cleanup evidence, not a stale restore of
	// the previously stopped, fork-less opening host.
	store.hostStopped[side.ID] = false
	if id != "" {
		store.cleanup[id] = domain.SideProviderCleanup{ForkID: id, SessionID: side.SessionID, Harness: side.Harness}
	}
	return nil
}

func (m *sideManager) registerFork(ctx context.Context, side domain.SideConversation, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forkCleanupCompleteLocked(side.ID, id) {
		return ErrSideClosed
	}
	current, err := m.store.SideConversation(ctx, side.ID)
	if err == nil && (current.Generation != side.Generation || current.AppRunID != side.AppRunID || (current.ProviderForkID != "" && current.ProviderForkID != id)) {
		return ErrSideUnavailable
	}
	if err := m.writeForkRecordLocked(ctx, side, id, false); err != nil {
		return err
	}
	if errors.Is(err, ErrSideClosed) || side.AppRunID != m.runID {
		if err := m.enqueueForkCleanupLocked(side, id); err != nil {
			return err
		}
		return ErrSideClosed
	}
	if err != nil {
		return err
	}
	return m.store.RegisterSideFork(ctx, side.ID, side.Generation, id, m.service.now())
}

func (m *sideManager) closeJournalSide(ctx context.Context, session domain.SessionID, id string) error {
	if m.service.store == nil {
		return ErrSideUnavailable
	}
	parent, err := m.service.store.ConversationForSession(ctx, session)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.service.dataDir == "" {
		m.mu.Unlock()
		return ErrSideClosed
	}
	record, err := m.readForkRecordLocked(id)
	if errors.Is(err, os.ErrNotExist) {
		err = ErrSideClosed
	}
	if err == nil && (record.RunID != m.runID || record.SessionID != session || record.MainConversationID != parent.ID || !record.Harness.IsKnown()) {
		err = ErrSideUnavailable
	}
	side := domain.SideConversation{ID: record.SideID, AppRunID: record.RunID, SessionID: record.SessionID, MainConversationID: record.MainConversationID, ProviderHostID: record.HostID, ProviderForkID: record.ForkID, Harness: record.Harness}
	if err == nil {
		err = m.writeForkRecordLocked(ctx, side, record.ForkID, false)
	}
	if err == nil {
		err = m.enqueueForkCleanupLocked(side, record.ForkID)
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return m.finishSideClose(ctx, side)
}

func (m *sideManager) recoverForkRecords(ctx context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recoverForkRecordsLocked(ctx, runID)
}

func (m *sideManager) recoverForkRecordsLocked(ctx context.Context, runID string) error {
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
	_, ok := m.store.(*memorySideStore)
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
		if err := m.enqueueForkCleanupLocked(side, side.ProviderForkID); err != nil {
			return err
		}
	}
	return nil
}

func (m *sideManager) completeForkCleanup(ctx context.Context, side domain.SideConversation, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.removeForkRecordLocked(ctx, side, id); err != nil {
		return err
	}
	return m.store.CompleteSideProviderCleanup(context.WithoutCancel(ctx), id)
}

func (m *sideManager) removeForkRecordLocked(ctx context.Context, side domain.SideConversation, id string) error {
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
