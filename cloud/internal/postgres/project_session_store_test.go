package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

func TestCreateSessionReturnsCompleteSession(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)

	session, err := store.CreateSession(
		context.Background(),
		domain.Principal{UserID: fixture.userID, Provider: "local"},
		fixture.orgID,
		"create-session-"+uuid.NewString(),
		10,
		domain.CreateSession{
			ProjectID:   fixture.projectID,
			Kind:        "orchestrator",
			Harness:     "codex",
			DisplayName: "Test orchestrator",
			Provider:    "docker",
		},
	)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.ID == "" {
		t.Fatal("created session has no ID")
	}
	if !session.AutoInjectCI || !session.AutoInjectReview {
		t.Fatalf(
			"created session policies = ci:%v review:%v, want both enabled",
			session.AutoInjectCI,
			session.AutoInjectReview,
		)
	}
	if session.RuntimeConnected {
		t.Fatal("new session is unexpectedly runtime-connected")
	}
	if session.SandboxProvider != "docker" {
		t.Fatalf("sandbox provider = %q, want docker", session.SandboxProvider)
	}
}

// A requested branch becomes the session branch; without one the store still
// generates <prefix>/<short id>.
func TestCreateSessionKeepsRequestedBranch(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	create := func(branch string) domain.Session {
		t.Helper()
		session, err := store.CreateSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID,
			"create-branch-"+uuid.NewString(), 10, domain.CreateSession{
				ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Branch test",
				Prompt: "hello", Provider: "docker", Branch: branch,
			})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	if got := create("feature/x").Branch; got != "feature/x" {
		t.Fatalf("branch = %q, want feature/x", got)
	}
	generated := create("")
	if want := "/" + generated.ID[:8]; !strings.HasSuffix(generated.Branch, want) || len(generated.Branch) == len(want) {
		t.Fatalf("generated branch = %q, want <prefix>%s", generated.Branch, want)
	}
	// The default branch, the session prefix, and names git refuses are
	// rejected wherever the session is created.
	prefix := strings.TrimSuffix(generated.Branch, "/"+generated.ID[:8])
	for _, branch := range []string{"main", prefix, "-f", "a..b"} {
		_, err := store.CreateSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID,
			"create-branch-"+uuid.NewString(), 10, domain.CreateSession{
				ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Branch test",
				Prompt: "hello", Provider: "docker", Branch: branch,
			})
		if !errors.Is(err, ErrInvalidBranch) {
			t.Fatalf("branch %q: err = %v, want ErrInvalidBranch", branch, err)
		}
	}
}

// The branch override and the requested output are separate bind parameters
// of the session insert; each lands in its own column.
func TestCreateSessionKeepsBranchAndRequestedOutputApart(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	session, err := store.CreateSession(context.Background(), domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID,
		"create-output-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Output test",
			Prompt: "hello", Provider: "docker", Branch: "feature/report", RequestedOutput: domain.RequestedOutputArtifact,
		})
	if err != nil {
		t.Fatal(err)
	}
	if session.Branch != "feature/report" || session.RequestedOutput != domain.RequestedOutputArtifact {
		t.Fatalf("branch = %q, requested output = %q", session.Branch, session.RequestedOutput)
	}
}

// A requested branch is one live session's at a time, and never an additional
// repository's base branch; a terminated session releases its branch, and a
// session whose branch was taken over while it was deleted is not restored.
func TestCreateSessionRefusesABranchInUseOrARepositoryBase(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	create := func(branch string) (domain.Session, error) {
		t.Helper()
		return store.CreateSession(ctx, principal, fixture.orgID, "branch-use-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Branch use",
			Prompt: "hello", Provider: "docker", Branch: branch,
		})
	}
	first, err := create("feature/shared")
	if err != nil {
		t.Fatal(err)
	}
	_, err = create("Feature/Shared")
	var refused *BranchRefusedError
	if !errors.Is(err, ErrBranchInUse) || !errors.As(err, &refused) || refused.SessionID != first.ID {
		t.Fatalf("second session on the branch: err = %v, want ErrBranchInUse naming %s", err, first.ID)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET is_terminated = true WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := create("feature/shared")
	if err != nil {
		t.Fatalf("a terminated session's branch was not released: %v", err)
	}
	if err := store.RestoreSession(ctx, principal, fixture.orgID, first.ID); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("restore over %s's branch: err = %v, want ErrBranchInUse", second.ID, err)
	}
	var terminated bool
	if err := admin.QueryRow(ctx, `SELECT is_terminated FROM ao_sessions WHERE id = $1`, first.ID).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("a refused restore left the session terminated = %v (%v)", terminated, err)
	}
	// ao kill only marks the session's sandboxes deleted; its branch is free
	// for the next spawn before teardown finishes.
	var sandboxes int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_sandboxes WHERE session_id = $1`, second.ID).Scan(&sandboxes); err != nil || sandboxes == 0 {
		t.Fatalf("the session has %d sandboxes (%v), want at least one", sandboxes, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_sandboxes SET desired_state = 'deleted' WHERE session_id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := create("feature/shared"); err != nil {
		t.Fatalf("a killed session still holds its branch: %v", err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE ao_projects SET config = config || '{"repos":[{"url":"https://github.com/acme/julian","branch":"develop"}]}'::jsonb WHERE id = $1`,
		fixture.projectID,
	); err != nil {
		t.Fatal(err)
	}
	_, err = create("Develop")
	if !errors.As(err, &refused) || refused.Reason != domain.SessionBranchRepositoryBase || refused.Repository != "acme/julian" ||
		!errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("an additional repository's base branch: err = %v, want repository_base for acme/julian", err)
	}
}

func TestCreateSessionPreservesInitialEffort(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	session, err := store.CreateSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID,
		"create-effort-"+uuid.NewString(), 10, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Effort test",
			Prompt: "hello", Provider: "docker", ReasoningEffort: "medium",
		})
	if err != nil {
		t.Fatal(err)
	}
	launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if launch.ReasoningEffort != "medium" {
		t.Fatalf("launch effort = %q, want medium", launch.ReasoningEffort)
	}
}

// A Chat session launches in Chat and queues its opening prompt as a turn for
// the Chat controller, so its terminal launch carries no prompt (a later switch
// to the terminal does not run it again, as locally); a Terminal session gets
// the prompt on the agent's command line and queues no turn.
func TestCreateSessionStartsInTheRequestedInterface(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	// Read as the organization, so forced row-level security shows the turns
	// under a role that does not bypass it.
	admin = orgAdminPool(t, admin, fixture.orgID)
	ctx := context.Background()
	for _, tc := range []struct {
		requested    domain.SessionInterface
		want         domain.SessionInterface
		turns        int
		launchPrompt string
	}{
		{domain.SessionInterfaceChat, domain.SessionInterfaceChat, 1, ""},
		{"", domain.SessionInterfaceTUI, 0, "hello"},
	} {
		session, err := store.CreateSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID,
			"create-interface-"+uuid.NewString(), 10, domain.CreateSession{
				ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Interface test",
				Prompt: "hello", Provider: "docker", Interface: tc.requested,
			})
		if err != nil {
			t.Fatal(err)
		}
		launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		var turns int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_turns WHERE session_id = $1 AND state = 'queued'`, session.ID).Scan(&turns); err != nil {
			t.Fatal(err)
		}
		if launch.Interface != tc.want || turns != tc.turns || launch.Prompt != tc.launchPrompt {
			t.Fatalf("requested %q: launch interface = %q, queued turns = %d, launch prompt = %q; want %q, %d, %q",
				tc.requested, launch.Interface, turns, launch.Prompt, tc.want, tc.turns, tc.launchPrompt)
		}
	}
}

func TestQueuedTurnDoesNotOverrideIdleWorkerActivity(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_events (org_id, session_id, sequence, type)
		VALUES ($1, $2, 1, 'chat.user_message')`,
		fixture.orgID, fixture.sessionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_turns (org_id, session_id, user_message_sequence)
		VALUES ($1, $2, 1)`,
		fixture.orgID, fixture.sessionID,
	); err != nil {
		t.Fatal(err)
	}
	for _, turnState := range []string{"queued", "running"} {
		t.Run(turnState, func(t *testing.T) {
			if _, err := admin.Exec(ctx,
				`UPDATE ao_turns SET state = $1 WHERE org_id = $2 AND session_id = $3`,
				turnState, fixture.orgID, fixture.sessionID,
			); err != nil {
				t.Fatal(err)
			}
			session, err := store.GetSession(ctx,
				domain.Principal{UserID: fixture.userID, Provider: "local"},
				fixture.orgID, fixture.sessionID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := session.Status(time.Now(), nil); got != contract.StatusIdle {
				t.Fatalf("status = %q, want idle despite %s turn", got, turnState)
			}
		})
	}
}

func TestSessionInsertReturningMatchesScannerArity(t *testing.T) {
	const scanSessionDestinations = 39
	if got := strings.Count(sessionInsertReturning, ",") + 1; got != scanSessionDestinations {
		t.Fatalf("session insert RETURNING fields = %d, want %d", got, scanSessionDestinations)
	}
	for _, field := range []string{
		"reviewer_harness", "auto_review_enabled", "auto_inject_ci",
		"auto_inject_review", "terminate_on_pr_merge",
		"requested_output", "output_type",
	} {
		if !strings.Contains(sessionInsertReturning, field) {
			t.Fatalf("session insert RETURNING is missing %s", field)
		}
	}
}

// A full organization still reports a requested branch that can never work,
// rather than a quota error the caller would wait out for nothing.
func TestCreateSessionChecksTheBranchBeforeTheSandboxQuota(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	create := func(branch string, maxSandboxes int) error {
		t.Helper()
		_, err := store.CreateSession(ctx, principal, fixture.orgID, "branch-quota-"+uuid.NewString(), maxSandboxes, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "Branch quota",
			Prompt: "hello", Provider: "docker", Branch: branch,
		})
		return err
	}
	if err := create("feature/held", 10); err != nil {
		t.Fatal(err)
	}
	if err := create("main", 0); !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("the default branch in a full organization: err = %v, want ErrInvalidBranch", err)
	}
	if err := create("feature/held", 0); !errors.Is(err, ErrBranchInUse) {
		t.Fatalf("a held branch in a full organization: err = %v, want ErrBranchInUse", err)
	}
	if err := create("feature/free", 0); !errors.Is(err, ErrSandboxQuotaExceeded) {
		t.Fatalf("a usable branch in a full organization: err = %v, want ErrSandboxQuotaExceeded", err)
	}
}
