package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
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

func TestStartTransitionWaitsForMessageAdmission(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if err := store.withTenant(ctx, principal, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET interface = 'chat'
			WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	messageTx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer messageTx.Rollback(context.Background())
	if _, err := messageTx.Exec(ctx,
		`SELECT set_config('ao.user_id', $1, true), set_config('ao.org_id', $2, true)`,
		fixture.userID, fixture.orgID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := sendMessageTx(ctx, messageTx, fixture.orgID, fixture.sessionID,
		"concurrent-message", "queued before switch", fixture.userID, "", "", nil,
		domain.ChatTurnSettings{}, true); err != nil {
		t.Fatal(err)
	}

	transitionDone := make(chan error, 1)
	go func() {
		_, err := store.StartSessionInterfaceTransition(ctx, principal, fixture.orgID,
			fixture.sessionID, domain.SessionInterfaceChat, domain.SessionInterfaceTUI,
			domain.SessionInterfaceTransitionInterrupt, "")
		transitionDone <- err
	}()
	select {
	case err := <-transitionDone:
		t.Fatalf("transition started before message committed: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := messageTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-transitionDone:
		if err != nil {
			t.Fatalf("start transition: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("transition did not resume after message committed")
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
	if queued != 0 || interrupted != 1 {
		t.Fatalf("after interrupt: queued=%d, interrupted=%d; want 0, 1", queued, interrupted)
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

func TestClaimedTransitionCarriesProviderActivity(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	transition, err := store.StartSessionInterfaceTransition(ctx, principal, fixture.orgID,
		fixture.sessionID, domain.SessionInterfaceTUI, domain.SessionInterfaceChat,
		domain.SessionInterfaceTransitionDrain, "")
	if err != nil {
		t.Fatal(err)
	}
	claim := func(state string, sourceRequest any) CoordinatedInterfaceTransition {
		t.Helper()
		if _, err := admin.Exec(ctx, `UPDATE ao_sessions
			SET activity_state = $3, activity_source_request_id = $4
			WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID, state, sourceRequest); err != nil {
			t.Fatal(err)
		}
		// Other tests may leave transitions behind; a short lease keeps every
		// claim here immediately reclaimable by them and by the next call.
		claimed, err := store.ClaimCoordinatedInterfaceTransitions(ctx, "activity-test", 1000, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range claimed {
			if candidate.ID == transition.ID {
				return candidate
			}
		}
		t.Fatalf("transition %s was not claimed", transition.ID)
		return CoordinatedInterfaceTransition{}
	}

	if got := claim("active", uuid.NewString()); got.SessionActivityState != "active" || !got.SessionActivityUnconfirmed {
		t.Fatalf("typed-message activity = %q unconfirmed=%t, want active/true", got.SessionActivityState, got.SessionActivityUnconfirmed)
	}
	time.Sleep(5 * time.Millisecond)
	if got := claim("idle", nil); got.SessionActivityState != "idle" || got.SessionActivityUnconfirmed || got.SessionTerminated {
		t.Fatalf("hook-reported activity = %q unconfirmed=%t terminated=%t, want idle/false/false",
			got.SessionActivityState, got.SessionActivityUnconfirmed, got.SessionTerminated)
	}
	// A deleted session's handoff is still claimed, carrying the deletion, so
	// the coordinator can end it instead of waiting for a worker forever.
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true
		WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if got := claim("exited", nil); !got.SessionTerminated {
		t.Fatal("claimed transition of a deleted session does not report it terminated")
	}
}

func TestResumeDeletedSessionReportsTermination(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true
		WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := store.ResumeSession(ctx, principal, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrSessionTerminated) {
		t.Fatalf("resume deleted session error = %v, want ErrSessionTerminated", err)
	}
}

// A recovery_required handoff carries the session's live worker epoch (0 once
// it is gone), and a handoff of a deleted session ends with each prompt it
// held shown as a failed turn ("not sent because the session was deleted"),
// never queued, so a restored session does not run it unasked.
func TestTerminatedHandoffEndsWithItsHeldPromptsNotSent(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	asTenant := func(query string, args ...any) {
		t.Helper()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true), set_config('ao.org_id', $2, true)`, fixture.userID, fixture.orgID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	transition, err := store.StartSessionInterfaceTransition(ctx, principal, fixture.orgID,
		fixture.sessionID, domain.SessionInterfaceTUI, domain.SessionInterfaceChat,
		domain.SessionInterfaceTransitionInterrupt, "")
	if err != nil {
		t.Fatal(err)
	}
	held, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, "held-"+uuid.NewString(), "run the tests", domain.ChatTurnSettings{})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(held.Payload, &payload); err != nil || payload.TurnID == "" {
		t.Fatalf("held message carries no turn id: %s (%v)", held.Payload, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_interface_transitions
		SET phase = 'recovery_required', error_code = 'SOURCE_DRAIN_FAILED' WHERE id = $1`, transition.ID); err != nil {
		t.Fatal(err)
	}
	claim := func() CoordinatedInterfaceTransition {
		t.Helper()
		claimed, err := store.ClaimCoordinatedInterfaceTransitions(ctx, "terminated-test", 1000, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range claimed {
			if candidate.ID == transition.ID {
				return candidate
			}
		}
		t.Fatalf("transition %s was not claimed", transition.ID)
		return CoordinatedInterfaceTransition{}
	}
	if got := claim(); got.LiveWorkerEpoch != fixture.epoch {
		t.Fatalf("live worker epoch = %d, want %d", got.LiveWorkerEpoch, fixture.epoch)
	}
	asTenant(`UPDATE ao_worker_connections SET disconnected_at = now() WHERE session_id = $1`, fixture.sessionID)
	time.Sleep(5 * time.Millisecond)
	if got := claim(); got.LiveWorkerEpoch != 0 {
		t.Fatalf("live worker epoch with no worker = %d, want 0", got.LiveWorkerEpoch)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true WHERE org_id = $1 AND id = $2`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	got := claim()
	if err := store.FailTerminatedInterfaceTransition(ctx, "terminated-test", got.ID, got.Phase, "SESSION_TERMINATED", "deleted"); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTerminatedInterfaceTransition(ctx, "terminated-test", got.ID, got.Phase, "SESSION_TERMINATED", "deleted"); !errors.Is(err, ErrTransitionStale) {
		t.Fatalf("second end = %v, want ErrTransitionStale", err)
	}
	var state, message, phase string
	var aborted, queued, undelivered int
	if err := store.withTenant(ctx, principal, fixture.orgID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT state, error_message FROM ao_turns WHERE org_id = $1 AND id = $2`,
			fixture.orgID, payload.TurnID).Scan(&state, &message); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_turns WHERE org_id = $1 AND session_id = $2 AND state = 'queued'`,
			fixture.orgID, fixture.sessionID).Scan(&queued); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_events WHERE org_id = $1 AND session_id = $2
			AND type = 'chat.turn_aborted' AND payload->>'turnId' = $3 AND payload->>'error' = $4`,
			fixture.orgID, fixture.sessionID, payload.TurnID, TerminatedInterfaceMessageError).Scan(&aborted); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ao_interface_transition_messages
			WHERE org_id = $1 AND transition_id = $2 AND delivered_at IS NULL`, fixture.orgID, transition.ID).Scan(&undelivered); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT phase FROM ao_interface_transitions WHERE id = $1`, transition.ID).Scan(&phase)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || message != TerminatedInterfaceMessageError || aborted != 1 || queued != 0 || undelivered != 0 || phase != "failed" {
		t.Fatalf("turn %s/%q, aborted events %d, queued %d, undelivered %d, phase %s", state, message, aborted, queued, undelivered, phase)
	}
}

// Completing a handoff delivers every prompt it held as a queued turn. Two
// held prompts prove the delivery never writes while its read is still open
// (pgx rejects that as "conn busy" and the handoff stayed activating forever).
func TestCompletedHandoffDeliversEveryHeldPrompt(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	transition, err := store.StartSessionInterfaceTransition(ctx, principal, fixture.orgID,
		fixture.sessionID, domain.SessionInterfaceTUI, domain.SessionInterfaceChat,
		domain.SessionInterfaceTransitionInterrupt, "")
	if err != nil {
		t.Fatal(err)
	}
	var turnIDs []string
	for _, text := range []string{"run the tests", "then open a PR"} {
		held, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, "held-"+uuid.NewString(), text, domain.ChatTurnSettings{})
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			TurnID string `json:"turnId"`
		}
		if err := json.Unmarshal(held.Payload, &payload); err != nil || payload.TurnID == "" {
			t.Fatalf("held message carries no turn id: %s (%v)", held.Payload, err)
		}
		turnIDs = append(turnIDs, payload.TurnID)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_interface_transitions
		SET phase = 'activating', claimed_by = 'deliver-test', claimed_at = now() WHERE id = $1`, transition.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCoordinatedInterfaceTransition(ctx, "deliver-test", transition.ID); err != nil {
		t.Fatalf("complete handoff with two held prompts: %v", err)
	}
	var phase string
	var undelivered int
	if err := admin.QueryRow(ctx, `SELECT phase FROM ao_interface_transitions WHERE id = $1`, transition.ID).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if phase != "completed" {
		t.Fatalf("handoff phase = %q, want completed", phase)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_interface_transition_messages
		WHERE transition_id = $1 AND delivered_at IS NULL`, transition.ID).Scan(&undelivered); err != nil {
		t.Fatal(err)
	}
	if undelivered != 0 {
		t.Fatalf("undelivered held prompts = %d, want 0", undelivered)
	}
	for _, turnID := range turnIDs {
		var state string
		if err := admin.QueryRow(ctx, `SELECT state FROM ao_turns WHERE org_id = $1 AND id = $2`, fixture.orgID, turnID).Scan(&state); err != nil {
			t.Fatalf("held prompt %s has no turn: %v", turnID, err)
		}
		if state != "queued" {
			t.Fatalf("held prompt %s turn state = %q, want queued", turnID, state)
		}
	}
}

// A new Chat session switched to the terminal before its first turn started
// runs its task exactly once, through the terminal's turn forwarder: its
// terminal launches without the prompt, and the switch (drain or stop now)
// leaves the never-claimed task turn queued for the forwarder. A message sent
// after it stays queued on a drain and is cancelled on a stop now, as any
// queued message is. Once Chat claimed a turn every queued turn stays on a
// drain. A session whose launch still carries the prompt (created before Chat
// sessions launched without it) gets it on the command line, so its task turn
// is settled at the commit instead, and is never typed again.
func TestSwitchToTerminalRunsTheSessionTaskOnce(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	rlsStore := nonBypassStore(t, store, admin)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	switchToTerminal := func(sessionID string, policy domain.SessionInterfaceTransitionPolicy) {
		t.Helper()
		transition, err := store.StartSessionInterfaceTransition(ctx, principal, fixture.orgID, sessionID,
			domain.SessionInterfaceChat, domain.SessionInterfaceTUI, policy, "")
		if err != nil {
			t.Fatal(err)
		}
		owner := "switch-test-" + uuid.NewString()
		if _, err := admin.Exec(ctx, `UPDATE ao_interface_transitions SET phase = 'source_stopped', claimed_by = $2, claimed_at = now()
			WHERE id = $1`, transition.ID, owner); err != nil {
			t.Fatal(err)
		}
		if committed, err := rlsStore.CommitCoordinatedSessionInterface(ctx, owner, fixture.orgID, transition.ID, domain.SessionInterfaceTUI); err != nil || !committed {
			t.Fatalf("commit = %v, %v", committed, err)
		}
	}
	turnStates := func(sessionID string) []string {
		t.Helper()
		rows, err := admin.Query(ctx, `SELECT state FROM ao_turns WHERE session_id = $1 ORDER BY user_message_sequence`, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		states, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return states
	}
	launchPrompt := func(sessionID string) string {
		t.Helper()
		launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if launch.Interface != domain.SessionInterfaceTUI {
			t.Fatalf("launch interface = %q, want the TUI", launch.Interface)
		}
		return launch.Prompt
	}
	create := func() domain.Session {
		t.Helper()
		session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "do the work",
			Provider: "docker", Interface: domain.SessionInterfaceChat,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SendMessage(ctx, principal, fixture.orgID, session.ID, uuid.NewString(), "and then this", domain.ChatTurnSettings{}); err != nil {
			t.Fatal(err)
		}
		return session
	}

	for _, tc := range []struct {
		policy domain.SessionInterfaceTransitionPolicy
		want   []string
	}{
		{domain.SessionInterfaceTransitionDrain, []string{"queued", "queued"}},
		{domain.SessionInterfaceTransitionInterrupt, []string{"queued", "completed"}},
	} {
		fresh := create()
		switchToTerminal(fresh.ID, tc.policy)
		if states := turnStates(fresh.ID); !slices.Equal(states, tc.want) {
			t.Fatalf("%s: turn states after the switch = %v, want %v", tc.policy, states, tc.want)
		}
		if prompt := launchPrompt(fresh.ID); prompt != "" {
			t.Fatalf("%s: launch prompt = %q, want none: the forwarder delivers the task", tc.policy, prompt)
		}
	}

	ran := create()
	if _, err := admin.Exec(ctx, `UPDATE ao_turns SET state = 'completed', attempt_count = 1, completed_at = now()
		WHERE session_id = $1 AND user_message_sequence = (SELECT min(user_message_sequence) FROM ao_turns WHERE session_id = $1)`, ran.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SendMessage(ctx, principal, fixture.orgID, ran.ID, uuid.NewString(), "one more", domain.ChatTurnSettings{}); err != nil {
		t.Fatal(err)
	}
	switchToTerminal(ran.ID, domain.SessionInterfaceTransitionDrain)
	if states := turnStates(ran.ID); !slices.Equal(states, []string{"completed", "queued", "queued"}) {
		t.Fatalf("turn states after Chat ran = %v, want [completed queued queued]", states)
	}

	for _, policy := range []domain.SessionInterfaceTransitionPolicy{domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionInterrupt} {
		legacy := create()
		if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET prompt = 'do the work' WHERE id = $1`, legacy.ID); err != nil {
			t.Fatal(err)
		}
		switchToTerminal(legacy.ID, policy)
		want := []string{"completed", "queued"}
		if policy == domain.SessionInterfaceTransitionInterrupt {
			want = []string{"completed", "completed"}
		}
		if states := turnStates(legacy.ID); !slices.Equal(states, want) {
			t.Fatalf("legacy %s: turn states = %v, want %v", policy, states, want)
		}
		if prompt := launchPrompt(legacy.ID); prompt != "do the work" {
			t.Fatalf("legacy %s: launch prompt = %q, want the prompt", policy, prompt)
		}
	}
}
