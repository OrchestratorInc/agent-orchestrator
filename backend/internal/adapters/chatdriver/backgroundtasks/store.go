package backgroundtasks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store persists background task start times across daemon restarts and chat reconnects.
// It is keyed by session id + task id + run id.
type Store interface {
	GetStartedAt(sessionID, taskID, runID string) (time.Time, bool)
	SetStartedAt(sessionID, taskID, runID string, at time.Time) error
}

func taskKey(taskID, runID string) string {
	return strconv.Itoa(len(taskID)) + ":" + taskID + ":" +
		strconv.Itoa(len(runID)) + ":" + runID
}

func storeKey(sessionID, taskID, runID string) string {
	return strconv.Itoa(len(sessionID)) + ":" + sessionID + ":" +
		strconv.Itoa(len(taskID)) + ":" + taskID + ":" +
		strconv.Itoa(len(runID)) + ":" + runID
}

// MemoryStore is an in-memory Store.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

// NewMemoryStore creates a thread-safe in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]time.Time)}
}

func (m *MemoryStore) GetStartedAt(sessionID, taskID, runID string) (time.Time, bool) {
	if sessionID == "" || taskID == "" {
		return time.Time{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.entries[storeKey(sessionID, taskID, runID)]
	return t, ok
}

func (m *MemoryStore) SetStartedAt(sessionID, taskID, runID string, at time.Time) error {
	if sessionID == "" || taskID == "" || at.IsZero() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := storeKey(sessionID, taskID, runID)
	if existing, ok := m.entries[k]; ok && !existing.IsZero() && !at.Before(existing) {
		return nil
	}
	m.entries[k] = at.UTC()
	return nil
}

type persistedTaskRecord struct {
	TaskID    string    `json:"taskId"`
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
}

type fileStore struct {
	dataDir string
	mu      sync.Mutex
	cache   map[string]map[string]persistedTaskRecord
}

// NewFileStore returns a Store that persists start times under
// filepath.Join(dataDir, "chat-hosts", sessionID, "background-tasks.json").
// If dataDir is empty, an in-memory store is returned.
func NewFileStore(dataDir string) Store {
	if dataDir == "" {
		return NewMemoryStore()
	}
	return &fileStore{
		dataDir: dataDir,
		cache:   make(map[string]map[string]persistedTaskRecord),
	}
}

func (f *fileStore) taskFilePath(sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return "", errors.New("invalid session id")
	}
	return filepath.Join(f.dataDir, "chat-hosts", sessionID, "background-tasks.json"), nil
}

// Caller holds mu.
func (f *fileStore) loadLocked(sessionID string) map[string]persistedTaskRecord {
	if m, ok := f.cache[sessionID]; ok {
		return m
	}
	m := make(map[string]persistedTaskRecord)
	f.cache[sessionID] = m
	filePath, err := f.taskFilePath(sessionID)
	if err != nil {
		return m
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return m
	}
	var records []persistedTaskRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return m
	}
	for _, r := range records {
		if r.TaskID != "" && !r.StartedAt.IsZero() {
			m[taskKey(r.TaskID, r.RunID)] = r
		}
	}
	return m
}

// Caller holds mu.
func (f *fileStore) saveLocked(sessionID string) error {
	filePath, err := f.taskFilePath(sessionID)
	if err != nil {
		return err
	}
	m, ok := f.cache[sessionID]
	if !ok || len(m) == 0 {
		return nil
	}
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	records := make([]persistedTaskRecord, 0, len(m))
	for _, r := range m {
		records = append(records, r)
	}
	b, err := json.Marshal(records)
	if err != nil {
		return err
	}
	tmp := filePath + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (f *fileStore) GetStartedAt(sessionID, taskID, runID string) (time.Time, bool) {
	if sessionID == "" || taskID == "" {
		return time.Time{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.loadLocked(sessionID)
	r, ok := m[taskKey(taskID, runID)]
	if !ok || r.StartedAt.IsZero() {
		return time.Time{}, false
	}
	return r.StartedAt, true
}

func (f *fileStore) SetStartedAt(sessionID, taskID, runID string, at time.Time) error {
	if sessionID == "" || taskID == "" || at.IsZero() {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.loadLocked(sessionID)
	k := taskKey(taskID, runID)
	if existing, ok := m[k]; ok && !existing.StartedAt.IsZero() && !at.Before(existing.StartedAt) {
		return nil
	}
	m[k] = persistedTaskRecord{
		TaskID:    taskID,
		RunID:     runID,
		StartedAt: at.UTC(),
	}
	return f.saveLocked(sessionID)
}
