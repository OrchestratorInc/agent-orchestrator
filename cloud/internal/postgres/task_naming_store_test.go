package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCreateSessionTaskNamingState(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	for _, tc := range []struct {
		name, kind, prompt, repository string
		generateTitle                  bool
		want                           string
	}{
		{"prompted worker", "worker", "Fix login", "https://example.test/repo", true, "pending"},
		{"explicit title worker", "worker", "Fix login", "https://example.test/repo", false, "pending"},
		{"empty prompt", "worker", "  ", "https://example.test/repo", true, "done"},
		{"orchestrator", "orchestrator", "Fix login", "https://example.test/repo", true, "done"},
		{"scratch", "worker", "Fix login", "https://SCRATCH.ao.local/repo", true, "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, err := store.CreateProject(ctx, principal, fixture.orgID, uuid.NewString(), domain.CreateProject{
				DisplayName: tc.name, RepositoryURL: tc.repository + "/" + uuid.NewString(), DefaultBranch: "main",
			})
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
				ProjectID: project.ID, Kind: tc.kind, Harness: "codex", DisplayName: "Initial task", Prompt: tc.prompt, Provider: "docker", GenerateTitle: tc.generateTitle,
			})
			if err != nil {
				t.Fatal(err)
			}
			if session.TaskNamingState != tc.want || session.TaskNameSlug != "" {
				t.Fatalf("session naming = %q/%q", session.TaskNamingState, session.TaskNameSlug)
			}
			launch, err := store.WorkerLaunchSpec(ctx, fixture.orgID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if launch.TaskNamingState != tc.want || launch.TaskNameSlug != "" {
				t.Fatalf("launch naming = %q/%q", launch.TaskNamingState, launch.TaskNameSlug)
			}
			var initial *string
			if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT task_name_initial_title FROM ao_sessions WHERE org_id=$1 AND id=$2`, fixture.orgID, session.ID).Scan(&initial)
			}); err != nil {
				t.Fatal(err)
			}
			if (initial != nil) != (tc.generateTitle && tc.want == "pending") {
				t.Fatalf("initial title = %v", initial)
			}
		})
	}
}

func TestNameWorkerTaskFencesAndTitleOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, update, workerID string
		epoch                  int64
		wantErr                error
		wantTitle              string
	}{
		{name: "manual before reservation", update: "display_name='Manual title'", wantTitle: "Manual title"},
		{name: "explicit title", update: "task_name_initial_title=NULL", wantTitle: "Session"},
		{name: "terminated", update: "is_terminated=true", wantErr: ErrConflict},
		{name: "launched", update: "agent_session_id='provider-thread'", wantErr: ErrConflict},
		{name: "wrong worker", workerID: "other-worker", wantErr: ErrStaleWorker},
		{name: "wrong epoch", epoch: 40, wantErr: ErrStaleWorker},
		{name: "orchestrator", update: "kind='orchestrator'", wantErr: ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE ao_sessions SET prompt='Fix login', task_naming_state='pending', task_name_initial_title=display_name WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
				if err != nil || tc.update == "" {
					return err
				}
				_, err = tx.Exec(ctx, `UPDATE ao_sessions SET `+tc.update+` WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			workerID, epoch := fixture.workerID, fixture.epoch
			if tc.workerID != "" {
				workerID = tc.workerID
			}
			if tc.epoch != 0 {
				epoch = tc.epoch
			}
			result, err := store.NameWorkerTask(ctx, fixture.orgID, fixture.sessionID, workerID, epoch, domain.TaskNameRequest{Title: "Generated", Slug: "login"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if err == nil && result.Title != tc.wantTitle {
				t.Fatalf("title = %q, want %q", result.Title, tc.wantTitle)
			}
		})
	}
}

func TestNameWorkerTaskSerializesProjectClaims(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	var sessions []domain.Session
	for range 2 {
		session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "codex", DisplayName: "Explicit", Prompt: "Fix login", Provider: "docker",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, session.ID, fixture.workerID+session.ID, "test", 1, nil); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	results := make([]domain.TaskNameResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, session := range sessions {
		wg.Go(func() {
			results[i], errs[i] = store.NameWorkerTask(ctx, fixture.orgID, session.ID, fixture.workerID+session.ID, 1, domain.TaskNameRequest{Title: "Generated", Slug: "login"})
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].Branch == results[1].Branch {
		t.Fatalf("concurrent reservations = %+v", results)
	}
	for _, result := range results {
		if result.Slug != "login" && result.Slug != "login-2" || result.Title != "Explicit" {
			t.Fatalf("reservation = %+v", result)
		}
	}
	first := 0
	if results[1].Slug == "login" {
		first = 1
	}
	// The adapter may select the other session's namespace while resolving a
	// filesystem collision. Completion must refuse it without changing either.
	if _, err := store.NameWorkerTask(ctx, fixture.orgID, sessions[first].ID, fixture.workerID+sessions[first].ID, 1,
		domain.TaskNameRequest{Complete: true, Branch: "ao/login-2/root"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("collision completion error = %v", err)
	}
	retry, err := store.NameWorkerTask(ctx, fixture.orgID, sessions[first].ID, fixture.workerID+sessions[first].ID, 1,
		domain.TaskNameRequest{Retry: true, Slug: "login", Occupied: []string{"refs/remotes/upstream/ao/login-3/root"}})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Slug != "login-4" || retry.Title != "Explicit" {
		t.Fatalf("retry = %+v", retry)
	}
	done, err := store.NameWorkerTask(ctx, fixture.orgID, sessions[first].ID, fixture.workerID+sessions[first].ID, 1,
		domain.TaskNameRequest{Complete: true, Branch: retry.Branch})
	if err != nil || done.State != "done" {
		t.Fatalf("completion = %+v, %v", done, err)
	}
	read, err := store.GetSession(ctx, principal, fixture.orgID, sessions[first].ID)
	if err != nil || read.TaskNamingState != "done" || read.TaskNameSlug != retry.Slug || read.Branch != retry.Branch {
		t.Fatalf("read = %+v, %v", read, err)
	}
}

func TestTaskNameNamespaceValidation(t *testing.T) {
	for _, tc := range []struct {
		value    string
		occupied bool
	}{
		{"refs/heads/ao/login/root", true}, {"refs/remotes/upstream/ao/login/root", true},
		{"ao/login", true}, {"refs/heads/ao", true}, {"login", true}, {"/worktrees/login", true},
		{"ao", false}, {"refs/heads/login", false}, {"refs/heads/ao/login-2/root", false}, {"ao/logins/root", false},
	} {
		if got := taskNameOccupied("ao", "login", []string{tc.value}); got != tc.occupied {
			t.Errorf("occupied(%q) = %v", tc.value, got)
		}
	}
	for _, branch := range []string{"ao/login/root", "ao/login-2/root", "ao/login-100/root"} {
		if _, ok := completedTaskSlug("ao", "login", branch); !ok {
			t.Errorf("valid branch rejected: %q", branch)
		}
	}
	for _, branch := range []string{"ao/login-1/root", "ao/login-101/root", "ao/login-02/root", "ao/login-2/nested/root", "other/login/root", "ao/login/root/child"} {
		if _, ok := completedTaskSlug("ao", "login", branch); ok {
			t.Errorf("invalid branch accepted: %q", branch)
		}
	}
}

func TestNameWorkerTaskReservationAndCompletion(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET prompt='Fix login redirect', task_naming_state='pending', task_name_initial_title=display_name WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	name := func(input domain.TaskNameRequest) (domain.TaskNameResult, error) {
		return store.NameWorkerTask(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, input)
	}
	reserved, err := name(domain.TaskNameRequest{Title: "  Fix\nlogin\x00 ", Slug: "LOGIN / Redirect", Occupied: []string{"refs/heads/ao/login-redirect/root", "login-redirect-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if reserved.State != "reserved" || reserved.Title != "Fix login" || reserved.Slug != "login-redirect-3" || reserved.Branch != "ao/login-redirect-3/root" {
		t.Fatalf("reserved = %+v", reserved)
	}
	if claim, err := name(domain.TaskNameRequest{Claim: true}); err != nil || claim.Generate {
		t.Fatalf("reserved generation = %+v %v", claim, err)
	}
	if _, err := name(domain.TaskNameRequest{Skip: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reserved legacy skip = %v", err)
	}
	if replay, err := name(domain.TaskNameRequest{Title: "Replace", Slug: "different"}); err != nil || replay != reserved {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET display_name='Manual title' WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := name(domain.TaskNameRequest{Complete: true, Branch: "ao/foreign/root"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign branch error = %v", err)
	}
	done, err := name(domain.TaskNameRequest{Complete: true, Title: "Overwrite manual", Branch: "ao/login-redirect-3-2/root"})
	if err != nil {
		t.Fatal(err)
	}
	if done.State != "done" || done.Title != "Manual title" || done.Slug != "login-redirect-3-2" {
		t.Fatalf("done = %+v", done)
	}
	if replay, err := name(domain.TaskNameRequest{Retry: true, Slug: "different"}); err != nil || replay != done {
		t.Fatalf("done replay = %+v, %v", replay, err)
	}
	var events int
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM ao_events WHERE org_id=$1 AND session_id=$2 AND type='workspace.changed'`, fixture.orgID, fixture.sessionID).Scan(&events)
	}); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("completion events = %d", events)
	}
	if _, err := store.NameWorkerTask(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch-1, domain.TaskNameRequest{}); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("stale done replay error = %v", err)
	}
}

func TestNameWorkerTaskGenerationAdmissionAndExpiry(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	project, err := store.CreateProject(ctx, principal, fixture.orgID, uuid.NewString(), domain.CreateProject{DisplayName: "Other project", RepositoryURL: "https://example.test/" + uuid.NewString(), DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	var sessions []domain.Session
	for i := range 6 {
		projectID := fixture.projectID
		if i%2 == 0 {
			projectID = project.ID
		}
		session, err := store.CreateSession(ctx, principal, fixture.orgID, uuid.NewString(), 100, domain.CreateSession{ProjectID: projectID, Kind: "worker", Harness: "codex", DisplayName: "Task", Prompt: "Fix login", Provider: "docker"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterWorkerBootstrap(ctx, fixture.orgID, session.ID, "worker-"+session.ID, "test", 1, nil); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	claim := func(session domain.Session) (domain.TaskNameResult, error) {
		return store.NameWorkerTask(ctx, fixture.orgID, session.ID, "worker-"+session.ID, 1, domain.TaskNameRequest{Claim: true})
	}
	results := make([]domain.TaskNameResult, len(sessions))
	errs := make([]error, len(sessions))
	var wg sync.WaitGroup
	for i, session := range sessions {
		wg.Go(func() { results[i], errs[i] = claim(session) })
	}
	wg.Wait()
	var granted, denied []int
	for i, result := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if result.State != domain.TaskNamingPending {
			t.Fatalf("claim reserved a name: %+v", result)
		}
		if result.Generate {
			granted = append(granted, i)
		} else {
			denied = append(denied, i)
		}
	}
	if len(granted) != 4 || len(denied) != 2 {
		t.Fatalf("generation admission = %d granted/%d denied", len(granted), len(denied))
	}
	if again, err := claim(sessions[granted[0]]); err != nil || again.Generate {
		t.Fatalf("repeated admission: %+v %v", again, err)
	}
	if _, err := store.NameWorkerTask(ctx, fixture.orgID, sessions[granted[0]].ID, "worker-"+sessions[granted[0]].ID, 1, domain.TaskNameRequest{Slug: "login"}); err != nil {
		t.Fatal(err)
	}
	if result, err := claim(sessions[denied[0]]); err != nil || !result.Generate {
		t.Fatalf("released admission: %+v %v", result, err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET task_name_generation_at=now()-interval '21 seconds' WHERE org_id=$1 AND id=$2`, fixture.orgID, sessions[granted[1]].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := claim(sessions[denied[1]]); err != nil || !result.Generate {
		t.Fatalf("expired admission: %+v %v", result, err)
	}
	if _, err := store.NameWorkerTask(ctx, fixture.orgID, sessions[denied[1]].ID, "stale-worker", 1, domain.TaskNameRequest{Claim: true}); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("unfenced admission = %v", err)
	}
}

func TestNameWorkerTaskLegacySkipAndValidation(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET task_naming_state='pending', prompt='Fix login', task_name_generation_at=now() WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	name := func(input domain.TaskNameRequest) (domain.TaskNameResult, error) {
		return store.NameWorkerTask(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, input)
	}
	for _, input := range []domain.TaskNameRequest{{Claim: true, Skip: true}, {Claim: true, Complete: true}, {Skip: true, Retry: true}, {Complete: true, Retry: true}} {
		if _, err := name(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid flags %+v = %v", input, err)
		}
	}
	if _, err := store.NameWorkerTask(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch-1, domain.TaskNameRequest{Skip: true}); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("unfenced skip = %v", err)
	}
	result, err := name(domain.TaskNameRequest{Skip: true})
	if err != nil || result.State != domain.TaskNamingDone || result.Branch != "main" || result.Slug != "" || result.Title != "Session" {
		t.Fatalf("skip = %+v %v", result, err)
	}
	var leaseCleared bool
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT task_name_generation_at IS NULL FROM ao_sessions WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID).Scan(&leaseCleared)
	}); err != nil || !leaseCleared {
		t.Fatalf("lease cleared = %v %v", leaseCleared, err)
	}
	if replay, err := name(domain.TaskNameRequest{Claim: true}); err != nil || replay.Generate {
		t.Fatalf("done claim = %+v %v", replay, err)
	}
}

func TestNameWorkerTaskReservesExtraRepositoryDirectories(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE ao_projects SET config=$3 WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.projectID, `{"coder":{"extraRepos":[{"url":"https://example.test/owner/api"},{"url":"https://example.test/owner/api-2"},{"url":"https://example.test/owner/api-3-2"}]}}`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET task_naming_state='pending',prompt='Fix API' WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	name := func(input domain.TaskNameRequest) (domain.TaskNameResult, error) {
		return store.NameWorkerTask(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, input)
	}
	reserved, err := name(domain.TaskNameRequest{Slug: "api"})
	if err != nil || reserved.Slug != "api-3" {
		t.Fatalf("extra repository reservation = %+v %v", reserved, err)
	}
	if _, err := name(domain.TaskNameRequest{Complete: true, Branch: "ao/api-3-2/root"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("adapter suffix collided with extra repo: %v", err)
	}
	completed, err := name(domain.TaskNameRequest{Complete: true, Branch: "ao/api-3/root"})
	if err != nil || completed.State != "done" {
		t.Fatalf("safe completion = %+v %v", completed, err)
	}
}
