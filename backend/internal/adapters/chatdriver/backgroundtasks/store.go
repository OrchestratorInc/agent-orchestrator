package backgroundtasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// GetStartedAt returns the recorded start time for the given task and run identity.
// If runID is empty, it falls back to matching any recorded run for sessionID and taskID.
func (m *MemoryStore) GetStartedAt(sessionID, taskID, runID string) (time.Time, bool) {
	if sessionID == "" || taskID == "" {
		return time.Time{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if runID != "" {
		t, ok := m.entries[storeKey(sessionID, taskID, runID)]
		return t, ok
	}
	prefix := strconv.Itoa(len(sessionID)) + ":" + sessionID + ":" +
		strconv.Itoa(len(taskID)) + ":" + taskID + ":"
	for k, t := range m.entries {
		if strings.HasPrefix(k, prefix) && !t.IsZero() {
			return t, true
		}
	}
	return time.Time{}, false
}

// SetStartedAt records the start time for the given task and run identity.
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

const (
	maxPersistedRecords = 256
	maxPersistedAge     = 7 * 24 * time.Hour
)

var (
	storeRegistryMu   sync.Mutex
	fileStoreRegistry = make(map[string]*fileStore)
)

type fileStore struct {
	dataDir string
	mu      sync.Mutex
	cache   map[string]map[string]persistedTaskRecord
}

// NewFileStore returns a Store that persists start times under
// filepath.Join(dataDir, "chat-hosts", sessionID, "background-tasks.json").
// If dataDir is empty, an in-memory store is returned.
// Stores are cached per clean dataDir so multiple conversations share one cache and mutex.
func NewFileStore(dataDir string) Store {
	if dataDir == "" {
		return NewMemoryStore()
	}
	cleanDir := filepath.Clean(dataDir)
	storeRegistryMu.Lock()
	defer storeRegistryMu.Unlock()
	if s, ok := fileStoreRegistry[cleanDir]; ok {
		return s
	}
	s := &fileStore{
		dataDir: cleanDir,
		cache:   make(map[string]map[string]persistedTaskRecord),
	}
	fileStoreRegistry[cleanDir] = s
	return s
}

func (f *fileStore) taskFilePath(sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return "", errors.New("invalid session id")
	}
	return filepath.Join(f.dataDir, "chat-hosts", sessionID, "background-tasks.json"), nil
}

// Caller holds mu.
func (f *fileStore) loadLocked(sessionID string) (map[string]persistedTaskRecord, error) {
	if m, ok := f.cache[sessionID]; ok {
		return m, nil
	}
	filePath, err := f.taskFilePath(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			m := make(map[string]persistedTaskRecord)
			f.cache[sessionID] = m
			return m, nil
		}
		return nil, err
	}
	var records []persistedTaskRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	m := make(map[string]persistedTaskRecord, len(records))
	for _, r := range records {
		if r.TaskID != "" && !r.StartedAt.IsZero() {
			m[taskKey(r.TaskID, r.RunID)] = r
		}
	}
	f.cache[sessionID] = m
	return m, nil
}

// Caller holds mu. Returns a pruned copy of m without mutating m.
func (f *fileStore) pruneCopy(m map[string]persistedTaskRecord, now time.Time) map[string]persistedTaskRecord {
	if len(m) == 0 {
		return m
	}
	pruned := make(map[string]persistedTaskRecord, len(m))
	for k, r := range m {
		if !r.StartedAt.IsZero() && now.Sub(r.StartedAt) > maxPersistedAge {
			continue
		}
		pruned[k] = r
	}
	if len(pruned) <= maxPersistedRecords {
		return pruned
	}
	records := make([]persistedTaskRecord, 0, len(pruned))
	for _, r := range pruned {
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt.Before(records[j].StartedAt)
	})
	dropCount := len(records) - maxPersistedRecords
	for i := 0; i < dropCount; i++ {
		delete(pruned, taskKey(records[i].TaskID, records[i].RunID))
	}
	return pruned
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
	pruned := f.pruneCopy(m, time.Now())
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	records := make([]persistedTaskRecord, 0, len(pruned))
	for _, r := range pruned {
		records = append(records, r)
	}
	b, err := json.Marshal(records)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d-%d", filePath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	f.cache[sessionID] = pruned
	return nil
}

func (f *fileStore) GetStartedAt(sessionID, taskID, runID string) (time.Time, bool) {
	if sessionID == "" || taskID == "" {
		return time.Time{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.loadLocked(sessionID)
	if err != nil {
		return time.Time{}, false
	}
	if runID != "" {
		r, ok := m[taskKey(taskID, runID)]
		if !ok || r.StartedAt.IsZero() {
			return time.Time{}, false
		}
		return r.StartedAt, true
	}
	// Fallback when runID is empty: find by taskID alone.
	for _, r := range m {
		if r.TaskID == taskID && !r.StartedAt.IsZero() {
			return r.StartedAt, true
		}
	}
	return time.Time{}, false
}

func (f *fileStore) SetStartedAt(sessionID, taskID, runID string, at time.Time) error {
	if sessionID == "" || taskID == "" || at.IsZero() {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.loadLocked(sessionID)
	if err != nil {
		return err
	}
	k := taskKey(taskID, runID)
	if existing, ok := m[k]; ok && !existing.StartedAt.IsZero() && !at.Before(existing.StartedAt) {
		return nil
	}
	previous, hadPrevious := m[k]
	m[k] = persistedTaskRecord{
		TaskID:    taskID,
		RunID:     runID,
		StartedAt: at.UTC(),
	}
	if err := f.saveLocked(sessionID); err != nil {
		if hadPrevious {
			m[k] = previous
		} else {
			delete(m, k)
		}
		return err
	}
	return nil
}
