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

// TestOrphanReapKillSwitch pins the AO_NO_ORPHANREAP gate: with the flag set,
// the boot scan stays silent and the allow endpoint is a clean no-op —
// victims may exist, nothing is reported or signalled.
func TestOrphanReapKillSwitch(t *testing.T) {
	t.Setenv("AO_NO_ORPHANREAP", "1")
	m, notifier, signaled := orphanScenarioHarness(t)
	m.NotifyOrphanWorkers(context.Background())
	if len(notifier.intents) != 0 {
		t.Fatalf("disabled scan notified %+v", notifier.intents)
	}
	sessions, err := m.ReapOrphanWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		t.Fatalf("disabled reap returned %v, want nil", sessions)
	}
	if len(*signaled) != 0 {
		t.Fatalf("disabled reap signaled %v", *signaled)
	}
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

// supersededResumeHarness builds a manager around a full resume fixture: an
// exited live session whose recorded handle probes definitively gone, so the
// resume's restartRuntime falls through to a fresh Create — the supersession
// moment. The process table shows a pre-boot PPID=1 pty-host chain for that
// session (the #115 orphan) and one for an unrelated live session.
func supersededResumeHarness(t *testing.T) (*Manager, *fakeRuntime, *[]int) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/ws/mer-1",
			Branch:          "ao/mer-1",
			RuntimeHandleID: "h-mer-1",
			RuntimeLaunchID: "launch-old",
			AgentSessionID:  "agent-x",
		},
	}
	rt := &fakeRuntime{}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/tmp/fake-bin/ao", nil },
		NewLaunchID: func() string { return "launch-new" },
	})
	boot := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	m.bootStartedAt = boot
	at := boot.Add(5 * time.Second)
	m.processTable = func(context.Context) (*orphanreap.Table, time.Time, error) {
		tbl, err := orphanreap.ParseTable(strings.Join([]string{
			"  502     1  7168  2-00:00:00 /tmp/fake-bin/ao pty-host mer-1 " +
				filepath.Join(m.dataDir, "worktrees", "mer", "mer-1") + " bash -lc claude",
			"  503     1  7168  2-00:00:00 /tmp/fake-bin/ao pty-host other-9 " +
				filepath.Join(m.dataDir, "worktrees", "mer", "other-9") + " bash -lc claude",
		}, "\n"), at)
		return tbl, at, err
	}
	var signaled []int
	m.orphanKiller = orphanreap.Killer{
		Signal: func(pid int) error { signaled = append(signaled, pid); return nil },
		Kill:   func(pid int) error { signaled = append(signaled, pid); return nil },
		Alive:  func(int) bool { return false },
		Grace:  time.Millisecond,
		Poll:   time.Millisecond,
	}
	return m, rt, &signaled
}

// TestResumeReapsSupersededOrphanTree pins the #115 reap: the resume binds a
// fresh runtime to the live session, and the pre-boot PPID=1 pty-host tree of
// the SAME session is reaped. The unrelated live session's tree stays — no
// terminated evidence, no supersession, not this pass's business.
func TestResumeReapsSupersededOrphanTree(t *testing.T) {
	m, _, signaled := supersededResumeHarness(t)
	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !sameInts(*signaled, []int{502}) {
		t.Fatalf("signaled = %v, want exactly the superseded mer-1 tree root 502", *signaled)
	}
}

// The supersession reap rides the same kill switch as the boot pass: with
// AO_NO_ORPHANREAP set, a resume never signals anything.
func TestResumeSupersededReapRespectsKillSwitch(t *testing.T) {
	t.Setenv("AO_NO_ORPHANREAP", "1")
	m, _, signaled := supersededResumeHarness(t)
	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(*signaled) != 0 {
		t.Fatalf("disabled reap signaled %v", *signaled)
	}
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[int]int, len(want))
	for _, w := range want {
		seen[w]++
	}
	for _, g := range got {
		seen[g]--
		if seen[g] < 0 {
			return false
		}
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
