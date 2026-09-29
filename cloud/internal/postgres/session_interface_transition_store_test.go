package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestInterruptTransitionFinishesQueuedChatTurns(t *testing.T) {
	for _, policy := range []domain.SessionInterfaceTransitionPolicy{
		domain.SessionInterfaceTransitionInterrupt,
		domain.SessionInterfaceTransitionDrain,
	} {
		t.Run(string(policy), func(t *testing.T) {
			store, _, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
			if err := store.withTenant(ctx, principal, fixture.orgID, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE ao_sessions SET interface = 'chat'
					WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"first queued turn", "second queued turn"} {
				if _, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, text, text, domain.ChatTurnSettings{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.StartSessionInterfaceTransition(ctx, principal, fixture.orgID,
				fixture.sessionID, domain.SessionInterfaceChat, domain.SessionInterfaceTUI, policy, ""); err != nil {
				t.Fatal(err)
			}
			var queued, interrupted int
			if err := store.withTenant(ctx, principal, fixture.orgID, func(tx pgx.Tx) error {
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_turns
					WHERE org_id = $1 AND session_id = $2 AND state = 'queued'`, fixture.orgID, fixture.sessionID).Scan(&queued); err != nil {
					return err
				}
				return tx.QueryRow(ctx, `SELECT count(*) FROM ao_events
					WHERE org_id = $1 AND session_id = $2 AND type = 'chat.turn_interrupted'`, fixture.orgID, fixture.sessionID).Scan(&interrupted)
			}); err != nil {
				t.Fatal(err)
			}
			if policy == domain.SessionInterfaceTransitionInterrupt && (queued != 0 || interrupted != 2) {
				t.Fatalf("interrupt left queued=%d, interrupted=%d; want 0, 2", queued, interrupted)
			}
			if policy == domain.SessionInterfaceTransitionDrain && (queued != 2 || interrupted != 0) {
				t.Fatalf("drain left queued=%d, interrupted=%d; want 2, 0", queued, interrupted)
			}
		})
	}
}

func TestStartSessionInterfaceTransitionRejectsInvalidModesBeforeDatabaseAccess(t *testing.T) {
	store := &Store{}
	for _, tc := range []struct {
		name   string
		source domain.SessionInterface
		target domain.SessionInterface
	}{
		{name: "unknown source", source: "unknown", target: domain.SessionInterfaceChat},
		{name: "unknown target", source: domain.SessionInterfaceTUI, target: "unknown"},
		{name: "same mode", source: domain.SessionInterfaceTUI, target: domain.SessionInterfaceTUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.StartSessionInterfaceTransition(
				context.Background(), domain.Principal{}, "org", "session",
				tc.source, tc.target, domain.SessionInterfaceTransitionDrain, "",
			)
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("start error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestActiveTransitionConstraintReportsTransitionInProgress(t *testing.T) {
	err := normalizeConstraintError(&pgconn.PgError{
		Code:           "23505",
		ConstraintName: "ao_interface_transitions_one_active",
	})
	if !errors.Is(err, ErrTransitionInProgress) {
		t.Fatalf("duplicate active transition error = %v, want ErrTransitionInProgress", err)
	}
}

func TestLatestRelevantTransitionDoesNotResurfaceFailureAfterSuccess(t *testing.T) {
	// The store selects the newest attempt before applying this rule. A
	// completed attempt supersedes an earlier failed notice.
	for _, tc := range []struct {
		phase domain.SessionInterfaceTransitionPhase
		want  bool
	}{
		{domain.SessionInterfaceTransitionRequested, true},
		{domain.SessionInterfaceTransitionFailed, true},
		{domain.SessionInterfaceTransitionRecovery, true},
		{domain.SessionInterfaceTransitionCompleted, false},
		{domain.SessionInterfaceTransitionCancelled, false},
	} {
		if got := latestTransitionIsRelevant(tc.phase); got != tc.want {
			t.Errorf("phase %q relevant = %v, want %v", tc.phase, got, tc.want)
		}
	}
}
