package store_test

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// timestampZones are the zones a hook or API caller can stamp a time in. A zone
// without a name (a JSON offset the daemon is not itself in) is stored by the
// driver as "+0200 +0200", which it cannot read back (#7026).
func timestampZones(t *testing.T) []struct {
	name string
	loc  *time.Location
} {
	t.Helper()
	load := func(name string) *time.Location {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		return loc
	}
	return []struct {
		name string
		loc  *time.Location
	}{
		{"UTC", time.UTC},
		{"named zone with daylight saving (CEST)", load("Europe/Rome")},
		{"named zone west of UTC (EDT)", load("America/New_York")},
		{"half-hour zone (IST)", load("Asia/Kolkata")},
		{"45-minute zone (NPT)", load("Asia/Kathmandu")},
		{"negative half-hour zone (NDT)", load("America/St_Johns")},
		{"widest zone (+14:00)", load("Pacific/Kiritimati")},
		// Real zones whose abbreviation is a four-digit offset: the daemon's own clock
		// writes the unreadable "+0545 +0545" there (#6890).
		{"Nepal (+0545 abbreviation)", load("Asia/Kathmandu")},
		{"Iran (+0330 abbreviation)", load("Asia/Tehran")},
		{"Afghanistan (+0430 abbreviation)", load("Asia/Kabul")},
		{"Myanmar (+0630 abbreviation)", load("Asia/Yangon")},
		{"Sri Lanka (+0530 abbreviation)", load("Asia/Colombo")},
		{"Chatham Islands (+1345 abbreviation)", load("Pacific/Chatham")},
		{"two-digit abbreviation (+04)", load("Asia/Dubai")},
		{"two-digit negative abbreviation (-03)", load("America/Sao_Paulo")},
		{"offset only +02:00", time.FixedZone("", 2*3600)},
		{"offset only -07:00", time.FixedZone("", -7*3600)},
		{"offset only +05:30", time.FixedZone("", 5*3600+30*60)},
		{"offset only +05:45", time.FixedZone("", 5*3600+45*60)},
		{"offset only -03:30", time.FixedZone("", -(3*3600 + 30*60))},
		{"offset only +14:00", time.FixedZone("", 14*3600)},
	}
}

func assertUTCInstant(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s changed the instant: wrote %s, read %s", what, want, got)
	}
	if _, offset := got.Zone(); offset != 0 {
		t.Fatalf("%s read back with offset %d, want UTC", what, offset)
	}
}

// Every time on a session row must come back after a write in any zone, and
// listing all sessions (what daemon boot does) must not fail.
func TestSessionTimesStayReadableInEveryZone(t *testing.T) {
	for _, zone := range timestampZones(t) {
		t.Run(zone.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "tz")
			at := time.Date(2026, time.October, 21, 18, 15, 40, 647754000, zone.loc)
			rec := sampleRecord("tz")
			rec.CreatedAt, rec.UpdatedAt = at, at
			rec.FirstSignalAt = at
			rec.Activity.LastActivityAt = at
			rec.Metadata.NativeIdentityObservedAt = at
			rec.Metadata.LatestUserPromptAt = at
			rec.Metadata.LatestAssistantUpdateAt = at
			created, err := s.CreateSession(ctx, rec)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if _, err := s.ListAllSessions(ctx); err != nil {
				t.Fatalf("list all sessions after writing %q: %v", at.String(), err)
			}
			got, ok, err := s.GetSession(ctx, created.ID)
			if err != nil || !ok {
				t.Fatalf("get: ok=%v err=%v", ok, err)
			}
			assertUTCInstant(t, "native_identity_observed_at", got.Metadata.NativeIdentityObservedAt, at)
			assertUTCInstant(t, "latest_user_prompt_at", got.Metadata.LatestUserPromptAt, at)
			assertUTCInstant(t, "latest_assistant_update_at", got.Metadata.LatestAssistantUpdateAt, at)
			assertUTCInstant(t, "first_signal_at", got.FirstSignalAt, at)
		})
	}
}

// The hook path rewrites the same columns through the narrow activity update.
func TestActivityProjectionKeepsEveryZoneReadable(t *testing.T) {
	for _, zone := range timestampZones(t) {
		t.Run(zone.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "tz")
			created, err := s.CreateSession(ctx, sampleRecord("tz"))
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			rec, ok, err := s.GetSession(ctx, created.ID)
			if err != nil || !ok {
				t.Fatalf("get: ok=%v err=%v", ok, err)
			}
			at := time.Date(2026, time.October, 21, 18, 15, 40, 647754000, zone.loc)
			rec.UpdatedAt = at
			rec.FirstSignalAt = at
			rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: at}
			rec.Metadata.AgentSessionID = "native-1"
			rec.Metadata.NativeIdentityObservedAt = at
			rec.Metadata.LatestUserPrompt = "hello"
			rec.Metadata.LatestUserPromptAt = at
			rec.Metadata.LatestAssistantUpdate = "done"
			rec.Metadata.LatestAssistantUpdateAt = at
			applied, err := s.UpdateSessionFromActivitySignal(ctx, rec, rec.Revision)
			if err != nil || !applied {
				t.Fatalf("project activity: applied=%v err=%v", applied, err)
			}
			if _, err := s.ListAllSessions(ctx); err != nil {
				t.Fatalf("list all sessions after projecting %q: %v", at.String(), err)
			}
			got, _, err := s.GetSession(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertUTCInstant(t, "native_identity_observed_at", got.Metadata.NativeIdentityObservedAt, at)
			assertUTCInstant(t, "latest_user_prompt_at", got.Metadata.LatestUserPromptAt, at)
			assertUTCInstant(t, "latest_assistant_update_at", got.Metadata.LatestAssistantUpdateAt, at)
			assertUTCInstant(t, "activity_last_at", got.Activity.LastActivityAt, at)
		})
	}
}

// A Stop closes the turn with the hook's time (session_terminal_turns.responded_at).
func TestTerminalTurnTimesStayReadableInEveryZone(t *testing.T) {
	for _, zone := range timestampZones(t) {
		t.Run(zone.name, func(t *testing.T) {
			s, session := newTerminalTurnSession(t, "tz")
			ctx := context.Background()
			at := time.Date(2026, time.October, 21, 18, 15, 40, 647754000, zone.loc)
			if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
				SessionID: session.ID, Harness: domain.HarnessClaudeCode, NativeSessionID: "native-1",
				UserRequest: "ask", RequestedAt: at,
			}); err != nil {
				t.Fatalf("record request: %v", err)
			}
			if err := s.RecordTerminalTurnResponse(ctx, session.ID, "answer", at.Add(time.Minute)); err != nil {
				t.Fatalf("record response: %v", err)
			}
			turns, err := s.ListTerminalTurns(ctx, session.ID)
			if err != nil || len(turns) != 1 || turns[0].AgentResponse != "answer" {
				t.Fatalf("list turns after writing %q: turns=%+v err=%v", at.String(), turns, err)
			}
		})
	}
}

// #6890: registering a project stamps registered_at with the daemon's own clock.
// In Nepal, Iran, Afghanistan, Myanmar and Sri Lanka that text is unreadable, and
// every project listing failed with PROJECTS_LIST_FAILED.
func TestProjectTimesStayReadableInEveryZone(t *testing.T) {
	for _, zone := range timestampZones(t) {
		t.Run(zone.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			at := time.Date(2026, time.October, 18, 13, 52, 41, 765189000, zone.loc)
			if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: at}); err != nil {
				t.Fatalf("register project: %v", err)
			}
			projects, err := s.ListProjects(ctx)
			if err != nil || len(projects) != 1 {
				t.Fatalf("list projects after writing %q: %v (err %v)", at.String(), projects, err)
			}
			assertUTCInstant(t, "registered_at", projects[0].RegisteredAt, at)
		})
	}
}
