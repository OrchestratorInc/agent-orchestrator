//go:build !windows

package e2e

import (
	"net/http"
	"testing"
	"time"
)

func TestChatUsageIsReportedWithAContextWindow(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "usage")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	snap := d.awaitConversation(session, 3*time.Minute, "the provider to report token usage",
		func(s snapshot) bool { return s.Usage != nil && s.Usage.ContextUsed > 0 })

	usage := snap.Usage
	if usage.ContextWindow <= 0 {
		t.Fatalf("context window = %d; without it the readout is a number with no scale",
			usage.ContextWindow)
	}
	if usage.ContextUsed > usage.ContextWindow {
		t.Errorf("context used %d exceeds the window %d; the running total is being read as the context position",
			usage.ContextUsed, usage.ContextWindow)
	}
	if fraction := float64(usage.ContextUsed) / float64(usage.ContextWindow); fraction > 0.5 {
		t.Errorf("a single opening turn reports the context %.0f%% full", fraction*100)
	}
	if usage.TotalTokens <= 0 {
		t.Errorf("cumulative total = %d, want the conversation's spend", usage.TotalTokens)
	}
}

func TestChatUsageIsLatestWinsAndStaysOutOfTheTimeline(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "usagestate")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	first := d.awaitConversation(session, 3*time.Minute, "the first usage report",
		func(s snapshot) bool { return s.Usage != nil && s.Usage.ContextUsed > 0 })
	before := first.Usage.ContextUsed

	send(t, d, session,
		"Run `ls`, then `pwd`, then `date`, each as a separate shell command, then reply with exactly: WALKED",
		"usage-walk")

	snap := d.awaitConversation(session, 5*time.Minute, "the multi-command turn to settle",
		func(s snapshot) bool { return terminal(s.Turns[len(s.Turns)-1].State) })

	for _, a := range snap.Activities {
		if a.Kind == "usage" {
			t.Fatalf("usage was written to the timeline as activity %s; one row per tool call is what buried the conversation", a.ID)
		}
	}

	if snap.Usage == nil {
		t.Fatal("usage disappeared after a turn that produced several reports")
	}
	if snap.Usage.ContextUsed <= before {
		t.Errorf("context used did not advance across a turn: %d then %d",
			before, snap.Usage.ContextUsed)
	}
	if snap.Usage.ContextUsed > snap.Usage.ContextWindow {
		t.Errorf("context used %d exceeds the window %d", snap.Usage.ContextUsed, snap.Usage.ContextWindow)
	}
}

func TestChatRateLimitsAreReportedForTheAccount(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "ratelimits")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	snap := d.awaitConversation(session, 3*time.Minute, "the account's quota position",
		func(s snapshot) bool { return s.RateLimits != nil })

	limits := snap.RateLimits
	if limits.PrimaryUsedPercent > 100 {
		t.Errorf("primary window = %v; a percentage cannot exceed 100, so an absolute is leaking through",
			limits.PrimaryUsedPercent)
	}
	if limits.PrimaryUsedPercent >= 0 && limits.SecondaryUsedPercent > 100 {
		t.Errorf("secondary window = %v, want a percentage or a negative", limits.SecondaryUsedPercent)
	}
	if limits.PrimaryUsedPercent < 0 && limits.SecondaryUsedPercent < 0 {
		t.Error("neither window was reported, so the quota warning can never fire")
	}
	const tenYears = int64(10 * 365 * 24 * 60 * 60)
	if limits.PrimaryResetsInSeconds > tenYears {
		t.Errorf("primary resets in %d seconds; an absolute timestamp was not converted to a duration",
			limits.PrimaryResetsInSeconds)
	}
	if limits.PrimaryResetsInSeconds < 0 || limits.SecondaryResetsInSeconds < 0 {
		t.Errorf("negative reset durations (%d, %d); a window cannot refill in the past",
			limits.PrimaryResetsInSeconds, limits.SecondaryResetsInSeconds)
	}
}

func TestChatUsageSurvivesADaemonRestart(t *testing.T) {
	requireE2E(t)
	dir := t.TempDir()
	d := startDaemon(t, dir)
	project := seedProject(t, d, "usagerestart")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	before := d.awaitConversation(session, 3*time.Minute, "usage to be recorded",
		func(s snapshot) bool { return s.Usage != nil && s.Usage.ContextUsed > 0 })

	d.stop()
	restarted := startDaemon(t, dir)

	var after snapshot
	restarted.mustCall("GET", "/sessions/"+session+"/conversation", http.StatusOK, nil, &after)

	if after.Usage == nil {
		t.Fatal("usage was lost across a restart; it is conversation state, not controller state")
	}
	if after.Usage.ContextUsed != before.Usage.ContextUsed {
		t.Errorf("context used changed across a restart: %d then %d",
			before.Usage.ContextUsed, after.Usage.ContextUsed)
	}
	if after.Usage.ContextWindow != before.Usage.ContextWindow {
		t.Errorf("context window changed across a restart: %d then %d",
			before.Usage.ContextWindow, after.Usage.ContextWindow)
	}
	if before.RateLimits != nil {
		if after.RateLimits == nil {
			t.Fatal("rate limits were lost across a restart")
		}
		if (before.RateLimits.SecondaryUsedPercent < 0) != (after.RateLimits.SecondaryUsedPercent < 0) {
			t.Errorf("an unreported window changed meaning across a restart: %v then %v",
				before.RateLimits.SecondaryUsedPercent, after.RateLimits.SecondaryUsedPercent)
		}
	}
}
