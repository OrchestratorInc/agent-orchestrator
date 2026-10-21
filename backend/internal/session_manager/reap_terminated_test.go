package sessionmanager

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/orphanreap"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeNotifier struct {
	intents []ports.NotificationIntent
	err     error
}

func (f *fakeNotifier) Notify(_ context.Context, intent ports.NotificationIntent) error {
	if f.err != nil {
		return f.err
	}
	f.intents = append(f.intents, intent)
	return nil
}

// orphanScenarioHarness builds a manager whose store holds one terminated and
// one live session and whose process table shows an identical pty-host chain
// for each. Only the terminated session's tree is decidable.
func orphanScenarioHarness(t *testing.T) (*Manager, *fakeNotifier, *[]int) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["dead-1"] = domain.SessionRecord{ID: "dead-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true}
	st.sessions["live-1"] = domain.SessionRecord{ID: "live-1", ProjectID: "mer", Kind: domain.KindWorker}
	m := New(Deps{
		Runtime:    &fakeRuntime{},
		Agents:     fakeAgents{},
		Workspace:  &fakeWorkspace{},
		Store:      st,
		Messenger:  &fakeMessenger{},
		Lifecycle:  &fakeLCM{store: st},
		LookPath:   func(string) (string, error) { return "/bin/true", nil },
		DataDir:    t.TempDir(),
		Executable: func() (string, error) { return "/tmp/fake-bin/ao", nil },
	})
	boot := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	m.bootStartedAt = boot
	at := boot.Add(5 * time.Second)
	m.processTable = func(context.Context) (*orphanreap.Table, time.Time, error) {
		tbl, err := orphanreap.ParseTable(strings.Join([]string{
			"  500     1  7168  2-00:00:00 /tmp/fake-bin/ao pty-host dead-1 " +
				filepath.Join(m.dataDir, "worktrees", "mer", "dead-1") + " bash -lc claude",
			"  501   500  6144  2-00:00:00 /tmp/fake-bin/ao agent-process supervise --session dead-1 --launch L1 -- claude",
			"  502     1  7168  2-00:00:00 /tmp/fake-bin/ao pty-host live-1 " +
				filepath.Join(m.dataDir, "worktrees", "mer", "live-1") + " bash -lc claude",
		}, "\n"), at)
		return tbl, at, err
	}
	notifier := &fakeNotifier{}
	m.notifier = notifier
	var signaled []int
	m.orphanKiller = orphanreap.Killer{
		Signal: func(pid int) error { signaled = append(signaled, pid); return nil },
		Kill:   func(pid int) error { signaled = append(signaled, pid); return nil },
		Alive:  func(int) bool { return false },
		Grace:  time.Millisecond,
		Poll:   time.Millisecond,
	}
	return m, notifier, &signaled
}

// TestReapTerminatedDoesNotSignalOrphanTrees pins the boundary: the boot/shutdown
// reap destroys stored runtime handles only — the process-table pass never
// signals anything, that decision belongs to the user via ReapOrphanWorkers.
func TestReapTerminatedDoesNotSignalOrphanTrees(t *testing.T) {
	m, notifier, signaled := orphanScenarioHarness(t)
	if err := m.ReapTerminated(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*signaled) != 0 {
		t.Fatalf("boot reap signaled %v, want nothing without the user's allow", *signaled)
	}
	if len(notifier.intents) != 0 {
		t.Fatalf("boot reap notified %+v, want the notify step in NotifyOrphanWorkers only", notifier.intents)
	}
}

func TestNotifyOrphanWorkersEmitsOneBatchIntent(t *testing.T) {
	m, notifier, signaled := orphanScenarioHarness(t)
	m.NotifyOrphanWorkers(context.Background())
	if len(notifier.intents) != 1 {
		t.Fatalf("intents = %d (%+v), want exactly one batch report", len(notifier.intents), notifier.intents)
	}
	intent := notifier.intents[0]
	if intent.Type != domain.NotificationOrphanWorkers {
		t.Fatalf("type = %q, want orphan_workers", intent.Type)
	}
	if intent.SessionID != "dead-1" {
		t.Fatalf("anchor session = %q, want the first victim dead-1", intent.SessionID)
	}
	if !strings.HasPrefix(intent.SourceKey, "orphan_workers:") {
		t.Fatalf("source key = %q, want the orphan_workers:<daemonRunID> shape", intent.SourceKey)
	}
	if !strings.Contains(intent.Detail, "dead-1") || strings.Contains(intent.Detail, "live-1") {
		t.Fatalf("detail %q must list the victim dead-1 and never the live session", intent.Detail)
	}
	if len(*signaled) != 0 {
		t.Fatalf("notify pass signaled %v, want nothing until the user allows", *signaled)
	}

	// Dismissal is the frontend's existing clear; asking again next start is
	// the SourceKey's job. A second scan in the same run stays silent here
	// because the notification writer's dedupe owns the frequency.
	m.NotifyOrphanWorkers(context.Background())
	if len(notifier.intents) != 2 {
		t.Fatalf("second scan emitted %d intents, want the raw Notify call again (dedupe is the store's)", len(notifier.intents))
	}
}

func TestNotifyOrphanWorkersSkipsWithoutVictimsOrNotifier(t *testing.T) {
	m, notifier, _ := orphanScenarioHarness(t)
	// Every row live: nothing decidable, no notify.
	st := m.store.(*fakeStore)
	live := st.sessions["dead-1"]
	live.IsTerminated = false
	st.sessions["dead-1"] = live
	m.NotifyOrphanWorkers(context.Background())
	if len(notifier.intents) != 0 {
		t.Fatalf("live-only board notified %+v, want silence", notifier.intents)
	}
	// Victims present but no notifier wired (tests/embedders): no panic.
	live.IsTerminated = true
	st.sessions["dead-1"] = live
	m.notifier = nil
	m.NotifyOrphanWorkers(context.Background())
}

func TestReapOrphanWorkersKillsPlannedVictimsOnly(t *testing.T) {
	m, _, signaled := orphanScenarioHarness(t)
	sessions, err := m.ReapOrphanWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0] != "dead-1" {
		t.Fatalf("sessions = %v, want exactly [dead-1]", sessions)
	}
	if len(*signaled) != 1 || (*signaled)[0] != 500 {
		t.Fatalf("signaled = %v, want exactly the dead session's pty-host root 500", *signaled)
	}
}
