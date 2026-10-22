package domain

import (
	"testing"
	"time"
)

func TestSessionLastEventAtTakesLatestTransition(t *testing.T) {
	base := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	session := func(activity time.Time, prs ...PRFacts) Session {
		var s Session
		s.CreatedAt = base
		s.UpdatedAt = at(999) // never an event: advances on metadata writes and polls
		s.Activity = Activity{State: ActivityIdle, LastActivityAt: activity}
		s.PRs = prs
		return s
	}

	tests := []struct {
		name string
		s    Session
		want time.Time
	}{
		{"no signal yet falls back to created", session(time.Time{}), base},
		{"activity transition", session(at(5)), at(5)},
		{"pr lifecycle", session(at(5), PRFacts{StateChangedAt: at(10)}), at(10)},
		{"ci change", session(at(5), PRFacts{StateChangedAt: at(10), CIChangedAt: at(20)}), at(20)},
		{"review submission", session(at(5), PRFacts{CIChangedAt: at(20), LastReviewAt: at(30)}), at(30)},
		{"latest across prs", session(at(5), PRFacts{LastReviewAt: at(30)}, PRFacts{CIChangedAt: at(40)}), at(40)},
		{"pr poll alone is not an event", session(at(5), PRFacts{UpdatedAt: at(50)}), at(5)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.LastEventAt(); !got.Equal(tt.want) {
				t.Fatalf("LastEventAt = %s, want %s", got, tt.want)
			}
		})
	}
}
