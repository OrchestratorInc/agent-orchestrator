package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// initialTurn reports the state of the turn queued for a session's first user
// message, and whether that message's event names it.
func initialTurn(t *testing.T, admin *pgxpool.Pool, sessionID string) (state string, found, linked bool) {
	t.Helper()
	ctx := context.Background()
	var turnID string
	err := admin.QueryRow(ctx, `SELECT turn.id, turn.state FROM ao_turns turn
		JOIN ao_events event ON event.session_id = turn.session_id AND event.sequence = turn.user_message_sequence
		WHERE turn.session_id = $1 AND event.type = 'chat.user_message'`, sessionID).Scan(&turnID, &state)
	if err != nil {
		return "", false, false
	}
	var payload map[string]any
	var raw []byte
	if err := admin.QueryRow(ctx, `SELECT payload FROM ao_events WHERE session_id = $1 AND type = 'chat.user_message'`, sessionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return state, true, payload["turnId"] == turnID
}

// A session's interface is resolved at create as a local spawn's: an
// orchestrator starts in Chat, a worker in the default it was created with,
// and Chat only for a harness that can start in it. A create without a default
// (an older desktop) starts in the TUI, an orchestrator too. A Chat session's prompt is
// queued as the first turn, the only work the Chat controller takes; a TUI
// session keeps the prompt on the agent's command line and queues nothing.
func TestCreateSessionStartInterface(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	create := func(kind, harness string, requested, defaultInterface domain.SessionInterface) domain.Session {
		t.Helper()
		// A project has one active orchestrator, so each gets its own.
		projectID := fixture.projectID
		if kind == "orchestrator" {
			projectID = uuid.NewString()
			if _, err := admin.Exec(ctx, `INSERT INTO ao_projects (id, org_id, display_name, repository_url) VALUES ($1, $2, 'P', $3)`,
				projectID, fixture.orgID, "https://example.test/"+projectID); err != nil {
				t.Fatal(err)
			}
		}
		session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
			ProjectID: projectID, Kind: kind, Harness: harness, DisplayName: "s", Prompt: "do the work",
			Provider: "docker", Interface: requested, DefaultInterface: defaultInterface,
		})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	for _, tc := range []struct {
		name, kind, harness         string
		requested, defaultInterface domain.SessionInterface
		want                        domain.SessionInterface
	}{
		{"claude worker, chat default", "worker", "claude-code", "", domain.SessionInterfaceChat, domain.SessionInterfaceChat},
		{"codex worker, chat default", "worker", "codex", "", domain.SessionInterfaceChat, domain.SessionInterfaceChat},
		{"cursor worker, chat default", "worker", "cursor", "", domain.SessionInterfaceChat, domain.SessionInterfaceChat},
		{"opencode worker, chat default", "worker", "opencode", "", domain.SessionInterfaceChat, domain.SessionInterfaceTUI},
		{"worker, tui default", "worker", "codex", "", domain.SessionInterfaceTUI, domain.SessionInterfaceTUI},
		{"worker, no default", "worker", "claude-code", "", "", domain.SessionInterfaceTUI},
		// A task's interfaceMode (the composer's resolution) wins over a default.
		{"worker, explicit chat", "worker", "cursor", domain.SessionInterfaceChat, "", domain.SessionInterfaceChat},
		{"worker, explicit tui over a chat default", "worker", "claude-code", domain.SessionInterfaceTUI, domain.SessionInterfaceChat, domain.SessionInterfaceTUI},
		{"codex orchestrator, no default", "orchestrator", "codex", "", "", domain.SessionInterfaceTUI},
		{"codex orchestrator, tui default", "orchestrator", "codex", "", domain.SessionInterfaceTUI, domain.SessionInterfaceChat},
		{"claude orchestrator, tui default", "orchestrator", "claude-code", "", domain.SessionInterfaceTUI, domain.SessionInterfaceChat},
		{"cursor orchestrator", "orchestrator", "cursor", "", domain.SessionInterfaceChat, domain.SessionInterfaceChat},
		{"opencode orchestrator", "orchestrator", "opencode", "", domain.SessionInterfaceChat, domain.SessionInterfaceTUI},
	} {
		session := create(tc.kind, tc.harness, tc.requested, tc.defaultInterface)
		if session.Interface != tc.want {
			t.Fatalf("%s: interface = %q, want %q", tc.name, session.Interface, tc.want)
		}
		state, found, linked := initialTurn(t, admin, session.ID)
		if tc.want == domain.SessionInterfaceChat && (!found || state != "queued" || !linked) {
			t.Fatalf("%s: initial turn state=%q found=%v linked=%v, want a queued turn the message names", tc.name, state, found, linked)
		}
		if tc.want == domain.SessionInterfaceTUI && found {
			t.Fatalf("%s: a TUI session queued its prompt as a turn", tc.name)
		}
		var stored string
		if err := admin.QueryRow(ctx, `SELECT COALESCE(agent_config->>'defaultInterface', '') FROM ao_sessions WHERE id = $1`, session.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != string(tc.defaultInterface) {
			t.Fatalf("%s: stored default = %q, want %q", tc.name, stored, tc.defaultInterface)
		}
		// The agent config keeps decoding as before beside the key.
		launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		var agentConfig domain.ProjectAgentConfig
		if err := json.Unmarshal(launch.AgentConfig, &agentConfig); err != nil {
			t.Fatalf("%s: agent config %s: %v", tc.name, launch.AgentConfig, err)
		}
		// The task runs from one place: a Chat session's queued turn (its
		// terminal launch carries no prompt), or a TUI session's command line.
		wantPrompt := "do the work"
		if tc.want == domain.SessionInterfaceChat {
			wantPrompt = ""
		}
		if launch.Prompt != wantPrompt {
			t.Fatalf("%s: launch prompt = %q, want %q", tc.name, launch.Prompt, wantPrompt)
		}
	}
}

// A create that omits the interface records the same idempotency payload as
// before the field existed (it always carried "Interface":"tui"), so a retry
// across the upgrade still replays the first create.
func TestCreateSessionIdempotencyPayloadUnchangedWithoutInterface(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	key := uuid.NewString()
	input := domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "go", Provider: "docker",
	}
	first, err := store.CreateSession(ctx, principal, fixture.orgID, key, 100, input)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	var raw []byte
	if err := admin.QueryRow(ctx, `SELECT payload FROM ao_commands WHERE org_id = $1 AND idempotency_key = $2`, fixture.orgID, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["Interface"] != "tui" {
		t.Fatalf("stored payload Interface = %v, want tui (the pre-change payload)", stored["Interface"])
	}
	if _, ok := stored["DefaultInterface"]; ok {
		t.Fatalf("stored payload carries DefaultInterface: %s", raw)
	}
	replay, err := store.CreateSession(ctx, principal, fixture.orgID, key, 100, input)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay = %q, %v; want %q", replay.ID, err, first.ID)
	}
	// The same key with an explicit tui is the same request.
	input.Interface = domain.SessionInterfaceTUI
	if replay, err := store.CreateSession(ctx, principal, fixture.orgID, key, 100, input); err != nil || replay.ID != first.ID {
		t.Fatalf("explicit tui replay = %q, %v; want %q", replay.ID, err, first.ID)
	}
}

// A worker an orchestrator spawns resolves its interface from the default its
// orchestrator was created with, as a local spawn resolves from the daemon's
// default; the orchestrator's own interface (Chat) does not decide it.
func TestCreateOrchestratorChildResolvesFromTheOrchestratorsDefault(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	setParent := func(defaultInterface string) {
		t.Helper()
		if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET kind = 'orchestrator', interface = 'chat',
			agent_config = CASE WHEN $2 = '' THEN agent_config - 'defaultInterface'
				ELSE agent_config || jsonb_build_object('defaultInterface', $2::text) END
			WHERE id = $1`, fixture.sessionID, defaultInterface); err != nil {
			t.Fatal(err)
		}
	}
	spawn := func(harness string) domain.Session {
		t.Helper()
		child, err := store.CreateOrchestratorChild(ctx, fixture.orgID, fixture.sessionID, uuid.NewString(), 100, domain.CreateSession{
			Kind: "worker", Harness: harness, DisplayName: "child", Prompt: "go", Provider: "docker",
		})
		if err != nil {
			t.Fatal(err)
		}
		return child
	}
	setParent("chat")
	if child := spawn("codex"); child.Interface != domain.SessionInterfaceChat {
		t.Fatalf("codex child, chat default = %q, want chat", child.Interface)
	} else if state, found, _ := initialTurn(t, admin, child.ID); !found || state != "queued" {
		t.Fatalf("chat child initial turn: state=%q found=%v", state, found)
	}
	if child := spawn("cursor"); child.Interface != domain.SessionInterfaceChat {
		t.Fatalf("cursor child = %q, want chat", child.Interface)
	}
	for _, defaultInterface := range []string{"tui", ""} {
		setParent(defaultInterface)
		if child := spawn("claude-code"); child.Interface != domain.SessionInterfaceTUI {
			t.Fatalf("claude child of a Chat orchestrator with default %q = %q, want tui", defaultInterface, child.Interface)
		}
	}
}

// A session created in Chat whose worker cannot start Chat moves to the TUI
// before anything ran: the prompt's queued turn stays queued for the
// terminal's turn forwarder (the TUI launches without the prompt), unless the
// launch still carries the prompt, and a failed TUI-to-Chat transition carries the
// notice. A session the person switched to Chat does not move.
func TestFallBackChatStart(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "do the work",
		Provider: "docker", Interface: domain.SessionInterfaceChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := store.FallBackChatStart(ctx, fixture.orgID, session.ID, "CHAT_START_UNSUPPORTED", "started in the terminal")
	if err != nil || !moved {
		t.Fatalf("FallBackChatStart = %v, %v; want moved", moved, err)
	}
	launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The terminal launches without the prompt; the task stays queued for the
	// terminal's turn forwarder, which delivers it once after the agent starts.
	if launch.Interface != domain.SessionInterfaceTUI || launch.Prompt != "" {
		t.Fatalf("launch interface = %q prompt = %q, want the TUI without the prompt", launch.Interface, launch.Prompt)
	}
	if state, found, _ := initialTurn(t, admin, session.ID); !found || state != "queued" {
		t.Fatalf("initial turn state = %q (found %v), want queued for the forwarder", state, found)
	}
	transition, found, err := store.GetLatestRelevantSessionInterfaceTransition(ctx, principal, fixture.orgID, session.ID)
	if err != nil || !found {
		t.Fatalf("notice transition: found=%v err=%v", found, err)
	}
	if transition.Phase != domain.SessionInterfaceTransitionFailed || transition.TargetInterface != domain.SessionInterfaceChat ||
		transition.ErrorCode != "CHAT_START_UNSUPPORTED" || transition.ErrorDetail != "started in the terminal" {
		t.Fatalf("notice transition = %+v", transition)
	}
	// Moved once; asking again changes nothing.
	if moved, err := store.FallBackChatStart(ctx, fixture.orgID, session.ID, "CHAT_START_UNSUPPORTED", "again"); err != nil || moved {
		t.Fatalf("second FallBackChatStart = %v, %v; want not moved", moved, err)
	}

	// The fixture session was switched to Chat by the person: it stays.
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET interface = 'chat' WHERE id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO ao_interface_transitions (org_id, session_id, source_interface, target_interface, policy, phase, completed_at)
		VALUES ($1, $2, 'tui', 'chat', 'drain', 'completed', now())`, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if moved, err := store.WorkerFallBackChatStart(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		"CHAT_ADAPTER_UNAVAILABLE", "no npm"); err != nil || moved {
		t.Fatalf("switched session: WorkerFallBackChatStart = %v, %v; want not moved", moved, err)
	}
	// A new Chat session's current worker moves it.
	fresh, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "go",
		Provider: "docker", Interface: domain.SessionInterfaceChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO ao_worker_connections (session_id, org_id, sandbox_id, epoch, worker_id, version)
		VALUES ($1, $2, $1, 1, 'worker-fresh', 'test')`, fresh.ID, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	if moved, err := store.WorkerFallBackChatStart(ctx, fixture.orgID, fresh.ID, "worker-fresh", 1,
		"CHAT_ADAPTER_UNAVAILABLE", "no npm"); err != nil || !moved {
		t.Fatalf("fresh session: WorkerFallBackChatStart = %v, %v; want moved", moved, err)
	}
	// A Chat session whose terminal launch still carries the prompt (created
	// before Chat sessions launched without it) gets it on the command line,
	// so its queued task turn is settled: the forwarder must not type it again.
	legacy, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "legacy task",
		Provider: "docker", Interface: domain.SessionInterfaceChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET prompt = 'legacy task' WHERE id = $1`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if moved, err := store.FallBackChatStart(ctx, fixture.orgID, legacy.ID, "CHAT_START_UNSUPPORTED", "x"); err != nil || !moved {
		t.Fatalf("legacy session: FallBackChatStart = %v, %v; want moved", moved, err)
	}
	if state, found, _ := initialTurn(t, admin, legacy.ID); !found || state != "completed" {
		t.Fatalf("legacy initial turn state = %q (found %v), want completed", state, found)
	}
	if launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, legacy.ID); err != nil || launch.Prompt != "legacy task" {
		t.Fatalf("legacy launch prompt = %q (%v), want the prompt", launch.Prompt, err)
	}
	// Only the session's current worker may ask.
	if _, err := store.WorkerFallBackChatStart(ctx, fixture.orgID, fixture.sessionID, "worker-stale", fixture.epoch,
		"CHAT_ADAPTER_UNAVAILABLE", "no npm"); err == nil {
		t.Fatal("a stale worker moved the session")
	}
}

// A session created in Chat whose Chat already ran stays in Chat when a later
// worker (a restart, a bootstrap without the chat-start capability, or an
// adapter install blip) asks to move it: its first turn was claimed or
// settled, or the agent reported its conversation. Moving it would show a
// false notice and run its first task again in the terminal.
func TestFallBackChatStartLeavesAChatThatRan(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	for _, tc := range []struct {
		name string
		ran  string
	}{
		{"first turn completed, conversation reported",
			`UPDATE ao_turns SET state = 'completed', attempt_count = 1, completed_at = now() WHERE session_id = $1;
			UPDATE ao_sessions SET agent_session_id = 'native-1' WHERE id = $1`},
		{"first turn claimed", `UPDATE ao_turns SET state = 'running', attempt_count = 1 WHERE session_id = $1`},
		{"first turn settled without a claim", `UPDATE ao_turns SET state = 'completed', completed_at = now() WHERE session_id = $1`},
		{"conversation reported", `UPDATE ao_sessions SET agent_session_id = 'native-1' WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
				ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "do the work",
				Provider: "docker", Interface: domain.SessionInterfaceChat,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range strings.Split(tc.ran, ";") {
				if _, err := admin.Exec(ctx, statement, session.ID); err != nil {
					t.Fatal(err)
				}
			}
			if moved, err := store.FallBackChatStart(ctx, fixture.orgID, session.ID, "CHAT_START_UNSUPPORTED", "x"); err != nil || moved {
				t.Fatalf("FallBackChatStart = %v, %v; want not moved", moved, err)
			}
			launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if launch.Interface != domain.SessionInterfaceChat {
				t.Fatalf("launch interface = %q, want chat", launch.Interface)
			}
			if _, found, err := store.GetLatestRelevantSessionInterfaceTransition(ctx, principal, fixture.orgID, session.ID); err != nil || found {
				t.Fatalf("notice transition recorded: found=%v err=%v", found, err)
			}
		})
	}
}

// A person's switch that commits while the fallback waits on the session row
// keeps the session where the switch put it: the fallback checks the
// session's transitions after it holds the row lock, not in a snapshot taken
// before the switch committed.
func TestFallBackChatStartWaitsForASwitchInProgress(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "do the work",
		Provider: "docker", Interface: domain.SessionInterfaceChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The switch, as StartSessionInterfaceTransition runs it: lock the row,
	// then record the transition; it commits only after the fallback waits.
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM ao_sessions WHERE id = $1 FOR UPDATE`, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ao_interface_transitions (org_id, session_id, source_interface, target_interface, policy, phase)
		VALUES ($1, $2, 'chat', 'tui', 'drain', 'requested')`, fixture.orgID, session.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		moved bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		moved, err := store.FallBackChatStart(ctx, fixture.orgID, session.ID, "CHAT_START_UNSUPPORTED", "x")
		done <- result{moved, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the fallback did not wait for the switch: moved=%v err=%v", r.moved, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the fallback never waited on the session row")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.moved {
		t.Fatalf("FallBackChatStart during a switch = %v, %v; want not moved", r.moved, r.err)
	}
	var iface string
	if err := admin.QueryRow(ctx, `SELECT interface FROM ao_sessions WHERE id = $1`, session.ID).Scan(&iface); err != nil {
		t.Fatal(err)
	}
	if iface != "chat" {
		t.Fatalf("interface = %q, want chat", iface)
	}
}

// Once a Chat controller claimed a turn, the session's task already ran in
// Chat: a worker's launch carries no prompt, so a later switch to the
// terminal does not run it again. Until then (or when the start fell back to
// the terminal, which settles the turn unclaimed) the launch keeps it.
func TestWorkerLaunchSpecDropsThePromptOnceChatClaimedATurn(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
		ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "s", Prompt: "do the work",
		Provider: "docker", Interface: domain.SessionInterfaceChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A session created in Chat launches without the prompt at all.
	if launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID); err != nil || launch.Prompt != "" {
		t.Fatalf("new Chat session: prompt = %q, err = %v; want none", launch.Prompt, err)
	}
	// One created before that keeps it until Chat claims a turn.
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET prompt = 'do the work' WHERE id = $1`, session.ID); err != nil {
		t.Fatal(err)
	}
	launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
	if err != nil || launch.Prompt != "do the work" {
		t.Fatalf("before a claim: prompt = %q, err = %v", launch.Prompt, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_turns SET state = 'running', attempt_count = 1 WHERE session_id = $1`, session.ID); err != nil {
		t.Fatal(err)
	}
	launch, err = store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
	if err != nil || launch.Prompt != "" {
		t.Fatalf("after a claim: prompt = %q, err = %v; want none", launch.Prompt, err)
	}
}

// A scratch project's first orchestrator resolves its interface like any
// orchestrator (with a default, Chat where its harness can start there;
// without one, the TUI) and keeps the user's default for the workers it
// spawns. Without a default the create's payload
// carries none (TestLegacyProjectCreateRetriesSurviveConfigNormalization
// replays a scratch create made before the field).
func TestCreateGitHubScratchProjectOrchestratorInterface(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	var installationID string
	if err := admin.QueryRow(ctx, `INSERT INTO ao_github_installations
		(org_id, github_installation_id, github_account_id, account_login, account_type, repository_selection, installed_by_user_id)
		VALUES ($1, $2, $2, 'octo', 'Organization', 'selected', $3) RETURNING id::text`,
		fixture.orgID, time.Now().UnixNano(), fixture.userID).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	for index, tc := range []struct {
		harness          string
		defaultInterface domain.SessionInterface
		want             domain.SessionInterface
	}{
		{"codex", domain.SessionInterfaceChat, domain.SessionInterfaceChat},
		{"claude-code", "", domain.SessionInterfaceTUI},
		{"claude-code", domain.SessionInterfaceTUI, domain.SessionInterfaceChat},
		{"cursor", domain.SessionInterfaceChat, domain.SessionInterfaceChat},
	} {
		repositoryID := time.Now().UnixNano() + int64(index)
		url := fmt.Sprintf("https://github.com/octo/scratch-%d", repositoryID)
		if _, err := admin.Exec(ctx, `INSERT INTO ao_github_repositories
			(github_repository_id, github_owner_account_id, name, full_name, html_url, clone_url, visibility)
			VALUES ($1, 1, 'scratch', 'octo/scratch', $2, $3, 'private')`, repositoryID, url, url+".git"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(context.Background(), `DELETE FROM ao_github_repository_grants WHERE github_repository_id = $1`, repositoryID)
			_, _ = admin.Exec(context.Background(), `DELETE FROM ao_github_repositories WHERE github_repository_id = $1`, repositoryID)
		})
		if _, err := admin.Exec(ctx, `INSERT INTO ao_github_repository_grants (org_id, installation_id, github_repository_id, repository_selection)
			VALUES ($1, $2, $3, 'selected')`, fixture.orgID, installationID, repositoryID); err != nil {
			t.Fatal(err)
		}
		key := uuid.NewString()
		_, session, err := store.CreateGitHubScratchProject(ctx, principal, fixture.orgID, key, 100, domain.CreateGitHubScratchProject{
			Repository:  domain.GitHubRepository{GitHubRepositoryID: repositoryID},
			DisplayName: "Scratch",
			Session: domain.CreateSession{
				Kind: "orchestrator", Harness: tc.harness, DisplayName: "Orchestrator", Prompt: "start",
				Mode: "trusted", DeniedCommands: []string{}, Provider: "docker", DefaultInterface: tc.defaultInterface,
			},
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.harness, err)
		}
		if session.Interface != tc.want {
			t.Fatalf("%s orchestrator interface = %q, want %q", tc.harness, session.Interface, tc.want)
		}
		var stored string
		var payload []byte
		if err := admin.QueryRow(ctx, `SELECT COALESCE(agent_config->>'defaultInterface', '') FROM ao_sessions WHERE id = $1`, session.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != string(tc.defaultInterface) {
			t.Fatalf("%s: stored default = %q, want %q", tc.harness, stored, tc.defaultInterface)
		}
		if err := admin.QueryRow(ctx, `SELECT payload FROM ao_commands WHERE org_id = $1 AND idempotency_key = $2`, fixture.orgID, key).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if hasDefault := strings.Contains(string(payload), `"DefaultInterface"`); hasDefault != (tc.defaultInterface != "") {
			t.Fatalf("%s: payload %s, DefaultInterface present = %v", tc.harness, payload, hasDefault)
		}
	}
}

func TestSessionAgentConfigJSON(t *testing.T) {
	config := domain.ProjectAgentConfig{Model: "gpt-5", Effort: "high"}
	plain, err := sessionAgentConfigJSON(config, "")
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := json.Marshal(config); string(plain) != string(want) {
		t.Fatalf("without a default = %s, want the agent config alone %s", plain, want)
	}
	withDefault, err := sessionAgentConfigJSON(config, domain.SessionInterfaceChat)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(withDefault, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["defaultInterface"] != "chat" || fields["model"] != "gpt-5" || fields["effort"] != "high" {
		t.Fatalf("with a default = %s", withDefault)
	}
	var decoded domain.ProjectAgentConfig
	if err := json.Unmarshal(withDefault, &decoded); err != nil || decoded != config {
		t.Fatalf("decoded = %+v, %v; want %+v", decoded, err, config)
	}
}
