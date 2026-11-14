package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newSideTitleStore(t *testing.T) (*memorySideStore, domain.SideConversation) {
	t.Helper()
	ctx := context.Background()
	store := newMemorySideStore()
	if _, err := store.ClaimSideLaunch(ctx, "run", time.Now()); err != nil {
		t.Fatal(err)
	}
	side, _, err := store.CreateSideConversation(ctx, domain.SideConversation{
		ID: "side", AppRunID: "run", SessionID: "session", MainConversationID: "main", State: "opening",
		PolicyVersion: sidePolicyVersion, ProviderHostID: "btw-run-side",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, side
}

func TestSideFirstQuestionTitle(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"plain", "Explain this code", "Explain this code"},
		{"multiline", " \n # Explain  this\tcode!\nMore details", "Explain this code"},
		{"unicode", strings.Repeat("界", 90), strings.Repeat("界", 80)},
		{"unusable", "***", "Side Chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, side := newSideTitleStore(t)
			ctx := context.Background()
			now := time.Now().UTC()
			turn := domain.SideTurn{ID: "first", SideID: side.ID, ClientMessageID: "first", Text: tc.text, CreatedAt: now}
			if _, created, err := store.ReserveSideTurn(ctx, turn, "run"); err != nil || !created {
				t.Fatalf("reserve = %v, %v", created, err)
			}
			got, _ := store.SideConversation(ctx, side.ID)
			if got.Label != tc.want || got.ManualLabel {
				t.Fatalf("label = %q, manual = %v; want %q, false", got.Label, got.ManualLabel, tc.want)
			}
			if tc.want != "Side Chat" && !got.UpdatedAt.Equal(now) {
				t.Fatalf("title timestamp = %v, want %v", got.UpdatedAt, now)
			}
			// Retries, edits, cancellation, and subsequent messages never rename it.
			if _, created, err := store.ReserveSideTurn(ctx, turn, "run"); err != nil || created {
				t.Fatalf("duplicate = %v, %v", created, err)
			}
			turn.Text = "conflicting request"
			if _, _, err := store.ReserveSideTurn(ctx, turn, "run"); !errors.Is(err, ErrSideIdempotencyConflict) {
				t.Fatalf("conflict = %v", err)
			}
			if err := store.EditQueuedSideTurn(ctx, side.ID, "first", "Edited question"); err != nil {
				t.Fatal(err)
			}
			if err := store.CancelSideTurn(ctx, side.ID, "first", now); err != nil {
				t.Fatal(err)
			}
			turn.ID, turn.ClientMessageID = "second", "second"
			if _, _, err := store.ReserveSideTurn(ctx, turn, "run"); err != nil {
				t.Fatal(err)
			}
			got, _ = store.SideConversation(ctx, side.ID)
			if got.Label != tc.want || got.ManualLabel {
				t.Fatalf("label changed after later activity: %+v", got)
			}
		})
	}
}

func TestSideTitleManualRenameAndRecovery(t *testing.T) {
	for _, timing := range []string{"before", "after", "concurrent", "automatic"} {
		t.Run(timing, func(t *testing.T) {
			store, side := newSideTitleStore(t)
			ctx := context.Background()
			now := time.Now().UTC()
			rename := func() {
				if err := store.RenameSide(ctx, side.ID, "My title", now); err != nil {
					t.Error(err)
				}
			}
			send := func() {
				if _, _, err := store.ReserveSideTurn(ctx, domain.SideTurn{ID: "first", SideID: side.ID, ClientMessageID: "first", Text: "Explain this code", CreatedAt: now}, "run"); err != nil {
					t.Error(err)
				}
			}
			switch timing {
			case "before":
				rename()
				send()
			case "after":
				send()
				rename()
			case "concurrent":
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); rename() }()
				go func() { defer wg.Done(); send() }()
				wg.Wait()
			default:
				send()
			}
			want, manual := "My title", true
			if timing == "automatic" {
				want, manual = "Explain this code", false
			}
			got, _ := store.SideConversation(ctx, side.ID)
			if got.Label != want || got.ManualLabel != manual {
				t.Fatalf("label = %q, manual = %v", got.Label, got.ManualLabel)
			}
			after := newMemorySideStore()
			if _, err := after.ClaimSideLaunch(ctx, "run", now); err != nil {
				t.Fatal(err)
			}
			if _, err := after.recover("run", store.export("run"), now); err != nil {
				t.Fatal(err)
			}
			got, err := after.SideConversation(ctx, side.ID)
			if err != nil || got.Label != want || got.ManualLabel != manual {
				t.Fatalf("recovered title = %+v, %v", got, err)
			}
		})
	}
}

func TestSideTitleRejectedAndExistingTurns(t *testing.T) {
	ctx := context.Background()
	store, side := newSideTitleStore(t)
	turn := domain.SideTurn{ID: "first", SideID: side.ID, ClientMessageID: "first", Text: "Explain this code"}
	if _, _, err := store.ReserveSideTurn(ctx, turn, "wrong-run"); !errors.Is(err, ErrSideClosed) {
		t.Fatalf("wrong launch = %v", err)
	}
	side.State = "failed"
	store.sides[side.ID] = side
	if _, _, err := store.ReserveSideTurn(ctx, turn, "run"); !errors.Is(err, ErrSideNotReady) {
		t.Fatalf("failed side = %v", err)
	}
	got, _ := store.SideConversation(ctx, side.ID)
	if got.Label != "Side Chat" {
		t.Fatalf("rejected send renamed tab: %q", got.Label)
	}
	side.State = "opening"
	store.sides[side.ID] = side
	store.turns[side.ID] = []domain.SideTurn{{ID: "old", ClientMessageID: "old"}}
	if _, _, err := store.ReserveSideTurn(ctx, turn, "run"); err != nil {
		t.Fatal(err)
	}
	got, _ = store.SideConversation(ctx, side.ID)
	if got.Label != "Side Chat" {
		t.Fatalf("existing side was backfilled: %q", got.Label)
	}
}

func TestSideTitleAttachmentOnlyQuestion(t *testing.T) {
	store, side := newSideTitleStore(t)
	svc := New(Options{AppRunID: "run", Sessions: sideSessionReader{}, NewID: func() string { return "attachment-turn" }})
	svc.sides.store = store
	if _, err := svc.SendSideQuestion(context.Background(), side.SessionID, side.ID, ports.ChatUserMessage{
		ClientMessageID: "attachment", Content: []ports.ChatContent{{Type: "text", Text: "attached context"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.SideConversation(context.Background(), side.ID)
	if got.Label != "Attached 1 item(s) for context" || got.ManualLabel {
		t.Fatalf("attachment title = %+v", got)
	}
}
