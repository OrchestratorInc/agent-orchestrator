package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProviderAccountStorageIntentCommitAndFinish(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	initial, pending, err := s.LoadProviderAccountState(ctx)
	if err != nil || pending != nil || initial.Revision != 0 || len(initial.Accounts) != 0 || len(initial.Primaries) != 0 || len(initial.Routes) != 0 {
		t.Fatalf("initial=%+v pending=%+v err=%v", initial, pending, err)
	}
	next := domain.ProviderAccountState{Revision: 1, Accounts: []domain.ProviderAccount{{ID: "alice", Provider: "codex", Email: "alice@example.com", CredentialRef: "saved.json", AuthID: "auth-alice"}}, Primaries: []domain.ProviderPrimary{{Provider: "codex", PrimaryID: "alice"}}}
	intent := domain.ProviderAccountIntent{Next: next, DeleteCredential: "old.json"}
	if err = s.SaveProviderAccountIntent(ctx, 0, intent); err != nil {
		t.Fatal(err)
	}
	before, pending, err := s.LoadProviderAccountState(ctx)
	if err != nil || !reflect.DeepEqual(before, initial) || pending == nil || !reflect.DeepEqual(*pending, intent) {
		t.Fatalf("before=%+v pending=%+v err=%v", before, pending, err)
	}
	if err = s.CommitProviderAccountIntent(ctx, 0); err != nil {
		t.Fatal(err)
	}
	effective, pending, err := s.LoadProviderAccountState(ctx)
	if err != nil || !reflect.DeepEqual(effective, next) || pending == nil || pending.DeleteCredential != "old.json" {
		t.Fatalf("effective=%+v pending=%+v err=%v", effective, pending, err)
	}
	if err = s.FinishProviderAccountIntent(ctx, 1); err != nil {
		t.Fatal(err)
	}
	got, pending, err := s.LoadProviderAccountState(ctx)
	if err != nil || pending != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("got=%+v pending=%+v err=%v", got, pending, err)
	}
}
func TestProviderAccountStorageCompareAndSwap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	intent := domain.ProviderAccountIntent{Next: domain.ProviderAccountState{Revision: 1}}
	if err := s.SaveProviderAccountIntent(ctx, 0, intent); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProviderAccountIntent(ctx, 0, intent); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("second admission=%v", err)
	}
	if err := s.CommitProviderAccountIntent(ctx, 99); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("stale commit=%v", err)
	}
	if err := s.FinishProviderAccountIntent(ctx, 99); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("stale finish=%v", err)
	}
	if err := s.CommitProviderAccountIntent(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitProviderAccountIntent(ctx, 0); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("repeat commit=%v", err)
	}
	if err := s.FinishProviderAccountIntent(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishProviderAccountIntent(ctx, 1); !errors.Is(err, ports.ErrProviderAccountRecovery) {
		t.Fatalf("repeat finish=%v", err)
	}
}
func TestProviderAccountStorageRejectsRevisionGaps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, revision := range []int64{-1, 0, 2, 99} {
		if err := s.SaveProviderAccountIntent(ctx, 0, domain.ProviderAccountIntent{Next: domain.ProviderAccountState{Revision: revision}}); !errors.Is(err, ports.ErrProviderAccountRecovery) {
			t.Fatalf("revision=%d err=%v", revision, err)
		}
	}
	state, pending, err := s.LoadProviderAccountState(ctx)
	if err != nil || state.Revision != 0 || pending != nil {
		t.Fatalf("state=%+v pending=%+v err=%v", state, pending, err)
	}
}
func TestProviderAccountStorageCancellationLeavesFacts(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SaveProviderAccountIntent(ctx, 0, domain.ProviderAccountIntent{Next: domain.ProviderAccountState{Revision: 1}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	_, pending, err := s.LoadProviderAccountState(context.Background())
	if err != nil || pending != nil {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}
