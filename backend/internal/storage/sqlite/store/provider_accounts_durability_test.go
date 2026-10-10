package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func providerFacts() domain.ProviderAccountState {
	return domain.ProviderAccountState{Revision: 1,
		Accounts:  []domain.ProviderAccount{{ID: "alice", Provider: "codex", Email: "alice@example.test", CredentialRef: "alice.json", AuthID: "auth-alice"}, {ID: "clara", Provider: "claude", Email: "clara@example.test", CredentialRef: "clara.json", AuthID: "auth-clara"}},
		Primaries: []domain.ProviderPrimary{{Provider: "codex", PrimaryID: "alice"}, {Provider: "claude", PrimaryID: "clara"}},
		Routes:    []domain.ProviderSessionRoute{{SessionID: "s1", Provider: "codex", AccountID: "alice", TicketHash: strings.Repeat("a", 64)}, {SessionID: "s2", Provider: "claude", AccountID: "clara", TicketHash: strings.Repeat("b", 64)}},
	}
}
func cloneProviderFacts(t *testing.T, state domain.ProviderAccountState) domain.ProviderAccountState {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var result domain.ProviderAccountState
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func openProviderFactsDatabase(t *testing.T) (*sqlite.Store, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+failureStorePragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store, db, dir
}
func TestProviderAccountStorageSurvivesRestartAtEveryJournalBoundary(t *testing.T) {
	for _, phase := range []string{"saved", "committed", "finished", "aborted"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			store, err := sqlitetest.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			next := providerFacts()
			intent := domain.ProviderAccountIntent{Next: next, DeleteCredential: "old.json"}
			if err := store.SaveProviderAccountIntent(context.Background(), 0, intent); err != nil {
				t.Fatal(err)
			}
			if phase == "committed" || phase == "finished" {
				if err := store.CommitProviderAccountIntent(context.Background(), 0); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "finished" {
				if err := store.FinishProviderAccountIntent(context.Background(), 1); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "aborted" {
				if err := store.FinishProviderAccountIntent(context.Background(), 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.OpenPreMigrated(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			effective, pending, err := reopened.LoadProviderAccountState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if phase == "saved" || phase == "aborted" {
				if effective.Revision != 0 || len(effective.Accounts) != 0 || len(effective.Routes) != 0 {
					t.Fatalf("uncommitted facts became effective: %+v", effective)
				}
			} else if !reflect.DeepEqual(effective, next) {
				t.Fatalf("committed facts lost: %+v", effective)
			}
			if phase == "saved" || phase == "committed" {
				if pending == nil || !reflect.DeepEqual(*pending, intent) {
					t.Fatalf("durable intent lost: %+v", pending)
				}
			} else if pending != nil {
				t.Fatalf("completed or refused operation remained pending: %+v", pending)
			}
			if phase == "saved" {
				if err := reopened.CommitProviderAccountIntent(context.Background(), 0); err != nil {
					t.Fatal(err)
				}
				if err := reopened.FinishProviderAccountIntent(context.Background(), 1); err != nil {
					t.Fatal(err)
				}
				got, pending, err := reopened.LoadProviderAccountState(context.Background())
				if err != nil || pending != nil || !reflect.DeepEqual(got, next) {
					t.Fatalf("restart completion=%+v pending=%+v err=%v", got, pending, err)
				}
			}
		})
	}
}
func TestProviderAccountStorageRejectsStructurallyCorruptFacts(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.ProviderAccountState)
	}{
		{"negative-revision", func(s *domain.ProviderAccountState) { s.Revision = -1 }},
		{"empty-id", func(s *domain.ProviderAccountState) { s.Accounts[0].ID = "" }},
		{"duplicate-id", func(s *domain.ProviderAccountState) { s.Accounts[1].ID = s.Accounts[0].ID }},
		{"duplicate-email", func(s *domain.ProviderAccountState) {
			s.Accounts = append(s.Accounts, domain.ProviderAccount{ID: "duplicate", Provider: "codex", Email: "ALICE@example.test"})
		}},
		{"empty-email", func(s *domain.ProviderAccountState) { s.Accounts[0].Email = " " }},
		{"unknown-account-provider", func(s *domain.ProviderAccountState) { s.Accounts[0].Provider = "other" }},
		{"signed-out-with-auth-id", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = "" }},
		{"credential-with-no-auth-id", func(s *domain.ProviderAccountState) { s.Accounts[0].AuthID = "" }},
		{"absolute-credential", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = "/tmp/private.json" }},
		{"credential-parent", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = "../private.json" }},
		{"windows-credential-parent", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = `..\private.json` }},
		{"dot-credential", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = "." }},
		{"parent-credential", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = ".." }},
		{"unknown-primary-provider", func(s *domain.ProviderAccountState) { s.Primaries[0].Provider = "other" }},
		{"duplicate-primary-provider", func(s *domain.ProviderAccountState) { s.Primaries = append(s.Primaries, s.Primaries[0]) }},
		{"missing-primary-account", func(s *domain.ProviderAccountState) { s.Primaries[0].PrimaryID = "gone" }},
		{"cross-provider-primary", func(s *domain.ProviderAccountState) { s.Primaries[0].PrimaryID = "clara" }},
		{"signed-out-primary", func(s *domain.ProviderAccountState) { s.Accounts[0].CredentialRef = ""; s.Accounts[0].AuthID = "" }},
		{"no-adoption", func(s *domain.ProviderAccountState) { s.Primaries = s.Primaries[1:] }},
		{"empty-session", func(s *domain.ProviderAccountState) { s.Routes[0].SessionID = "" }},
		{"duplicate-session", func(s *domain.ProviderAccountState) { s.Routes[1].SessionID = s.Routes[0].SessionID }},
		{"duplicate-ticket", func(s *domain.ProviderAccountState) { s.Routes[1].TicketHash = s.Routes[0].TicketHash }},
		{"empty-ticket", func(s *domain.ProviderAccountState) { s.Routes[0].TicketHash = "" }},
		{"short-ticket", func(s *domain.ProviderAccountState) { s.Routes[0].TicketHash = "a" }},
		{"non-hex-ticket", func(s *domain.ProviderAccountState) { s.Routes[0].TicketHash = strings.Repeat("z", 64) }},
		{"uppercase-ticket", func(s *domain.ProviderAccountState) { s.Routes[0].TicketHash = strings.Repeat("A", 64) }},
		{"unknown-route-provider", func(s *domain.ProviderAccountState) { s.Routes[0].Provider = "other" }},
		{"route-to-missing-account", func(s *domain.ProviderAccountState) { s.Routes[0].AccountID = "gone" }},
		{"route-to-other-provider", func(s *domain.ProviderAccountState) { s.Routes[0].AccountID = "clara" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, db, _ := openProviderFactsDatabase(t)
			bad := providerFacts()
			tc.change(&bad)
			if err := store.SaveProviderAccountIntent(context.Background(), 0, domain.ProviderAccountIntent{Next: bad}); !errors.Is(err, ports.ErrProviderAccountRecovery) {
				t.Fatalf("invalid intent accepted: %v", err)
			}
			state, pending, err := store.LoadProviderAccountState(context.Background())
			if err != nil || state.Revision != 0 || pending != nil {
				t.Fatalf("invalid write changed facts: %+v %+v %v", state, pending, err)
			}
			data, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			revision := bad.Revision
			if revision < 0 {
				revision = 0
			}
			if _, err := db.ExecContext(context.Background(), "UPDATE provider_account_state SET revision=?, facts=? WHERE id=1", revision, string(data)); err != nil {
				t.Fatal(err)
			}
			_, _, err = store.LoadProviderAccountState(context.Background())
			if !errors.Is(err, ports.ErrProviderAccountRecovery) {
				t.Fatalf("corrupt saved facts were trusted: %v", err)
			}
		})
	}
}
func TestProviderAccountStorageWaitingRoutesRemainValidAfterLastSignOut(t *testing.T) {
	store := newTestStore(t)
	next := providerFacts()
	next.Accounts[0].CredentialRef = ""
	next.Accounts[0].AuthID = ""
	next.Primaries[0].PrimaryID = ""
	next.Routes[0].AccountID = ""
	if err := store.SaveProviderAccountIntent(context.Background(), 0, domain.ProviderAccountIntent{Next: next}); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitProviderAccountIntent(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishProviderAccountIntent(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	got, pending, err := store.LoadProviderAccountState(context.Background())
	if err != nil || pending != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("waiting state=%+v pending=%+v err=%v", got, pending, err)
	}
	if got.Routes[0].TicketHash != strings.Repeat("a", 64) {
		t.Fatal("sign-out erased stable ticket identity")
	}
	if got.Primaries[0].Provider != "codex" || got.Primaries[0].PrimaryID != "" {
		t.Fatal("no-account state lost deliberate managed adoption")
	}
	if got.Accounts[0].Email != "alice@example.test" {
		t.Fatal("sign-out removed retained safe identity")
	}
}
func TestProviderAccountStorageConcurrentIntentsHaveExactlyOneWinner(t *testing.T) {
	store := newTestStore(t)
	next := providerFacts()
	var wg sync.WaitGroup
	result := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result <- store.SaveProviderAccountIntent(context.Background(), 0, domain.ProviderAccountIntent{Next: next})
		}()
	}
	wg.Wait()
	close(result)
	successes, conflicts := 0, 0
	for err := range result {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ports.ErrProviderAccountRecovery):
			conflicts++
		default:
			t.Fatalf("unexpected storage failure: %v", err)
		}
	}
	if successes != 1 || conflicts != 11 {
		t.Fatalf("CAS outcomes=%d successes/%d conflicts", successes, conflicts)
	}
	effective, pending, err := store.LoadProviderAccountState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.Revision != 0 || pending == nil || !reflect.DeepEqual(pending.Next, next) {
		t.Fatalf("admitted state=%+v intent=%+v", effective, pending)
	}
	if err := store.CommitProviderAccountIntent(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishProviderAccountIntent(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	effective, pending, err = store.LoadProviderAccountState(context.Background())
	if err != nil || pending != nil || !reflect.DeepEqual(effective, next) {
		t.Fatalf("final=%+v pending=%+v err=%v", effective, pending, err)
	}
}
func TestProviderAccountStorageDetectsRevisionAndIntentCorruption(t *testing.T) {
	for _, damage := range []string{"revision", "pending-revision", "pending-identity", "malformed-facts", "malformed-pending"} {
		t.Run(damage, func(t *testing.T) {
			store, db, _ := openProviderFactsDatabase(t)
			facts := providerFacts()
			facts.Revision = 0
			var err error
			data, _ := json.Marshal(facts)
			switch damage {
			case "revision":
				_, err = db.Exec("UPDATE provider_account_state SET facts=?, revision=7", string(data))
			case "pending-revision":
				next := cloneProviderFacts(t, facts)
				next.Revision = 5
				pending, _ := json.Marshal(domain.ProviderAccountIntent{Next: next})
				_, err = db.Exec("UPDATE provider_account_state SET pending=?", string(pending))
			case "pending-identity":
				next := cloneProviderFacts(t, facts)
				next.Revision = 1
				next.Routes[0].AccountID = "clara"
				pending, _ := json.Marshal(domain.ProviderAccountIntent{Next: next})
				_, err = db.Exec("UPDATE provider_account_state SET pending=?", string(pending))
			case "malformed-facts":
				_, err = db.Exec("UPDATE provider_account_state SET facts='[]'")
			case "malformed-pending":
				_, err = db.Exec("UPDATE provider_account_state SET pending='[]'")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = store.LoadProviderAccountState(context.Background())
			if err == nil {
				t.Fatal("corrupt account journal was trusted")
			}
		})
	}
}
func TestProviderAccountStorageCDCOnlyPublishesCommittedRoutes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "account-cdc")
	first, err := store.CreateSession(ctx, sampleRecord("account-cdc"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSession(ctx, sampleRecord("account-cdc"))
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := store.CreateSession(ctx, sampleRecord("account-cdc"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := store.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := providerFacts()
	next.Routes[0].SessionID = first.ID
	next.Routes[1].SessionID = second.ID
	if err := store.SaveProviderAccountIntent(ctx, 0, domain.ProviderAccountIntent{Next: next}); err != nil {
		t.Fatal(err)
	}
	events, err := store.EventsAfter(ctx, baseline, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("unacknowledged account intent emitted session invalidations")
	}
	if err := store.CommitProviderAccountIntent(ctx, 0); err != nil {
		t.Fatal(err)
	}
	events, err = store.EventsAfter(ctx, baseline, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("committed invalidations=%d want 2", len(events))
	}
	seen := map[string]bool{}
	for _, event := range events {
		if string(event.Type) != "session_updated" {
			t.Fatalf("unexpected event type=%s", event.Type)
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 1 {
			t.Fatalf("account CDC leaked routing facts: %v", payload)
		}
		id, ok := payload["id"].(string)
		if !ok {
			t.Fatalf("missing CDC session identity=%v", payload)
		}
		if id == string(unrelated.ID) {
			t.Fatal("account CDC invalidated unrelated session")
		}
		seen[id] = true
	}
	if !seen[string(first.ID)] || !seen[string(second.ID)] {
		t.Fatalf("affected identities=%v", seen)
	}
	committedSeq, err := store.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishProviderAccountIntent(ctx, 1); err != nil {
		t.Fatal(err)
	}
	events, err = store.EventsAfter(ctx, committedSeq, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("credential cleanup emitted duplicate CDC: %v %v", events, err)
	}
	next.Revision = 2
	next.Routes = next.Routes[1:]
	if err := store.SaveProviderAccountIntent(ctx, 1, domain.ProviderAccountIntent{Next: next}); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitProviderAccountIntent(ctx, 1); err != nil {
		t.Fatal(err)
	}
	events, err = store.EventsAfter(ctx, committedSeq, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen = map[string]bool{}
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		seen[payload["id"].(string)] = true
	}
	if !seen[string(first.ID)] {
		t.Fatal("revoking a seed route did not invalidate the previous owner")
	}
	if !seen[string(second.ID)] {
		t.Fatal("route snapshot change did not invalidate retained owner")
	}
	if seen[string(unrelated.ID)] {
		t.Fatal("revocation invalidated unrelated owner")
	}
}
