package usage

import (
	"context"
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

// TestLimitHistoryWindows pins how readings become windows: one window per
// reset (jitter merged), final only when read near its reset, the computer's
// login split when it changes accounts, two accounts on one login merged, and
// an hourly series for weekly windows, and a week restarted early closing the
// old one.
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
		// Max on this computer: a window read up to 3 minutes before its reset,
		// with a reset that jitters by under a second.
		read("device-claude", "max@example.com", "five_hour", 300, 10, ptr(at(5, 0)), at(1, 0)),
		read("device-claude", "max@example.com", "five_hour", 300, 15, &jitter, at(4, 57)),
		// The computer's login switches to Team; a Team account folder reads the
		// same login too.
		read("device-claude", "team@example.com", "five_hour", 300, 20, ptr(at(12, 0)), at(8, 0)),
		read("acct-team", "Team@Example.com", "five_hour", 300, 46, ptr(at(12, 0)), at(9, 0)),
		// Readings stop two hours before the next reset: at least 30%.
		read("device-claude", "team@example.com", "five_hour", 300, 30, ptr(at(18, 0)), at(16, 0)),
		read("device-claude", "team@example.com", "five_hour", 300, 5, ptr(at(23, 0)), at(19, 0)),
		// Nothing used yet: no reset, no window.
		read("device-claude", "team@example.com", "model:Fable", 10080, 0, nil, at(19, 0)),
		// A weekly window read twice in one hour and once in the next.
		read("device-claude", "team@example.com", "seven_day", 10080, 40, ptr(at(23, 0).AddDate(0, 0, 3)), at(18, 10)),
		read("device-claude", "team@example.com", "seven_day", 10080, 41, ptr(at(23, 0).AddDate(0, 0, 3)), at(18, 50)),
		read("device-claude", "team@example.com", "seven_day", 10080, 43, ptr(at(23, 0).AddDate(0, 0, 3)), at(19, 5)),
		// Codex restarts the week early: the old week, due to reset in four days,
		// ends when the new one is first read five minutes later.
		{AccountID: "device-codex", Provider: domain.AgentAccountProviderCodex, Email: "c@example.com", WindowID: "codex:Weekly",
			DurationMinutes: 10080, UsedPercent: 69, ResetsAt: ptr(at(6, 0).AddDate(0, 0, 4)), ReadAt: at(6, 30)},
		{AccountID: "device-codex", Provider: domain.AgentAccountProviderCodex, Email: "c@example.com", WindowID: "codex:Weekly",
			DurationMinutes: 10080, UsedPercent: 1, ResetsAt: ptr(at(6, 35).AddDate(0, 0, 7)), ReadAt: at(6, 35)},
	}
	reader := NewLimitHistoryReader(readings)
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
