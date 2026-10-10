package host

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testRoutes(t *testing.T) *Routes {
	t.Helper()
	r, err := OpenRoutes(filepath.Join(t.TempDir(), "run", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func testRoute(session, ticket, provider, account string) Route {
	return Route{SessionID: session, TicketHash: TicketHash(ticket), Provider: provider, AuthID: account}
}
func applyRoutes(t *testing.T, r *Routes, revision uint64, routes ...Route) {
	t.Helper()
	if err := r.Apply(Snapshot{Revision: revision, Routes: routes}); err != nil {
		t.Fatal(err)
	}
}
func TestRoutesRestorePrivateSnapshot(t *testing.T) {
	r := testRoutes(t)
	want := Snapshot{Revision: 1, Routes: []Route{testRoute("s1", "ticket-a", "codex", "alice"), testRoute("s2", "ticket-b", "claude", "bob")}}
	if err := r.Apply(want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ticket-a") || strings.Contains(string(data), "ticket-b") {
		t.Fatal("raw ticket persisted")
	}
	stat, err := os.Stat(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", stat.Mode().Perm())
	}
	restored, err := OpenRoutes(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, restored.Snapshot()) {
		t.Fatalf("restored=%+v", restored.Snapshot())
	}
	for _, tc := range []struct{ ticket, account string }{{"ticket-a", "alice"}, {"ticket-b", "bob"}} {
		route, release, err := restored.Acquire(tc.ticket)
		if err != nil {
			t.Fatal(err)
		}
		if route.AuthID != tc.account {
			t.Fatalf("route=%+v", route)
		}
		release()
	}
}

func TestEmptyRoutesReplayAndRestoreWithoutChangingTheirWireShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		routes []Route
	}{{"empty-list", []Route{}}, {"null-list", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			routes := testRoutes(t)
			want := Snapshot{Revision: 1, Routes: tc.routes}
			if err := routes.Apply(want); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(routes.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, replay := range []Snapshot{want, routes.Snapshot()} {
				if err := routes.Apply(replay); err != nil {
					t.Fatalf("empty snapshot replay refused: %v", err)
				}
			}
			restored, err := OpenRoutes(routes.path)
			if err != nil || !reflect.DeepEqual(restored.Snapshot(), want) {
				t.Fatalf("empty snapshot lost its durable representation: %v", err)
			}
			if err := restored.Apply(want); err != nil {
				t.Fatalf("reopened empty snapshot replay refused: %v", err)
			}
			after, err := os.ReadFile(routes.path)
			if err != nil || string(after) != string(before) {
				t.Fatal("exact replay changed the durable snapshot")
			}
			if _, done, err := restored.Acquire("old-session-ticket"); err == nil || done != nil {
				t.Fatal("empty snapshot admitted an old session")
			}
		})
	}
}
func TestRouteChangeWaitsForWholeRequest(t *testing.T) {
	r := testRoutes(t)
	a := testRoute("s1", "ticket-a", "codex", "alice")
	b := testRoute("s2", "ticket-b", "codex", "bob")
	applyRoutes(t, r, 1, a, b)
	selected, release, err := r.Acquire("ticket-a")
	if err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.AuthID = "bob"
	if err = r.Apply(Snapshot{Revision: 2, Routes: []Route{changed, b}}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v", err)
	}
	if selected.AuthID != "alice" || r.Snapshot().Revision != 1 {
		t.Fatal("changed an accepted request")
	}
	release()
	release()
	applyRoutes(t, r, 2, changed, b)
	next, done, err := r.Acquire("ticket-a")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if next.AuthID != "bob" {
		t.Fatalf("next=%+v", next)
	}
}
func TestIndependentAccountCanChangeWhileAnotherRequestRuns(t *testing.T) {
	r := testRoutes(t)
	a := testRoute("s1", "ticket-a", "codex", "alice")
	b := testRoute("s2", "ticket-b", "codex", "bob")
	applyRoutes(t, r, 1, a, b)
	_, release, err := r.Acquire("ticket-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	b.AuthID = "alice"
	applyRoutes(t, r, 2, a, b)
	if r.Snapshot().Revision != 2 {
		t.Fatal("unrelated route was blocked")
	}
}
func TestLastAccountSignOutAndRecoveryUsesSameTicket(t *testing.T) {
	r := testRoutes(t)
	route := testRoute("s1", "stable-ticket", "claude", "alice")
	applyRoutes(t, r, 1, route)
	route.AuthID = ""
	applyRoutes(t, r, 2, route)
	if _, release, err := r.Acquire("stable-ticket"); err == nil || release != nil {
		t.Fatal("signed out request admitted")
	}
	route.AuthID = "new-account"
	applyRoutes(t, r, 3, route)
	got, release, err := r.Acquire("stable-ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got.AuthID != "new-account" {
		t.Fatalf("got=%+v", got)
	}
}
func TestRemoveRouteWhileActiveFails(t *testing.T) {
	r := testRoutes(t)
	applyRoutes(t, r, 1, testRoute("s1", "ticket", "codex", "alice"))
	_, release, err := r.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Apply(Snapshot{Revision: 2, Routes: []Route{}}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v", err)
	}
	release()
	applyRoutes(t, r, 2)
	if _, _, err = r.Acquire("ticket"); err == nil {
		t.Fatal("removed ticket usable")
	}
}
func TestSnapshotAdmissionValidation(t *testing.T) {
	base := testRoute("s1", "ticket", "codex", "alice")
	for _, tc := range []struct {
		name   string
		routes []Route
	}{
		{"missing-session", []Route{{TicketHash: base.TicketHash, Provider: "codex"}}},
		{"invalid-hash", []Route{{SessionID: "s1", TicketHash: "short", Provider: "codex"}}},
		{"unknown-provider", []Route{{SessionID: "s1", TicketHash: base.TicketHash, Provider: "other"}}},
		{"duplicate-ticket", []Route{base, {SessionID: "s2", TicketHash: base.TicketHash, Provider: "codex"}}},
		{"duplicate-session", []Route{base, testRoute("s1", "other-ticket", "codex", "bob")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRoutes(t)
			if err := r.Apply(Snapshot{Revision: 1, Routes: tc.routes}); err == nil {
				t.Fatal("invalid snapshot admitted")
			}
			if r.Snapshot().Revision != 0 {
				t.Fatal("invalid write changed revision")
			}
			if _, err := os.Stat(r.path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid write persisted: %v", err)
			}
		})
	}
}
func TestRevisionReplayAndConflict(t *testing.T) {
	r := testRoutes(t)
	s := Snapshot{Revision: 1, Routes: []Route{testRoute("s1", "ticket", "codex", "alice")}}
	if err := r.Apply(s); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(s); err != nil {
		t.Fatalf("lost ack replay=%v", err)
	}
	for _, revision := range []uint64{0, 1} {
		changed := Snapshot{Revision: revision, Routes: []Route{testRoute("s1", "ticket", "codex", "bob")}}
		if err := r.Apply(changed); !errors.Is(err, ErrRevision) {
			t.Fatalf("revision=%d err=%v", revision, err)
		}
	}
	metadataOnly := s
	metadataOnly.AuthIDs = []string{"alice"}
	if err := r.Apply(metadataOnly); !errors.Is(err, ErrRevision) {
		t.Fatalf("same-revision metadata change err=%v", err)
	}
	if !reflect.DeepEqual(s, r.Snapshot()) {
		t.Fatal("conflict changed snapshot")
	}
	// AO owns routing: a newer revision replaces the cache even across a gap.
	for _, revision := range []uint64{3, 99} {
		changed := Snapshot{Revision: revision, Routes: []Route{testRoute("s1", "ticket", "codex", "bob")}}
		if err := r.Apply(changed); err != nil {
			t.Fatalf("revision=%d err=%v", revision, err)
		}
		if !reflect.DeepEqual(changed, r.Snapshot()) {
			t.Fatalf("revision=%d did not replace the cache", revision)
		}
	}
}
func TestRevisionGapStillWaitsForWholeRequest(t *testing.T) {
	r := testRoutes(t)
	a := testRoute("s1", "ticket", "codex", "alice")
	applyRoutes(t, r, 1, a)
	before, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := r.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.AuthID = "bob"
	for _, routes := range [][]Route{{changed}, {}} {
		if err = r.Apply(Snapshot{Revision: 9, Routes: routes}); !errors.Is(err, ErrBusy) {
			t.Fatalf("err=%v", err)
		}
	}
	after, err := os.ReadFile(r.path)
	if err != nil || string(before) != string(after) || r.Snapshot().Revision != 1 {
		t.Fatal("busy refusal changed the cache")
	}
	release()
	applyRoutes(t, r, 9, changed)
}
func TestSnapshotDoesNotShareMutableSlices(t *testing.T) {
	r := testRoutes(t)
	s := Snapshot{Revision: 1, Routes: []Route{testRoute("s1", "ticket", "codex", "alice")}}
	if err := r.Apply(s); err != nil {
		t.Fatal(err)
	}
	s.Routes[0].AuthID = "spoof"
	read := r.Snapshot()
	read.Routes[0].AuthID = "spoof-again"
	got, release, err := r.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got.AuthID != "alice" || r.Snapshot().Routes[0].AuthID != "alice" {
		t.Fatal("external mutation reached table")
	}
}
func TestUnknownAndEmptyTicketDeny(t *testing.T) {
	r := testRoutes(t)
	applyRoutes(t, r, 1, testRoute("s1", "valid", "codex", "alice"))
	for _, ticket := range []string{"", "unknown", "VALID", " valid", "valid "} {
		if _, done, err := r.Acquire(ticket); err == nil || done != nil {
			t.Fatalf("ticket %q admitted", ticket)
		}
	}
}
func TestWriteFailureRetainsPreviousRoutes(t *testing.T) {
	r := testRoutes(t)
	a := testRoute("s1", "ticket", "codex", "alice")
	applyRoutes(t, r, 1, a)
	// A directory at the rename target deterministically fails on all platforms.
	r.path = filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(r.path, 0700); err != nil {
		t.Fatal(err)
	}
	a.AuthID = "bob"
	if err := r.Apply(Snapshot{Revision: 2, Routes: []Route{a}}); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	got, done, err := r.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if got.AuthID != "alice" || r.Snapshot().Revision != 1 {
		t.Fatal("failed persistence changed effective route")
	}
}
func TestCorruptSnapshotIsDiscardedAndRefilled(t *testing.T) {
	for _, data := range []string{"invalid-json", `{"revision":1,"routes":[{"session_id":"s","ticket_hash":"bad","provider":"codex"}]}`} {
		path := filepath.Join(t.TempDir(), "routes.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		r, err := OpenRoutes(path)
		if err != nil {
			t.Fatalf("corrupt cache stopped the helper: %v", err)
		}
		if got := r.Snapshot(); got.Revision != 0 || len(got.Routes) != 0 {
			t.Fatal("corrupt cache was served")
		}
		if _, done, err := r.Acquire("ticket"); err == nil || done != nil {
			t.Fatal("corrupt cache admitted a ticket")
		}
		refill := Snapshot{Revision: 7, Routes: []Route{testRoute("s", "ticket", "codex", "alice")}}
		if err := r.Apply(refill); err != nil {
			t.Fatalf("refill refused: %v", err)
		}
		restored, err := OpenRoutes(path)
		if err != nil || !reflect.DeepEqual(restored.Snapshot(), refill) {
			t.Fatal("refilled cache was not restored", err)
		}
		got, done, err := restored.Acquire("ticket")
		if err != nil || got.AuthID != "alice" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		done()
	}
}
func TestMultipleConcurrentRequestsRequireAllReleases(t *testing.T) {
	r := testRoutes(t)
	route := testRoute("s1", "ticket", "codex", "alice")
	applyRoutes(t, r, 1, route)
	var releases []func()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, done, err := r.Acquire("ticket")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			releases = append(releases, done)
			mu.Unlock()
		}()
	}
	wg.Wait()
	route.AuthID = "bob"
	for i, done := range releases {
		if err := r.Apply(Snapshot{Revision: 2, Routes: []Route{route}}); !errors.Is(err, ErrBusy) {
			t.Fatalf("before release %d err=%v", i, err)
		}
		done()
	}
	applyRoutes(t, r, 2, route)
}
