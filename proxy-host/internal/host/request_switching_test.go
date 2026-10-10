package host

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func requestSwitchSnapshot(revision uint64, route Route, ids ...string) Snapshot {
	return Snapshot{Revision: revision, Routes: []Route{route}, AuthIDs: ids, RequestBoundary: true}
}

func TestCodexRebindRetainsEveryOutstandingAccountLease(t *testing.T) {
	routes := testRoutes(t)
	original := testRoute("session", "ticket", "codex", "a")
	if err := routes.Apply(requestSwitchSnapshot(1, original, "a", "b", "c")); err != nil {
		t.Fatal(err)
	}
	a, finishA, err := routes.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer finishA()
	b := original
	b.AuthID = "b"
	if err = routes.Apply(requestSwitchSnapshot(2, b, "a", "b", "c")); err != nil {
		t.Fatal(err)
	}
	selectedB, finishB, err := routes.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer finishB()
	c := original
	c.AuthID = "c"
	if err = routes.Apply(requestSwitchSnapshot(3, c, "a", "b", "c")); err != nil {
		t.Fatal(err)
	}
	selectedC, finishC, err := routes.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer finishC()
	if a.AuthID != "a" || selectedB.AuthID != "b" || selectedC.AuthID != "c" {
		t.Fatal("rebind rewrote an accepted request")
	}
	before := routes.Snapshot()
	dataBefore, _ := os.ReadFile(routes.path)
	for _, ids := range [][]string{{"b", "c"}, {"a", "c"}, {"c"}} {
		if err = routes.Apply(requestSwitchSnapshot(4, c, ids...)); !errors.Is(err, ErrBusy) {
			t.Fatalf("retired outstanding credential %v: %v", ids, err)
		}
		dataAfter, _ := os.ReadFile(routes.path)
		if !reflect.DeepEqual(before, routes.Snapshot()) || string(dataBefore) != string(dataAfter) {
			t.Fatal("busy retirement partially committed")
		}
	}
	finishA()
	finishA()
	if err = routes.Apply(requestSwitchSnapshot(4, c, "b", "c")); err != nil {
		t.Fatal(err)
	}
	if err = routes.Apply(requestSwitchSnapshot(5, c, "c")); !errors.Is(err, ErrBusy) {
		t.Fatal("releasing A released B's lease")
	}
	finishB()
	finishB()
	if err = routes.Apply(requestSwitchSnapshot(5, c, "c")); err != nil {
		t.Fatal(err)
	}
	if routes.Snapshot().RequestBoundary {
		t.Fatal("operation instruction persisted as routing fact")
	}
	restored, err := OpenRoutes(routes.path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(routes.Snapshot(), restored.Snapshot()) {
		t.Fatal("restart lost signed-in inventory")
	}
	if err = restored.Apply(requestSwitchSnapshot(5, c, "c")); err != nil {
		t.Fatal("lost acknowledgement replay changed admission mode")
	}
}

func TestRequestBoundaryCannotChangeCapabilityProviderOrRevokeBusyRoute(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, mutation := range []string{"rebind", "remove", "signed-out", "provider", "session", "ticket", "missing-inventory"} {
			t.Run(provider+"/"+mutation, func(t *testing.T) {
				routes := testRoutes(t)
				old := testRoute("s", "ticket", provider, "a")
				if err := routes.Apply(requestSwitchSnapshot(1, old, "a", "b")); err != nil {
					t.Fatal(err)
				}
				selected, done, err := routes.Acquire("ticket")
				if err != nil {
					t.Fatal(err)
				}
				defer done()
				next := requestSwitchSnapshot(2, old, "a", "b")
				switch mutation {
				case "rebind":
					next.Routes[0].AuthID = "b"
				case "remove":
					next.Routes = nil
				case "signed-out":
					next.Routes[0].AuthID = ""
				case "provider":
					if provider == "codex" {
						next.Routes[0].Provider = "claude"
					} else {
						next.Routes[0].Provider = "codex"
					}
				case "session":
					next.Routes[0].SessionID = "other"
				case "ticket":
					next.Routes[0].TicketHash = TicketHash("replacement")
				case "missing-inventory":
					next.Routes[0].AuthID = "b"
					next.AuthIDs = nil
				}
				err = routes.Apply(next)
				if mutation == "rebind" {
					if err != nil {
						t.Fatal(err)
					}
					newRoute, release, err := routes.Acquire("ticket")
					if err != nil {
						t.Fatal(err)
					}
					release()
					if newRoute.AuthID != "b" || selected.AuthID != "a" {
						t.Fatal("request boundary not preserved")
					}
				} else if err == nil || routes.Snapshot().Revision != 1 {
					t.Fatalf("unsafe mutation accepted: %v", err)
				}
			})
		}
	}
}

func TestRequestBoundaryAdmissionRaceUsesCompleteMapping(t *testing.T) {
	routes := testRoutes(t)
	old := testRoute("s", "ticket", "codex", "a")
	if err := routes.Apply(requestSwitchSnapshot(1, old, "a", "b")); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for n := 0; n < 8; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				selected, done, err := routes.Acquire("ticket")
				if err != nil {
					failures <- err
					return
				}
				if selected != old && selected != (Route{SessionID: old.SessionID, TicketHash: old.TicketHash, Provider: old.Provider, AuthID: "b"}) {
					failures <- fmt.Errorf("mixed mapping: %+v", selected)
					done()
					return
				}
				done()
				done()
			}
		}()
	}
	for revision := uint64(2); revision < 102; revision++ {
		next := old
		if revision%2 == 0 {
			next.AuthID = "b"
		}
		if err := routes.Apply(requestSwitchSnapshot(revision, next, "a", "b")); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

func TestRequestBoundaryRealHTTPAndSSEKeepAAndAdmitB(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream-%t", stream), func(t *testing.T) {
			server, routes, execution := heldRoutingServer(t)
			initial := routes.Snapshot()
			initial.Revision++
			initial.AuthIDs = []string{"held-a", "held-b"}
			if err := routes.Apply(initial); err != nil {
				t.Fatal(err)
			}
			first, cancelFirst := requestHeld(t, server, "ticket-a", stream)
			defer cancelFirst()
			awaitAccount(t, execution, "held-a")
			next := routes.Snapshot()
			next.Revision++
			next.RequestBoundary = true
			next.Routes[0].AuthID = "held-b"
			if err := routes.Apply(next); err != nil {
				t.Fatal(err)
			}
			second, cancelSecond := requestHeld(t, server, "ticket-a", stream)
			defer cancelSecond()
			awaitAccount(t, execution, "held-b")
			retire := next
			retire.Revision++
			retire.AuthIDs = []string{"held-b"}
			if err := routes.Apply(retire); !errors.Is(err, ErrBusy) {
				t.Fatalf("outstanding A retired: %v", err)
			}
			close(execution.release)
			awaitResponse(t, first, "held-a")
			awaitResponse(t, second, "held-b")
			// Drain the middleware lease; client EOF may arrive first.
			deadline := time.Now().Add(time.Second)
			for {
				routes.mu.Lock()
				activeA := routes.activeAuth["held-a"]
				routes.mu.Unlock()
				if activeA == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("completed response retained A lease")
				}
				time.Sleep(time.Millisecond)
			}
			if err := routes.Apply(retire); err != nil {
				t.Fatal(err)
			}
			completed := execution.recorded()
			slices.Sort(completed) // Concurrent responses may finish in either order.
			if !reflect.DeepEqual(completed, []string{"held-a", "held-b"}) {
				t.Fatalf("account substitution: %v", completed)
			}
		})
	}
}

func TestRequestBoundaryFailedWriteKeepsMappingInventoryAndLease(t *testing.T) {
	r := testRoutes(t)
	old := testRoute("s", "ticket", "codex", "a")
	if err := r.Apply(requestSwitchSnapshot(1, old, "a", "b")); err != nil {
		t.Fatal(err)
	}
	before := r.Snapshot()
	originalPath := r.path
	r.path = originalPath + "/cannot-create-below-file"
	accepted, done, err := r.Acquire("ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	next := before
	next.Routes = append([]Route(nil), before.Routes...)
	next.Routes[0].AuthID = "b"
	next.Revision++
	next.RequestBoundary = true
	if err = r.Apply(next); err == nil {
		t.Fatal("failed durable write reported success")
	}
	if !reflect.DeepEqual(before, r.Snapshot()) || accepted.AuthID != "a" {
		t.Fatal("failed write changed effective account")
	}
	r.path = originalPath
	if err = r.Apply(next); err != nil {
		t.Fatal(err)
	}
	retire := r.Snapshot()
	retire.Revision++
	retire.AuthIDs = []string{"b"}
	if err = r.Apply(retire); !errors.Is(err, ErrBusy) {
		t.Fatal("failed write lost outstanding A lease")
	}
	done()
	if err = r.Apply(retire); err != nil {
		t.Fatal(err)
	}
}

func TestRequestBoundaryCancellationReleasesOnlyOriginalAccount(t *testing.T) {
	server, routes, execution := heldRoutingServer(t)
	snapshot := routes.Snapshot()
	snapshot.Revision++
	snapshot.AuthIDs = []string{"held-a", "held-b"}
	if err := routes.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	first, cancelA := requestHeld(t, server, "ticket-a", true)
	defer cancelA()
	awaitAccount(t, execution, "held-a")
	snapshot.Revision++
	snapshot.RequestBoundary = true
	snapshot.Routes[0].AuthID = "held-b"
	if err := routes.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	second, cancelB := requestHeld(t, server, "ticket-a", true)
	defer cancelB()
	awaitAccount(t, execution, "held-b")
	cancelA()
	select {
	case result := <-first:
		if result.err == nil {
			t.Fatal("cancellation completed normally")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close response")
	}
	retireA := routes.Snapshot()
	retireA.Revision++
	retireA.AuthIDs = []string{"held-b"}
	deadline := time.Now().Add(time.Second)
	for {
		err := routes.Apply(retireA)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatal("cancel retained original account lease", err)
		}
		time.Sleep(time.Millisecond)
	}
	retireB := routes.Snapshot()
	retireB.Revision++
	retireB.Routes[0].AuthID = ""
	retireB.AuthIDs = nil
	if err := routes.Apply(retireB); !errors.Is(err, ErrBusy) {
		t.Fatal("cancelling A released unfinished B request", err)
	}
	close(execution.release)
	awaitResponse(t, second, "held-b")
}

func TestRequestBoundaryFailedARequestDoesNotFallBackToNewBRoute(t *testing.T) {
	server, routes, execution := heldRoutingServer(t)
	execution.fail = true
	snapshot := routes.Snapshot()
	snapshot.Revision++
	snapshot.AuthIDs = []string{"held-a", "held-b"}
	if err := routes.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	first, cancelFirst := requestHeld(t, server, "ticket-a", false)
	defer cancelFirst()
	awaitAccount(t, execution, "held-a")
	snapshot.Revision++
	snapshot.RequestBoundary = true
	snapshot.Routes[0].AuthID = "held-b"
	if err := routes.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	close(execution.release)
	select {
	case result := <-first:
		if result.err != nil || result.status == 200 {
			t.Fatalf("original failed request was replaced: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("failed original request did not finish")
	}
	if !reflect.DeepEqual(execution.recorded(), []string{"held-a"}) {
		t.Fatalf("SDK substituted new route on failure: %v", execution.recorded())
	}
	execution.fail = false
	second, cancelSecond := requestHeld(t, server, "ticket-a", false)
	defer cancelSecond()
	awaitAccount(t, execution, "held-b")
	awaitResponse(t, second, "held-b")
	if !reflect.DeepEqual(execution.recorded(), []string{"held-a", "held-b"}) {
		t.Fatalf("new client call did not use new route: %v", execution.recorded())
	}
}
