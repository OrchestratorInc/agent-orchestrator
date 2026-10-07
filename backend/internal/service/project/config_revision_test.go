package project_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type snapshotProjectStore struct {
	project.Store
	afterRead func(domain.ProjectRecord)
}

func (s snapshotProjectStore) GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	row, ok, err := s.Store.GetProject(ctx, id)
	if err == nil && ok {
		s.afterRead(row)
	}
	return row, ok, err
}

func configAtRevision(t *testing.T, revision int64, branch string) project.SetConfigInput {
	t.Helper()
	var in project.SetConfigInput
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"config":{"defaultBranch":%q},"expectedRevision":%d}`, branch, revision)), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func requireRevisionConflict(t *testing.T, err error) {
	t.Helper()
	var api *apierr.Error
	if !errors.As(err, &api) || api.Kind != apierr.KindConflict || api.Code != "PROJECT_REVISION_CONFLICT" {
		t.Fatalf("want PROJECT_REVISION_CONFLICT, got %v", err)
	}
}

func TestSetConfigSnapshotCASPreservesConcurrentEdits(t *testing.T) {
	for _, change := range []string{"unrelated", "archive", "ABA"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			store := sqlitetest.MustOpen(t)
			seed := domain.ProjectRecord{ID: "cas", Path: t.TempDir(), DisplayName: "Original", RegisteredAt: time.Now()}
			if err := store.UpsertProject(ctx, seed); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			mgr := project.NewWithDeps(project.Deps{Store: snapshotProjectStore{Store: store, afterRead: func(row domain.ProjectRecord) {
				switch change {
				case "unrelated":
					row.DisplayName = "Concurrent"
					row.RepoOriginURL = "https://github.com/example/concurrent.git"
					row.Kind = domain.ProjectKindWorkspace
					row.Path = t.TempDir()
					if err := store.UpsertProject(ctx, row); err != nil {
						t.Fatal(err)
					}
				case "archive":
					if ok, err := store.ArchiveProject(ctx, row.ID, time.Now()); err != nil || !ok {
						t.Fatalf("archive: %v %v", ok, err)
					}
				case "ABA":
					for _, name := range []string{"Temporary", row.DisplayName} {
						if _, ok, err := store.UpdateProjectSettings(ctx, row.ID, name, row.Config); err != nil || !ok {
							t.Fatalf("settings: %v %v", ok, err)
						}
					}
				}
			}}, OnModelScopeChanged: func(string) { calls.Add(1) }})
			_, err := mgr.SetConfig(ctx, "cas", project.SetConfigInput{Config: domain.ProjectConfig{DefaultBranch: "develop"}})
			requireRevisionConflict(t, err)
			if calls.Load() != 0 {
				t.Fatalf("conflict invalidated model scope %d times", calls.Load())
			}
			row, _, err := store.GetProject(ctx, "cas")
			if err != nil || !row.Config.IsZero() {
				t.Fatalf("conflict wrote config: row=%#v err=%v", row, err)
			}
			switch change {
			case "unrelated":
				if row.DisplayName != "Concurrent" || row.RepoOriginURL != "https://github.com/example/concurrent.git" || row.Kind != domain.ProjectKindWorkspace || row.Path == seed.Path || !row.RegisteredAt.Equal(seed.RegisteredAt) {
					t.Fatalf("concurrent fields overwritten: %#v", row)
				}
				before := row
				p, err := project.New(store).SetConfig(ctx, "cas", project.SetConfigInput{})
				if err != nil {
					t.Fatal(err)
				}
				row, _, err = store.GetProject(ctx, "cas")
				if err != nil || p.Revision != row.Revision || row.Revision != before.Revision+1 {
					t.Fatalf("committed revision mismatch: %#v %v", p, err)
				}
				before.Config = row.Config
				before.Revision = row.Revision
				if !reflect.DeepEqual(before, row) {
					t.Fatalf("config update changed unrelated fields: before=%#v after=%#v", before, row)
				}
			case "archive":
				if row.ArchivedAt.IsZero() {
					t.Fatal("archived project was resurrected")
				}
				_, err := project.New(store).SetConfig(ctx, "cas", project.SetConfigInput{})
				var api *apierr.Error
				if !errors.As(err, &api) || api.Code != "PROJECT_NOT_FOUND" {
					t.Fatalf("already archived update: %v", err)
				}
			case "ABA":
				if row.DisplayName != seed.DisplayName {
					t.Fatal("ABA did not restore original display name")
				}
			}
		})
	}
}

func TestSetConfigEqualRevisionConcurrentWriters(t *testing.T) {
	for _, precondition := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", precondition), func(t *testing.T) {
			ctx := context.Background()
			store := sqlitetest.MustOpen(t)
			if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "cas", Path: t.TempDir(), RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			arrivals := make(chan struct{}, 2)
			release := make(chan struct{})
			var calls atomic.Int64
			mgr := project.NewWithDeps(project.Deps{Store: snapshotProjectStore{Store: store, afterRead: func(domain.ProjectRecord) {
				arrivals <- struct{}{}
				<-release
			}}, OnModelScopeChanged: func(string) { calls.Add(1) }})
			type result struct {
				p   project.Project
				err error
			}
			results := make(chan result, 2)
			for _, branch := range []string{"alpha", "beta"} {
				in := project.SetConfigInput{Config: domain.ProjectConfig{DefaultBranch: branch}}
				if precondition {
					in = configAtRevision(t, 0, branch)
				}
				go func() {
					p, err := mgr.SetConfig(ctx, "cas", in)
					results <- result{p, err}
				}()
			}
			<-arrivals
			<-arrivals
			close(release)
			winners := 0
			var winner project.Project
			for range 2 {
				got := <-results
				if got.err == nil {
					winners++
					winner = got.p
				} else {
					requireRevisionConflict(t, got.err)
				}
			}
			if winners != 1 || calls.Load() != 1 {
				t.Fatalf("winners=%d invalidations=%d, want exactly one each", winners, calls.Load())
			}
			row, _, err := store.GetProject(ctx, "cas")
			if err != nil || winner.Config == nil || winner.Config.DefaultBranch != row.Config.DefaultBranch || winner.Revision != row.Revision || row.Revision != 1 {
				t.Fatalf("winner not committed row: winner=%#v row=%#v err=%v", winner, row, err)
			}
		})
	}
}

func TestProjectRegistrationReturnsRevisionWhenReusingArchivedID(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "reused", Path: "/archived", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if updated, err := store.ArchiveProject(ctx, "reused", time.Now()); err != nil || !updated {
		t.Fatalf("archive: %v %v", updated, err)
	}
	id := "reused"
	p, err := project.New(store).Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: &id})
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := store.GetProject(ctx, id)
	if err != nil || !row.ArchivedAt.IsZero() || p.Revision != row.Revision || p.Revision != 2 {
		t.Fatalf("reused registration returned stale revision: p=%#v row=%#v err=%v", p, row, err)
	}
}

func TestSetConfigStalePreconditionDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "cas", Path: t.TempDir(), RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	mgr := project.NewWithDeps(project.Deps{Store: store, OnModelScopeChanged: func(string) { calls.Add(1) }})
	if _, err := mgr.SetConfig(ctx, "cas", project.SetConfigInput{}); err != nil {
		t.Fatal(err)
	}
	_, err := mgr.SetConfig(ctx, "cas", configAtRevision(t, 0, "stale"))
	requireRevisionConflict(t, err)
	if calls.Load() != 1 {
		t.Fatalf("stale precondition invalidated: calls=%d", calls.Load())
	}
	row, _, err := store.GetProject(ctx, "cas")
	if err != nil || !row.Config.IsZero() || row.Revision != 1 {
		t.Fatalf("stale precondition changed row: %#v %v", row, err)
	}
}
