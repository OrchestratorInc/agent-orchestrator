package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type missedHookStore map[domain.SessionID]domain.SessionRecord

func (s missedHookStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s[id]
	return rec, ok, nil
}

func TestReplayMissedHooksAppliesTheLastReportMadeWhileTheDaemonWasDown(t *testing.T) {
	dir := t.TempDir()
	const down = "AO daemon is not running — start it with `ao start`"
	log := "2026-10-11T12:57:30Z session=ended ao hooks claude-code pre-tool-use: " + down + "\n" +
		"2026-10-11T12:57:41Z session=ended ao hooks claude-code stop: " + down + "\n" +
		"2026-10-11T12:57:44Z session=ended ao hooks claude-code subagent-stop: " + down + "\n" +
		"2026-10-11T12:57:58Z session=ended ao hooks claude-code notification: " + down + "\n" +
		"2026-10-11T12:58:12Z session=codex ao hooks codex stop: " + down + "\n" +
		"2026-10-11T12:58:20Z session=working ao hooks claude-code user-prompt-submit: " + down + "\n" +
		"2026-10-11T12:58:30Z session=known ao hooks codex stop: " + down + "\n" +
		"2026-10-11T12:58:40Z session=gone ao hooks codex stop: " + down + "\n" +
		"2026-10-11T12:58:50Z session=other ao hooks codex stop: write session-start context: broken pipe\n"
	if err := os.WriteFile(filepath.Join(dir, "hooks.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 10, 11, 12, 50, 0, 0, time.UTC)
	active := func(at time.Time) domain.SessionRecord {
		return domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: at}, Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"}}
	}
	store := missedHookStore{
		"ended": active(before), "codex": active(before), "working": active(before), "other": active(before),
		// It has reported since: the logged stop is from an earlier launch.
		"known": active(before.Add(time.Hour)),
	}
	got := map[domain.SessionID]ports.ActivitySignal{}
	replayMissedHooks(context.Background(), dir, store, func(_ context.Context, id domain.SessionID, s ports.ActivitySignal) error {
		got[id] = s
		return nil
	})
	want := map[domain.SessionID]domain.ActivityState{"ended": domain.ActivityIdle, "codex": domain.ActivityIdle, "working": domain.ActivityActive}
	if len(got) != len(want) {
		t.Fatalf("applied to %d sessions, want %d: %+v", len(got), len(want), got)
	}
	for id, state := range want {
		if got[id].State != state || !got[id].Valid || got[id].LaunchID != "launch-1" {
			t.Errorf("%s: applied %q for launch %q, want %q for the running launch", id, got[id].State, got[id].LaunchID, state)
		}
	}
	if at := got["ended"].Timestamp; !at.Equal(time.Date(2026, 10, 11, 12, 57, 41, 0, time.UTC)) || got["ended"].Event != "stop" {
		t.Errorf("ended: applied %q at %s, want the stop at 12:57:41", got["ended"].Event, at)
	}
}

func TestReplayMissedHooksWithoutALogDoesNothing(t *testing.T) {
	replayMissedHooks(context.Background(), t.TempDir(), missedHookStore{}, func(context.Context, domain.SessionID, ports.ActivitySignal) error {
		t.Fatal("applied a report with no log")
		return nil
	})
}
