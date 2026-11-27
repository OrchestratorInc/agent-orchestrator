package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sqlite "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

const (
	leasedSent    = "attachment-0123456789abcdef0123456789abcdef.png"
	leasedDropped = "attachment-fedcba9876543210fedcba9876543210.txt"
)

func releasedNames(t *testing.T, s interface {
	ReleasedChatAttachmentLeases(context.Context, domain.SessionID) (map[domain.SessionID][]string, error)
}, id domain.SessionID) []string {
	t.Helper()
	released, err := s.ReleasedChatAttachmentLeases(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return released[id]
}

// Accepting a message that names a draft file commits it in the same write:
// a release that arrives afterwards (a late chip removal, a racing discard)
// leaves the accepted file alone, while the file the message did not name is
// still the draft's to give up.
func TestChatAttachmentLeaseCommittedByAcceptedMessage(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	for _, name := range []string{leasedSent, leasedDropped} {
		if err := s.LeaseChatAttachment(ctx, session, name, histClock); err != nil {
			t.Fatal(err)
		}
	}
	msg := domain.ConversationMessage{ID: "m1", Text: "Look at .ao/attachments/" + leasedSent, Origin: domain.MessageOriginHuman}
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", msg, "turn-1", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedSent, leasedDropped}, histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, want := releasedNames(t, s, session), []string{leasedDropped}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released = %v, want %v (the sent file must never be released)", got, want)
	}
}

// Content blocks count as references too: a message whose text is only a
// caption still holds the file its delivery content names.
func TestChatAttachmentLeaseCommittedByDeliveryContent(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if err := s.LeaseChatAttachment(ctx, session, leasedSent, histClock); err != nil {
		t.Fatal(err)
	}
	content := `[{"type":"resource_link","uri":".ao/attachments/` + leasedSent + `"}]`
	msg := domain.ConversationMessage{ID: "m1", Text: "What is this?", Origin: domain.MessageOriginHuman, DeliveryContentJSON: content}
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", msg, "turn-1", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedSent}, histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, session); len(got) != 0 {
		t.Fatalf("released = %v, want none", got)
	}
}

// Side chats accept messages outside the conversation tables and commit
// explicitly (hold, then settle as accepted), with the same effect.
func TestChatAttachmentLeaseExplicitCommit(t *testing.T) {
	s, session, _ := conversationFixture(t)
	ctx := context.Background()
	for _, name := range []string{leasedSent, leasedDropped} {
		if err := s.LeaseChatAttachment(ctx, session, name, histClock); err != nil {
			t.Fatal(err)
		}
	}
	held, err := s.HoldChatAttachmentLeases(ctx, session, []string{"see .ao/attachments/" + leasedSent, "", leasedSent})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{leasedSent}; !reflect.DeepEqual(held, want) {
		t.Fatalf("held = %v, want %v", held, want)
	}
	if err := s.SettleChatAttachmentHolds(ctx, session, held, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedSent, leasedDropped}, histClock); err != nil {
		t.Fatal(err)
	}
	if got, want := releasedNames(t, s, session), []string{leasedDropped}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released = %v, want %v", got, want)
	}
}

// A release that loses the race to acceptance is undone: until reclamation
// claims the row, accepting a message that names the file commits it, and the
// reclaim pass that listed it before the acceptance finds nothing to claim.
func TestChatAttachmentLeaseAcceptanceRescuesReleasedFile(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if err := s.LeaseChatAttachment(ctx, session, leasedDropped, histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedDropped}, histClock); err != nil {
		t.Fatal(err)
	}
	// Releasing again is a no-op, not an error.
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedDropped}, histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	msg := domain.ConversationMessage{ID: "m1", Text: ".ao/attachments/" + leasedDropped, Origin: domain.MessageOriginHuman}
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", msg, "turn-1", histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, session); len(got) != 0 {
		t.Fatalf("released = %v, want the accepted file rescued", got)
	}
	claimed, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("reclamation claimed a file named by an accepted message")
	}
}

// While reclamation is deleting a claimed file, any acceptance that names it
// is refused, through the conversation tables and the explicit commit alike.
// Once the file is gone the refusal ends: a later message that only mentions
// the name (say, pasted command output) is accepted.
func TestChatAttachmentLeaseClaimRefusesAcceptance(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if err := s.LeaseChatAttachment(ctx, session, leasedDropped, histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedDropped}, histClock); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(time.Second))
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	// A pass interrupted after its claim re-claims the row on the next run.
	if again, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(2*time.Second)); err != nil || !again {
		t.Fatalf("re-claim = %v, %v", again, err)
	}
	msg := domain.ConversationMessage{ID: "m1", Text: ".ao/attachments/" + leasedDropped, Origin: domain.MessageOriginHuman}
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", msg, "turn-1", histClock.Add(3*time.Second)); err == nil {
		t.Fatal("a message naming a claimed file was accepted")
	}
	if _, err := s.HoldChatAttachmentLeases(ctx, session, []string{".ao/attachments/" + leasedDropped}); !errors.Is(err, sqlite.ErrChatAttachmentDiscarded) {
		t.Fatalf("explicit commit = %v, want ErrChatAttachmentDiscarded", err)
	}
	if err := s.MarkChatAttachmentReclaimed(ctx, session, leasedDropped, histClock.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, session); len(got) != 0 {
		t.Fatalf("released = %v, want the reclaimed file no longer listed", got)
	}
	if claimed, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(5*time.Second)); err != nil || claimed {
		t.Fatalf("claim after reclaim = %v, %v", claimed, err)
	}
	mention := domain.ConversationMessage{ID: "m2", Text: "why did git status list .ao/attachments/" + leasedDropped + "?", Origin: domain.MessageOriginHuman}
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", mention, "turn-2", histClock.Add(6*time.Second)); err != nil {
		t.Fatalf("a mention of a reclaimed file was refused: %v", err)
	}
	if held, err := s.HoldChatAttachmentLeases(ctx, session, []string{leasedDropped}); err != nil || len(held) != 0 {
		t.Fatalf("hold naming a reclaimed file = %v, %v; want nothing held, no error", held, err)
	}
	// The tombstone outlives the mention, so a renewal still reports the file gone.
	if gone, err := s.RenewChatAttachmentLeases(ctx, session, []string{leasedDropped}, histClock.Add(7*time.Second)); err != nil || len(gone) != 1 {
		t.Fatalf("renew after reclaim = %v, %v; want the file reported gone", gone, err)
	}
	// A tombstone reclaimed after the cutoff stays; one before it goes.
	if err := s.PruneChatAttachmentTombstones(ctx, histClock); err != nil {
		t.Fatal(err)
	}
	if err := s.LeaseChatAttachment(ctx, session, leasedDropped, histClock.Add(time.Minute)); err == nil {
		t.Fatal("tombstone pruned before its cutoff")
	}
	if err := s.PruneChatAttachmentTombstones(ctx, histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.LeaseChatAttachment(ctx, session, leasedDropped, histClock.Add(2*time.Minute)); err != nil {
		t.Fatalf("pruned tombstone still holds its row: %v", err)
	}
}

// While a side question holding a file is being recorded, reclamation cannot
// claim it, even if the draft released it meanwhile. If the question is then
// not recorded, dropping its hold leaves the release in force.
func TestChatAttachmentHoldBlocksReclaimUntilSettled(t *testing.T) {
	s, session, _ := conversationFixture(t)
	ctx := context.Background()
	if err := s.LeaseChatAttachment(ctx, session, leasedDropped, histClock); err != nil {
		t.Fatal(err)
	}
	held, err := s.HoldChatAttachmentLeases(ctx, session, []string{leasedDropped})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedDropped}, histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(2*time.Second)); err != nil || claimed {
		t.Fatalf("claim of a held file = %v, %v", claimed, err)
	}
	if err := s.SettleChatAttachmentHolds(ctx, session, held, false); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(3*time.Second)); err != nil || !claimed {
		t.Fatalf("claim after the hold was dropped = %v, %v", claimed, err)
	}
}

// A file another message accepts while a side question holds it stays
// committed when that side question fails: dropping the hold must not
// re-lease it.
func TestChatAttachmentFailedHoldKeepsFileAcceptedMeanwhile(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	if err := s.LeaseChatAttachment(ctx, session, leasedSent, histClock); err != nil {
		t.Fatal(err)
	}
	held, err := s.HoldChatAttachmentLeases(ctx, session, []string{leasedSent})
	if err != nil {
		t.Fatal(err)
	}
	msg := domain.ConversationMessage{ID: "m1", Text: ".ao/attachments/" + leasedSent, Origin: domain.MessageOriginHuman}
	if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", msg, "turn-1", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleChatAttachmentHolds(ctx, session, held, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedSent}, histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, session); len(got) != 0 {
		t.Fatalf("released = %v: a failed side question re-leased an accepted file", got)
	}
}

// Holds are counted: one of two concurrent side questions failing does not
// drop the other's hold, and the one that is recorded commits the file.
func TestChatAttachmentConcurrentHolds(t *testing.T) {
	s, session, _ := conversationFixture(t)
	ctx := context.Background()
	if err := s.LeaseChatAttachment(ctx, session, leasedDropped, histClock); err != nil {
		t.Fatal(err)
	}
	first, err := s.HoldChatAttachmentLeases(ctx, session, []string{leasedDropped})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.HoldChatAttachmentLeases(ctx, session, []string{leasedDropped})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SettleChatAttachmentHolds(ctx, session, first, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseChatAttachmentLeases(ctx, session, []string{leasedDropped}, histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimChatAttachmentLease(ctx, session, leasedDropped, histClock.Add(2*time.Second)); err != nil || claimed {
		t.Fatalf("claim while the second hold is in flight = %v, %v", claimed, err)
	}
	if err := s.SettleChatAttachmentHolds(ctx, session, second, true); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, session); len(got) != 0 {
		t.Fatalf("released = %v, want the accepted file committed", got)
	}
}

// Renewal keeps a draft's files from expiring; an expired lease is released,
// and a later renewal reports it as gone.
func TestChatAttachmentLeaseRenewAndExpiry(t *testing.T) {
	s, session, _ := conversationFixture(t)
	ctx := context.Background()
	for _, name := range []string{leasedSent, leasedDropped} {
		if err := s.LeaseChatAttachment(ctx, session, name, histClock); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	if gone, err := s.RenewChatAttachmentLeases(ctx, session, []string{leasedSent}, histClock.Add(5*day)); err != nil || len(gone) != 0 {
		t.Fatalf("renew = %v, %v", gone, err)
	}
	if err := s.ExpireChatAttachmentLeases(ctx, session, histClock.Add(3*day), histClock.Add(10*day)); err != nil {
		t.Fatal(err)
	}
	if got, want := releasedNames(t, s, session), []string{leasedDropped}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released = %v, want only the unrenewed lease %v", got, want)
	}
	gone, err := s.RenewChatAttachmentLeases(ctx, session, []string{leasedSent, leasedDropped}, histClock.Add(10*day))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{leasedDropped}; !reflect.DeepEqual(gone, want) {
		t.Fatalf("renew reported gone = %v, want %v", gone, want)
	}
	// An empty session id expires every session's stale leases (startup).
	if err := s.ExpireChatAttachmentLeases(ctx, "", histClock.Add(20*day), histClock.Add(20*day)); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, session); len(got) != 2 {
		t.Fatalf("released = %v, want both", got)
	}
}

// Deleting the session deletes its leases with it.
func TestChatAttachmentLeasesGoWithTheirSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "del")
	rec := terminalSession(t, s, "del", 0)
	if err := s.LeaseChatAttachment(ctx, rec.ID, leasedDropped, histClock); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.DeleteTerminatedSession(ctx, rec.ID); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if err := s.ExpireChatAttachmentLeases(ctx, "", histClock.Add(time.Hour), histClock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := releasedNames(t, s, rec.ID); len(got) != 0 {
		t.Fatalf("lease outlived its session: %v", got)
	}
}
