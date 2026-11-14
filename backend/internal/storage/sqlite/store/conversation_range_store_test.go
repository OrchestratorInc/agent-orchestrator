package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// A client that holds the history reads only the rows after its floor.
func TestConversationSnapshotRangeReadsOnlyTheTail(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	for i, turnID := range []string{"turn-1", "turn-2", "turn-3"} {
		seedToolTurn(t, s, conversation, session, turnID, 2, histClock.Add(time.Duration(i)*time.Second))
	}
	whole, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if whole.AfterSequence != 0 {
		t.Fatalf("whole page echoes afterSequence %d", whole.AfterSequence)
	}
	floor := whole.Messages[4].Sequence - 1 // turn-3 prompt

	tail, err := s.LoadConversationSnapshotRange(ctx, conversation, 0, floor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(tail.Messages); !reflect.DeepEqual(got, []string{"turn-3 prompt", "turn-3 reply"}) {
		t.Fatalf("tail messages = %v", got)
	}
	if len(tail.Activities) != 2 || tail.HasMoreBefore || tail.AfterSequence != floor ||
		!reflect.DeepEqual(turnIDs(tail.Turns), []string{"turn-3"}) {
		t.Fatalf("tail activities=%d hasMore=%v after=%d turns=%v", len(tail.Activities), tail.HasMoreBefore, tail.AfterSequence, turnIDs(tail.Turns))
	}

	// More rows than the limit after the floor: hasMoreBefore reports the gap.
	gap, err := s.LoadConversationSnapshotRange(ctx, conversation, 0, whole.Messages[0].Sequence, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !gap.HasMoreBefore || gap.OldestSequence <= whole.Messages[0].Sequence {
		t.Fatalf("gap cursor = (%d, %v), want a gap above %d", gap.OldestSequence, gap.HasMoreBefore, whole.Messages[0].Sequence)
	}

	// One row: what a client reads to show the whole output of one activity.
	activity := tail.Activities[0]
	one, err := s.LoadConversationSnapshotRange(ctx, conversation, activity.Sequence+1, activity.Sequence-1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Activities) != 1 || one.Activities[0].ID != activity.ID || len(one.Messages) != 0 {
		t.Fatalf("one-row read = %d activities, %d messages", len(one.Activities), len(one.Messages))
	}
}
