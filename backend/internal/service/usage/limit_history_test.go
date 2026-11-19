package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeLimitReadings []domain.AgentLimitReading

func (f fakeLimitReadings) ListAgentLimitReadings(_ context.Context, since time.Time) ([]domain.AgentLimitReading, error) {
	var out []domain.AgentLimitReading
	for _, r := range f {
		if !r.ReadAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (fakeLimitReadings) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return nil, nil
}

type fakeLimitStore struct {
	fakeLimitReadings
	saved []domain.AgentAccount
}

func (f fakeLimitStore) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return f.saved, nil
}

func loginStates(t *testing.T, reader *LimitHistoryReader) map[string][]string {
	t.Helper()
	history, err := reader.History(context.Background(), 7)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	out := map[string][]string{}
	for _, login := range history.Logins {
		for _, span := range login.Windows {
			out[login.Email] = append(out[login.Email], span.State)
		}
	}
	return out
}

func TestLimitHistoryShowsOnlyLoginsAnAccountHolds(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	reset := now.AddDate(0, 0, 2)
	weekly := func(account, email string, readAt time.Time) domain.AgentLimitReading {
		return domain.AgentLimitReading{
			AccountID: account, Provider: domain.AgentAccountProviderClaude, Email: email, WindowID: "seven_day",
			DurationMinutes: 10080, UsedPercent: 40, ResetsAt: &reset, ReadAt: readAt,
		}
	}
	reader := NewLimitHistoryReader(fakeLimitStore{
		fakeLimitReadings: fakeLimitReadings{
			weekly("device-claude", "old@example.com", now.Add(-25*time.Hour)),
			weekly("device-claude", "new@example.com", now.Add(-24*time.Hour)),
			weekly("acct-removed", "gone@example.com", now.Add(-3*time.Hour)),
			weekly("acct-kept", "kept@example.com", now.Add(-3*time.Hour)),
		},
		saved: []domain.AgentAccount{{ID: "acct-kept", Provider: domain.AgentAccountProviderClaude}},
	}, nil)
	reader.now = func() time.Time { return now }

	got := loginStates(t, reader)
	want := map[string][]string{"kept@example.com": {domain.UsageLimitSpanOpen}, "new@example.com": {domain.UsageLimitSpanOpen}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("logins = %v, want %v", got, want)
	}
}

type fakeConfirmedLogins map[string]domain.AgentAccountIdentityCheck

func (f fakeConfirmedLogins) ConfirmedLogins() map[string]domain.AgentAccountIdentityCheck { return f }

func TestLimitHistoryHidesASignedOutLoginUntilItIsBack(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	weekly := func(account string, provider domain.AgentAccountProvider, email string, reset, readAt time.Time) domain.AgentLimitReading {
		return domain.AgentLimitReading{
			AccountID: account, Provider: provider, Email: email, WindowID: "seven_day",
			DurationMinutes: 10080, UsedPercent: 40, ResetsAt: &reset, ReadAt: readAt,
		}
	}
	claude, codex := domain.AgentAccountProviderClaude, domain.AgentAccountProviderCodex
	lastWeek, thisWeek, readAt := now.AddDate(0, 0, -5), now.AddDate(0, 0, 2), now.Add(-2*time.Hour)
	confirmed := fakeConfirmedLogins{
		"device-claude": {SignedIn: true, Email: "b@example.com"},
		"acct-out":      {},
		"device-codex":  {SignedIn: true, Email: "C@Example.com"},
	}
	reader := NewLimitHistoryReader(fakeLimitStore{
		fakeLimitReadings: fakeLimitReadings{
			weekly("device-claude", claude, "a@example.com", lastWeek, lastWeek.Add(-time.Minute)),
			weekly("device-claude", claude, "a@example.com", thisWeek, readAt),
			weekly("acct-out", claude, "out@example.com", thisWeek, readAt),
			weekly("acct-unread", claude, "unread@example.com", thisWeek, readAt),
			weekly("device-codex", codex, "c@example.com", thisWeek, readAt),
		},
		saved: []domain.AgentAccount{{ID: "acct-out", Provider: claude}, {ID: "acct-unread", Provider: claude}},
	}, confirmed)
	reader.now = func() time.Time { return now }

	got := loginStates(t, reader)
	want := map[string][]string{"unread@example.com": {domain.UsageLimitSpanOpen}, "c@example.com": {domain.UsageLimitSpanOpen}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("while a@ and out@ are signed out: logins = %v, want %v", got, want)
	}

	confirmed["device-claude"] = domain.AgentAccountIdentityCheck{SignedIn: true, Email: "a@example.com"}
	got = loginStates(t, reader)
	want["a@example.com"] = []string{domain.UsageLimitSpanFinal, domain.UsageLimitSpanOpen}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after a@ is back: logins = %v, want %v", got, want)
	}
}

func TestLimitHistoryWindows(t *testing.T) {
	now := time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 28, h, m, 0, 0, time.UTC) }
	ptr := func(t time.Time) *time.Time { return &t }
	read := func(account, email, window string, minutes int, used float64, reset *time.Time, readAt time.Time) domain.AgentLimitReading {
		plan := "max"
		if email != "max@example.com" {
			plan = "team"
		}
		return domain.AgentLimitReading{
			AccountID: account, Provider: domain.AgentAccountProviderClaude, Email: email, Plan: plan, WindowID: window,
			Label: window, DurationMinutes: minutes, UsedPercent: used, ResetsAt: reset, ReadAt: readAt,
		}
	}
	jitter := at(5, 0).Add(700 * time.Millisecond)
	readings := fakeLimitReadings{
		read("acct-max", "max@example.com", "five_hour", 300, 10, ptr(at(5, 0)), at(1, 0)),
		read("acct-max", "max@example.com", "five_hour", 300, 15, &jitter, at(4, 57)),
		read("device-claude", "team@example.com", "five_hour", 300, 20, ptr(at(12, 0)), at(8, 0)),
		read("acct-team", "Team@Example.com", "five_hour", 300, 46, ptr(at(12, 0)), at(9, 0)),
		read("device-claude", "team@example.com", "five_hour", 300, 30, ptr(at(18, 0)), at(16, 0)),
		read("device-claude", "team@example.com", "five_hour", 300, 5, ptr(at(23, 0)), at(19, 0)),
		read("device-claude", "team@example.com", "model:Fable", 10080, 0, nil, at(19, 0)),
		read("device-claude", "team@example.com", "seven_day", 10080, 40, ptr(at(23, 0).AddDate(0, 0, 3)), at(18, 10)),
		read("device-claude", "team@example.com", "seven_day", 10080, 41, ptr(at(23, 0).AddDate(0, 0, 3)), at(18, 50)),
		read("device-claude", "team@example.com", "seven_day", 10080, 43, ptr(at(23, 0).AddDate(0, 0, 3)), at(19, 5)),
		{AccountID: "device-codex", Provider: domain.AgentAccountProviderCodex, Email: "c@example.com", WindowID: "codex:Weekly",
			DurationMinutes: 10080, UsedPercent: 69, ResetsAt: ptr(at(6, 0).AddDate(0, 0, 4)), ReadAt: at(6, 30)},
		{AccountID: "device-codex", Provider: domain.AgentAccountProviderCodex, Email: "c@example.com", WindowID: "codex:Weekly",
			DurationMinutes: 10080, UsedPercent: 1, ResetsAt: ptr(at(6, 35).AddDate(0, 0, 7)), ReadAt: at(6, 35)},
	}
	reader := NewLimitHistoryReader(fakeLimitStore{
		fakeLimitReadings: readings,
		saved:             []domain.AgentAccount{{ID: "acct-max", Provider: domain.AgentAccountProviderClaude}},
	}, nil)
	reader.now = func() time.Time { return now }

	history, err := reader.History(context.Background(), 7)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if history.FirstReadingAt == nil || !history.FirstReadingAt.Equal(at(1, 0)) {
		t.Fatalf("first reading = %v", history.FirstReadingAt)
	}
	if len(history.Logins) != 3 || history.Logins[0].Email != "max@example.com" || history.Logins[2].Provider != domain.AgentAccountProviderCodex {
		t.Fatalf("logins = %+v, want Max, Team, then Codex", history.Logins)
	}
	codex := history.Logins[2].Windows
	if len(codex) != 2 || codex[0].State != domain.UsageLimitSpanFinal || codex[0].PeakUsedPercent != 69 || codex[1].State != domain.UsageLimitSpanOpen {
		t.Fatalf("codex weeks = %+v, want the old week final and the new one open", codex)
	}

	maxPlan := history.Logins[0].Windows
	if len(maxPlan) != 1 || maxPlan[0].State != domain.UsageLimitSpanFinal || maxPlan[0].PeakUsedPercent != 15 || maxPlan[0].Readings != 2 ||
		maxPlan[0].StartsAt == nil || !maxPlan[0].StartsAt.Equal(at(0, 0)) {
		t.Fatalf("max windows = %+v, want one final 15%% window from 00:00", maxPlan)
	}

	team := history.Logins[1]
	if history.Logins[0].Plan != "max" || team.Plan != "team" {
		t.Fatalf("plans = %q, %q, want max and team", history.Logins[0].Plan, team.Plan)
	}
	if team.AccountID != "device-claude" {
		t.Fatalf("team account = %s, want the most recent reader", team.AccountID)
	}
	var sessions []domain.UsageLimitSpan
	var weekly *domain.UsageLimitSpan
	for i, w := range team.Windows {
		switch w.WindowID {
		case "five_hour":
			sessions = append(sessions, w)
		case "seven_day":
			weekly = &team.Windows[i]
		case "model:Fable":
			t.Fatalf("a window with no reset was reported: %+v", w)
		}
	}
	if len(sessions) != 3 {
		t.Fatalf("team sessions = %+v, want 3", sessions)
	}
	if sessions[0].PeakUsedPercent != 46 || sessions[0].Readings != 2 || sessions[0].State != domain.UsageLimitSpanPartial {
		t.Fatalf("merged window = %+v, want both accounts' readings, peak 46, read 3h before reset", sessions[0])
	}
	if sessions[1].State != domain.UsageLimitSpanPartial || sessions[2].State != domain.UsageLimitSpanOpen {
		t.Fatalf("states = %s, %s, want partial then open", sessions[1].State, sessions[2].State)
	}
	if weekly == nil || len(weekly.Points) != 2 || weekly.Points[0].UsedPercent != 41 || weekly.Points[1].UsedPercent != 43 {
		t.Fatalf("weekly = %+v, want the last reading of each hour", weekly)
	}
	if len(sessions[0].Points) != 0 {
		t.Fatalf("a five-hour window carries points: %+v", sessions[0].Points)
	}

	if _, err := reader.History(context.Background(), 15); err == nil {
		t.Fatal("15 days accepted; readings are kept for 14")
	}
}
