package agentaccounts

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

// fakeLimitLog keeps every limits read it is given.
type fakeLimitLog struct {
	mu       sync.Mutex
	readings []domain.AgentLimitReading
}

func (l *fakeLimitLog) RecordAgentLimitReadings(_ context.Context, readings []domain.AgentLimitReading) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.readings = append(l.readings, readings...)
	return nil
}

func (l *fakeLimitLog) ListAgentLimitReadings(_ context.Context, since time.Time) ([]domain.AgentLimitReading, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []domain.AgentLimitReading
	for _, r := range l.readings {
		if !r.ReadAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (*fakeLimitLog) LimitHistoryEnabled(context.Context) bool { return true }

func (l *fakeLimitLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.readings = nil
}

func (l *fakeLimitLog) all() []domain.AgentLimitReading {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]domain.AgentLimitReading(nil), l.readings...)
}

type limitHistoryStore struct {
	*fakeLimitLog
	accounts *memStore
}

func (s limitHistoryStore) ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	return s.accounts.ListAgentAccounts(ctx)
}

// A check that names no organization reads as the saved account's own, both
// for the limits it records and for the login it confirms, so the account's
// windows stay shown (#8891).
func TestRefreshWithoutAnOrganizationKeepsTheSavedAccountsWindows(t *testing.T) {
	ctx := context.Background()
	svc, claude, store, root := newDuplicateFixture(t, "")
	claude.orgOf, claude.orgNameOf = map[string]string{}, map[string]string{}
	log := &fakeLimitLog{}
	svc.SetLimitLog(log)
	reset := time.Now().Add(3 * time.Hour)
	claude.windows = []domain.AgentAccountLimitWindow{{ID: "five_hour", Label: "Session", UsedPercent: 30, ResetsAt: &reset, DurationMinutes: 300}}
	_, team := startAdd(t, svc, claude, root, "a@example.com", "org-team")
	id := team.Account.ID
	claude.orgNameOf[team.Account.Home] = "Acme"
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}

	// The next check succeeds but names no organization.
	delete(claude.orgOf, team.Account.Home)
	delete(claude.orgNameOf, team.Account.Home)
	log.reset()
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if v := viewByID(t, views, id); !v.SignedIn || v.OrgID != "org-team" || v.OrgName != "Acme" {
		t.Fatalf("view = %+v, want the account signed in to its own organization", v)
	}
	recorded := log.all()
	if len(recorded) == 0 {
		t.Fatal("the refresh recorded no limits")
	}
	for _, r := range recorded {
		if r.AccountID == id && r.OrgID != "org-team" {
			t.Fatalf("recorded %+v, want the account's own organization", r)
		}
	}
	if got := svc.ConfirmedLogins()[id]; !got.SignedIn || got.OrgID != "org-team" {
		t.Fatalf("confirmed login = %+v, want the account's own organization", got)
	}

	history, err := usage.NewLimitHistoryReader(limitHistoryStore{log, store}, svc).History(ctx, 7)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history.Logins) != 1 || history.Logins[0].AccountID != id || history.Logins[0].OrganizationUnknown ||
		len(history.Logins[0].Windows) != 1 || history.Logins[0].Windows[0].PeakUsedPercent != 30 {
		t.Fatalf("logins = %+v, want the account's window shown", history.Logins)
	}
}
