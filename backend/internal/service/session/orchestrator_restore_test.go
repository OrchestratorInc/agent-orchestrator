package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// These fakes own only in-memory session rows. No provider, runtime, database,
// or workspace is opened, including in the 64-request contention case.
type orchestratorRestoreStore struct {
	Store
	mu      sync.Mutex
	rows    map[domain.SessionID]domain.SessionRecord
	getErr  error
	listErr error
}

func newOrchestratorRestoreStore() *orchestratorRestoreStore {
	base := newFakeStore()
	base.projects["project"] = domain.ProjectRecord{ID: "project"}
	return &orchestratorRestoreStore{Store: base, rows: make(map[domain.SessionID]domain.SessionRecord)}
}

func (s *orchestratorRestoreStore) put(rec domain.SessionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[rec.ID] = rec
}

func (s *orchestratorRestoreStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, found := s.rows[id]
	return rec, found, s.getErr
}

func (s *orchestratorRestoreStore) ListSessions(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []domain.SessionRecord
	for _, rec := range s.rows {
		if project == "" || rec.ProjectID == project {
			rows = append(rows, rec)
		}
	}
	return rows, s.listErr
}

func (s *orchestratorRestoreStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	return s.ListSessions(ctx, "")
}

func (*orchestratorRestoreStore) ListPRFactsForSession(context.Context, domain.SessionID) ([]domain.PRFacts, error) {
	return nil, nil
}

func (*orchestratorRestoreStore) ListPRFactsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error) {
	return nil, nil
}

func (*orchestratorRestoreStore) ListCurrentHeadReviewRunsForSession(context.Context, domain.SessionID) ([]domain.CurrentHeadReviewRun, error) {
	return nil, nil
}

func (*orchestratorRestoreStore) ListCurrentHeadReviewRunsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID][]domain.CurrentHeadReviewRun, error) {
	return nil, nil
}

type orchestratorRestoreCommander struct {
	*fakeCommander
	store        *orchestratorRestoreStore
	restoreCalls atomic.Int32
	spawnCalls   atomic.Int32
	restoreHook  func(context.Context, domain.SessionID) error
	spawnHook    func()
}

func (m *orchestratorRestoreCommander) RestoreWithMode(ctx context.Context, id domain.SessionID) (sessionmanager.RestoreResult, error) {
	m.restoreCalls.Add(1)
	if m.restoreHook != nil {
		if err := m.restoreHook(ctx, id); err != nil {
			return sessionmanager.RestoreResult{}, err
		}
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return sessionmanager.RestoreResult{}, err
	}
	if !found {
		return sessionmanager.RestoreResult{}, sessionmanager.ErrNotFound
	}
	if !rec.IsTerminated {
		return sessionmanager.RestoreResult{}, sessionmanager.ErrNotRestorable
	}
	rec.IsTerminated = false
	m.store.put(rec)
	return sessionmanager.RestoreResult{Session: rec, Mode: sessionmanager.RestoreModeNative}, nil
}

func (m *orchestratorRestoreCommander) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	m.spawnCalls.Add(1)
	if m.spawnHook != nil {
		m.spawnHook()
	}
	rec := archivedOrchestrator("new")
	rec.IsTerminated = false
	rec.ProjectID = cfg.ProjectID
	m.store.put(rec)
	return rec, 0, 0, nil
}

func (m *orchestratorRestoreCommander) RetireForReplacement(ctx context.Context, id domain.SessionID) error {
	rec, _, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	rec.IsTerminated = true
	m.store.put(rec)
	return nil
}

func archivedOrchestrator(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: "project", Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, IsTerminated: true}
}

func orchestratorRestoreFixture() (*Service, *orchestratorRestoreStore, *orchestratorRestoreCommander) {
	store := newOrchestratorRestoreStore()
	manager := &orchestratorRestoreCommander{fakeCommander: &fakeCommander{}, store: store}
	return &Service{store: store, manager: manager}, store, manager
}

func requireOrchestratorConflict(t *testing.T, err error) {
	t.Helper()
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != apierr.KindConflict || apiError.Code != "ORCHESTRATOR_ALREADY_ACTIVE" {
		t.Fatalf("restore error = %v, want ORCHESTRATOR_ALREADY_ACTIVE conflict", err)
	}
}

func TestRestoreOrchestratorRejectsAnotherActiveOrchestrator(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		t.Run(string(mode), func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			old := archivedOrchestrator("old")
			old.Mode = mode
			store.put(old)
			active := archivedOrchestrator("current")
			active.IsTerminated = false
			// Exited is still an owned live session; a failed/unknown health
			// probe is not permission to create a second coordinator.
			active.Activity.State = domain.ActivityExited
			store.put(active)

			_, err := svc.Restore(context.Background(), old.ID)
			requireOrchestratorConflict(t, err)
			if got := manager.restoreCalls.Load(); got != 0 {
				t.Fatalf("manager restore calls = %d, want zero", got)
			}
			preserved, _, _ := store.GetSession(context.Background(), old.ID)
			if !preserved.IsTerminated {
				t.Fatal("rejected restore changed the archived session")
			}
		})
	}
}

func TestRestoreOrchestratorConcurrentAdmission(t *testing.T) {
	for _, count := range []int{1, 4, 8, 16, 32, 40, 64} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			start := make(chan struct{})
			results := make(chan error, count)
			for i := range count {
				id := domain.SessionID(fmt.Sprintf("archived-%d", i))
				store.put(archivedOrchestrator(id))
				go func() {
					<-start
					_, err := svc.Restore(context.Background(), id)
					results <- err
				}()
			}
			close(start)
			succeeded := 0
			for range count {
				if err := <-results; err == nil {
					succeeded++
				} else {
					requireOrchestratorConflict(t, err)
				}
			}
			if succeeded != 1 || manager.restoreCalls.Load() != 1 {
				t.Fatalf("%d requests: successful restores=%d, launches=%d; want one each", count, succeeded, manager.restoreCalls.Load())
			}
		})
	}
}

func TestRestoreOrchestratorSharesSpawnAdmission(t *testing.T) {
	for _, generic := range []bool{false, true} {
		t.Run(fmt.Sprintf("generic=%t", generic), func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			store.put(archivedOrchestrator("old"))
			entered := make(chan struct{})
			release := make(chan struct{})
			manager.spawnHook = func() { close(entered); <-release }
			spawned := make(chan error, 1)
			go func() {
				var err error
				if generic {
					_, _, _, err = svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "project", Kind: domain.KindOrchestrator})
				} else {
					_, err = svc.SpawnOrchestrator(context.Background(), "project", false, "", "")
				}
				spawned <- err
			}()
			<-entered // Spawn has checked for an active coordinator, but not published its row.
			restored := make(chan error, 1)
			go func() {
				_, err := svc.Restore(context.Background(), "old")
				restored <- err
			}()
			close(release)
			if err := <-spawned; err != nil {
				t.Fatalf("spawn: %v", err)
			}
			requireOrchestratorConflict(t, <-restored)
			if manager.spawnCalls.Load() != 1 || manager.restoreCalls.Load() != 0 {
				t.Fatalf("spawn calls=%d restore calls=%d; want 1 and 0", manager.spawnCalls.Load(), manager.restoreCalls.Load())
			}
		})
	}
}

func TestRestoreOrchestratorHoldsAdmissionUntilPublished(t *testing.T) {
	for _, operation := range []string{"generic", "orchestrator", "clean replacement"} {
		t.Run(operation, func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			store.put(archivedOrchestrator("old"))
			enteredRestore := make(chan struct{})
			releaseRestore := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseRestore) }) }
			t.Cleanup(release)
			manager.restoreHook = func(context.Context, domain.SessionID) error {
				close(enteredRestore)
				<-releaseRestore
				return nil
			}
			enteredSpawn := make(chan struct{})
			manager.spawnHook = func() { close(enteredSpawn) }
			restored := make(chan error, 1)
			go func() {
				_, err := svc.Restore(context.Background(), "old")
				restored <- err
			}()
			<-enteredRestore
			spawned := make(chan domain.Session, 1)
			spawnErrors := make(chan error, 1)
			go func() {
				var session domain.Session
				var err error
				if operation == "generic" {
					session, _, _, err = svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "project", Kind: domain.KindOrchestrator})
				} else {
					session, err = svc.SpawnOrchestrator(context.Background(), "project", operation == "clean replacement", "", "")
				}
				spawned <- session
				spawnErrors <- err
			}()
			select {
			case <-enteredSpawn:
				t.Error("spawn bypassed an in-flight restore before its session was published")
			case <-time.After(50 * time.Millisecond):
			}
			release()
			if err := <-restored; err != nil {
				t.Fatalf("restore: %v", err)
			}
			if err := <-spawnErrors; err != nil {
				t.Fatalf("spawn: %v", err)
			}
			got := <-spawned
			wantID, wantSpawns := domain.SessionID("old"), int32(0)
			if operation == "clean replacement" {
				wantID, wantSpawns = "new", 1
			}
			if got.ID != wantID || manager.spawnCalls.Load() != wantSpawns {
				t.Fatalf("spawn returned %s with %d launches; want %s with %d", got.ID, manager.spawnCalls.Load(), wantID, wantSpawns)
			}
		})
	}
}

func TestRestoreOrchestratorAdmissionLeavesOtherRestoresUnchanged(t *testing.T) {
	for _, name := range []string{"worker", "other project", "newer terminated", "same live session", "missing session"} {
		t.Run(name, func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			old, newer := archivedOrchestrator("old"), archivedOrchestrator("newer")
			newer.IsTerminated = false
			wantCode := ""
			switch name {
			case "worker":
				old.Kind = domain.KindWorker
			case "other project":
				newer.ProjectID = "other"
			case "newer terminated":
				newer.IsTerminated = true
			case "same live session":
				old.IsTerminated = false
				newer.IsTerminated = true
				wantCode = "SESSION_NOT_RESTORABLE"
			case "missing session":
				wantCode = "SESSION_NOT_FOUND"
			}
			if name != "missing session" {
				store.put(old)
			}
			store.put(newer)
			got, err := svc.Restore(context.Background(), old.ID)
			if wantCode == "" {
				if err != nil || got.Session.ID != old.ID || got.Mode != RestoreModeViewNative {
					t.Fatalf("restore = %+v, %v; want original session with native mode", got, err)
				}
			} else {
				var apiError *apierr.Error
				if !errors.As(err, &apiError) || apiError.Code != wantCode {
					t.Fatalf("restore error = %v, want %s", err, wantCode)
				}
			}
			if got := manager.restoreCalls.Load(); got != 1 {
				t.Fatalf("manager restore calls = %d, want one", got)
			}
		})
	}
}

func TestRestoreOrchestratorAdmissionReleasesAfterFailure(t *testing.T) {
	for _, launchErr := range []error{errors.New("launch failed"), context.Canceled, context.DeadlineExceeded} {
		t.Run(launchErr.Error(), func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			store.put(archivedOrchestrator("failed"))
			store.put(archivedOrchestrator("retry"))
			manager.restoreHook = func(_ context.Context, id domain.SessionID) error {
				if id == "failed" {
					return launchErr
				}
				return nil
			}
			if _, err := svc.Restore(context.Background(), "failed"); err == nil {
				t.Fatal("failed launch reported success")
			}
			if _, err := svc.Restore(context.Background(), "retry"); err != nil {
				t.Fatalf("retry after failed launch: %v", err)
			}
			if got := manager.restoreCalls.Load(); got != 2 {
				t.Fatalf("manager restore calls = %d, want failed launch plus retry", got)
			}
		})
	}
}

func TestRestoreOrchestratorAdmissionFailsClosedOnStoreErrors(t *testing.T) {
	for _, read := range []string{"get", "list"} {
		t.Run(read, func(t *testing.T) {
			svc, store, manager := orchestratorRestoreFixture()
			store.put(archivedOrchestrator("old"))
			storageErr := errors.New("storage unavailable")
			if read == "get" {
				store.getErr = storageErr
			} else {
				store.listErr = storageErr
			}
			if _, err := svc.Restore(context.Background(), "old"); !errors.Is(err, storageErr) {
				t.Fatalf("restore error = %v, want storage failure", err)
			}
			if got := manager.restoreCalls.Load(); got != 0 {
				t.Fatalf("manager restore calls = %d after unverified admission", got)
			}
			store.getErr, store.listErr = nil, nil
			if _, err := svc.Restore(context.Background(), "old"); err != nil {
				t.Fatalf("retry after storage recovery: %v", err)
			}
		})
	}
}

func TestRestoreOrchestratorAdmissionDoesNotLaunchCancelledRequest(t *testing.T) {
	svc, store, manager := orchestratorRestoreFixture()
	store.put(archivedOrchestrator("old"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Restore(ctx, "old"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled restore error = %v, want cancellation", err)
	}
	if got := manager.restoreCalls.Load(); got != 0 {
		t.Fatalf("manager restore calls = %d, want zero after cancellation", got)
	}
	if _, err := svc.Restore(context.Background(), "old"); err != nil {
		t.Fatalf("retry after cancellation: %v", err)
	}
}
