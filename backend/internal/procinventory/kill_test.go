package procinventory

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeKiller struct {
	table        []Entry
	live         map[domain.SessionID]domain.SessionRecord
	signals      []string
	goneOnTerm   map[int]bool // pids whose group is already gone at TERM time
	alive        map[int]bool
	unregistered []string
}

func (f *fakeKiller) scan(context.Context) ([]Entry, error) {
	// Route through the real parser so Lstart normalization matches production.
	return ParseTable(psTable(f.table))
}

func (f *fakeKiller) liveSessions(context.Context) (map[domain.SessionID]domain.SessionRecord, error) {
	return f.live, nil
}

func (f *fakeKiller) signal(pid int, sig Signal) error {
	f.signals = append(f.signals, sigLabel(sig)+"@"+strconv.Itoa(pid))
	if sig == SigTerm && f.goneOnTerm[-pid] {
		return ErrProcessGone
	}
	if sig == SigKill {
		delete(f.alive, -pid)
	}
	return nil
}

func (f *fakeKiller) probe(pid int) bool { return f.alive[pid] }

func sigLabel(sig Signal) string {
	if sig == SigKill {
		return "SIGKILL"
	}
	return "SIGTERM"
}

func newTestService(k *fakeKiller) *Service {
	return New(Deps{
		Scan:         k.scan,
		DaemonPID:    testDaemonPID,
		LiveSessions: k.liveSessions,
		Grace:        30 * time.Millisecond,
		Signal:       k.signal,
		Probe:        k.probe,
		Unregister: func(_ context.Context, sessionID string) error {
			k.unregistered = append(k.unregistered, sessionID)
			return nil
		},
	})
}

func killFixture() *fakeKiller {
	k := &fakeKiller{
		table: []Entry{
			{PID: testDaemonPID, PPID: 1, PGID: testDaemonPID, RSSKB: 90000, Command: "/usr/local/bin/ao daemon"},
			ptyRoot(300, testDaemonPID, "orphan-1"), // dead session, daemon-side leak
			{PID: 301, PPID: 300, PGID: 300, RSSKB: 140000, Command: "claude --resume abc"},
			ptyRoot(200, 1, "adopted-live"), // adopted live session
		},
		live:  map[domain.SessionID]domain.SessionRecord{"adopted-live": {Kind: domain.KindWorker}},
		alive: map[int]bool{300: true, 301: true, 200: true},
	}
	return k
}

func TestKill_OrphanTerminatedOnTerm(t *testing.T) {
	k := killFixture()
	k.alive = map[int]bool{300: false, 301: false} // TERM takes effect immediately
	svc := newTestService(k)

	report, err := svc.Kill(context.Background(), []KillTarget{{SessionID: "orphan-1", RootPID: 300, RootLstart: "Mon Sep 14 00:13:02 2026"}})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != ResultKilled {
		t.Fatalf("report = %+v", report)
	}
	if len(k.signals) != 1 || k.signals[0] != "SIGTERM@-300" {
		t.Fatalf("signals = %v, want a single group SIGTERM", k.signals)
	}
	if len(k.unregistered) != 1 || k.unregistered[0] != "orphan-1" {
		t.Fatalf("unregistered = %v", k.unregistered)
	}
}

func TestKill_RefusesOwnedWithoutSignals(t *testing.T) {
	k := killFixture()
	svc := newTestService(k)

	report, err := svc.Kill(context.Background(), []KillTarget{{SessionID: "adopted-live", RootPID: 200, RootLstart: "Mon Sep 14 00:13:02 2026"}})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if report.Results[0].Status != ResultSkipped {
		t.Fatalf("status = %s, want skipped", report.Results[0].Status)
	}
	if len(k.signals) != 0 {
		t.Fatalf("signals = %v, an owned tree must never be signalled", k.signals)
	}
}

func TestKill_StaleLstartAndMissingTreeSkipped(t *testing.T) {
	k := killFixture()
	svc := newTestService(k)

	report, err := svc.Kill(context.Background(), []KillTarget{
		{SessionID: "orphan-1", RootPID: 300, RootLstart: "Tue Sep 15 00:13:02 2026"}, // recycled
		{SessionID: "ghost", RootPID: 999, RootLstart: "Mon Sep 14 00:13:02 2026"},    // not in snapshot
	})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	for _, result := range report.Results {
		if result.Status != ResultSkipped {
			t.Fatalf("result %+v, want skipped", result)
		}
	}
	if len(k.signals) != 0 {
		t.Fatalf("signals = %v", k.signals)
	}
}

func TestKill_EscalatesToSigkillWhenTermIgnored(t *testing.T) {
	k := killFixture() // probe keeps 300 alive regardless of TERM
	svc := newTestService(k)

	report, err := svc.Kill(context.Background(), []KillTarget{{SessionID: "orphan-1", RootPID: 300, RootLstart: "Mon Sep 14 00:13:02 2026"}})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if report.Results[0].Status != ResultKilled {
		t.Fatalf("status = %s", report.Results[0].Status)
	}
	if len(k.signals) != 2 || k.signals[0] != "SIGTERM@-300" || k.signals[1] != "SIGKILL@-300" {
		t.Fatalf("signals = %v, want TERM then KILL", k.signals)
	}
}

func TestKill_AlreadyGone(t *testing.T) {
	k := killFixture()
	k.goneOnTerm = map[int]bool{300: true}
	svc := newTestService(k)

	report, err := svc.Kill(context.Background(), []KillTarget{{SessionID: "orphan-1", RootPID: 300, RootLstart: "Mon Sep 14 00:13:02 2026"}})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if report.Results[0].Status != ResultAlreadyGone {
		t.Fatalf("status = %s", report.Results[0].Status)
	}
}

func TestKill_ConcurrentCallRejected(t *testing.T) {
	k := killFixture()
	svc := newTestService(k)
	svc.killMu <- struct{}{} // hold the single-flight slot
	defer func() { <-svc.killMu }()

	if _, err := svc.Kill(context.Background(), nil); err != ErrKillInProgress {
		t.Fatalf("err = %v, want ErrKillInProgress", err)
	}
}
