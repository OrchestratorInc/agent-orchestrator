package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerTaskNamingHTTPPostgresLifecycle(t *testing.T) {
	databaseURL := os.Getenv("AO_CLOUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AO_CLOUD_TEST_DATABASE_URL is not set; skipping PostgreSQL worker naming integration test")
	}
	ctx := context.Background()
	if err := postgres.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	orgID, userID, projectID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id',$1,true),set_config('ao.org_id',$2,true)`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO ao_users(id,auth_provider,external_user_id,email,display_name,password_hash) VALUES ($1::uuid,'local',$1::uuid::text,$1::uuid::text||'@example.test','Worker Naming Test','hash')`, []any{userID}},
		{`INSERT INTO ao_organizations(id,auth_provider,slug,display_name,kind,owner_user_id,created_by_user_id) VALUES ($1::uuid,'local',$1::uuid::text,'Worker Naming Test','personal',$2,$2)`, []any{orgID, userID}},
		{`INSERT INTO ao_org_memberships(org_id,user_id,role) VALUES ($1,$2,'owner')`, []any{orgID, userID}},
		{`INSERT INTO ao_projects(id,org_id,display_name,repository_url,default_branch) VALUES ($1,$2,'Project',$1::uuid::text,'main')`, []any{projectID, orgID}},
		{`INSERT INTO ao_sessions(id,org_id,project_id,kind,harness,display_name,branch,prompt,created_by_user_id,task_naming_state,task_name_initial_title) VALUES ($1,$2,$3,'worker','codex','Fix login redirect','ao/12345678','Fix login redirect',$4,'pending','Fix login redirect')`, []any{sessionID, orgID, projectID, userID}},
		{`INSERT INTO ao_sandboxes(session_id,org_id,provider) VALUES ($1,$2,'docker')`, []any{sessionID, orgID}},
		{`INSERT INTO ao_worker_connections(session_id,org_id,sandbox_id,epoch,worker_id,version) VALUES ($1,$2,$1,1,'worker','test')`, []any{sessionID, orgID}},
	} {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tx, err := admin.Begin(context.Background())
		if err == nil {
			_, _ = tx.Exec(context.Background(), `SELECT set_config('ao.user_id',$1,true),set_config('ao.org_id',$2,true)`, userID, orgID)
			_, _ = tx.Exec(context.Background(), `DELETE FROM ao_organizations WHERE id=$1`, orgID)
			_ = tx.Commit(context.Background())
		}
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id=$1`, userID)
	})
	tokens := worker.NewTokenManager([]byte("task-naming-real-postgres-test-key"))
	srv := New(Options{Store: store, WorkerTokens: tokens, Logger: slog.New(slog.DiscardHandler)})
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	issue := func(scopes []string, epoch int64) string {
		token, err := tokens.Issue(worker.Claims{OrgID: orgID, SessionID: sessionID, WorkerID: "worker", Epoch: epoch, Scopes: scopes}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	token := issue([]string{"worker:connect"}, 1)
	request := func(body, bearer string, status int) domain.TaskNameResult {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/cloud/v1/worker/task-name", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Worker "+bearer)
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("HTTP naming %s: status=%d body=%s", body, response.StatusCode, raw)
		}
		var result domain.TaskNameResult
		if status == 200 {
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	request(`{"claim":true}`, "", 401)
	request(`{"claim":true}`, issue([]string{"worker:transport"}, 1), 403)
	request(`{"claim":true}`, issue([]string{"worker:connect"}, 2), 401)
	if claim := request(`{"claim":true}`, token, 200); !claim.Generate || claim.State != "pending" {
		t.Fatalf("HTTP admission = %+v", claim)
	}
	reserved := request(`{"title":"Repair login","slug":"login-redirect","occupied":["login-redirect"]}`, token, 200)
	if reserved.State != "reserved" || reserved.Branch != "ao/login-redirect-2/root" || reserved.Title != "Repair login" {
		t.Fatalf("HTTP reservation = %+v", reserved)
	}
	request(`{"complete":true,"branch":"ao/unrelated/root"}`, token, 422)
	completed := request(`{"complete":true,"branch":"ao/login-redirect-2-2/root"}`, token, 200)
	if completed.State != "done" || completed.Slug != "login-redirect-2-2" {
		t.Fatalf("HTTP completion = %+v", completed)
	}
	launch, err := store.WorkerLaunchSpec(ctx, orgID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if launch.TaskNamingState != "done" || launch.Branch != completed.Branch || launch.TaskNameSlug != completed.Slug || launch.DisplayName != "Repair login" {
		t.Fatalf("durable launch = %+v", launch)
	}
}
