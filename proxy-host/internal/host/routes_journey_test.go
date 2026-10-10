package host

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"testing"
)

type expectedRouteLease struct {
	session  string
	selected Route
	release  func()
}

func verifyRouteJourney(t *testing.T, routes *Routes, expected Snapshot, tickets map[string]string, leases []expectedRouteLease) {
	t.Helper()
	if !reflect.DeepEqual(routes.Snapshot(), expected) {
		t.Fatal("effective routing diverged from the accepted operation sequence")
	}
	for ticket, session := range tickets {
		var wanted Route
		found := false
		for _, route := range expected.Routes {
			if route.SessionID == session {
				wanted, found = route, true
				break
			}
		}
		selected, done, err := routes.Acquire(ticket)
		if !found || wanted.AuthID == "" {
			if err == nil || done != nil {
				t.Fatal("removed or waiting route still admits model requests")
			}
			continue
		}
		if err != nil || done == nil || selected != wanted {
			t.Fatalf("request admitted a different session account: %v", err)
		}
		done()
		done()
	}
	for _, lease := range leases {
		for _, route := range expected.Routes {
			if route.SessionID == lease.session && route != lease.selected {
				t.Fatal("account changed while an accepted model request was unfinished")
			}
		}
	}
	if _, done, err := routes.Acquire("unregistered-ticket"); err == nil || done != nil {
		t.Fatal("an unregistered request acquired an arbitrary account")
	}
	data, err := os.ReadFile(routes.path)
	if err != nil {
		t.Fatal(err)
	}
	for ticket := range tickets {
		if containsBytes(data, []byte(ticket)) {
			t.Fatal("raw process ticket was stored in the helper routing file")
		}
	}
}

func containsBytes(data, fragment []byte) bool {
	for i := 0; i+len(fragment) <= len(data); i++ {
		if string(data[i:i+len(fragment)]) == string(fragment) {
			return true
		}
	}
	return false
}

func TestRouteJourneysNeverMoveActiveRequestsAndPersistOnlyAcceptedChanges(t *testing.T) {
	for _, seed := range []int64{3, 17, 51, 99} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			routes := testRoutes(t)
			expected := Snapshot{Revision: 1, Routes: []Route{}}
			tickets := make(map[string]string)
			for n := 0; n < 6; n++ {
				ticket := fmt.Sprintf("journey-private-ticket-%d", n)
				session := fmt.Sprintf("journey-session-%d", n)
				provider := []string{"codex", "claude"}[n%2]
				expected.Routes = append(expected.Routes, testRoute(session, ticket, provider, provider+"-account-a"))
				tickets[ticket] = session
			}
			if err := routes.Apply(expected); err != nil {
				t.Fatal(err)
			}
			random := rand.New(rand.NewSource(seed))
			leases := []expectedRouteLease{}
			t.Cleanup(func() {
				for _, lease := range leases {
					lease.release()
				}
			})
			orderedTickets := make([]string, 0, len(tickets))
			for ticket := range tickets {
				orderedTickets = append(orderedTickets, ticket)
			}
			sort.Strings(orderedTickets)
			for step := 0; step < 80; step++ {
				switch random.Intn(6) {
				case 0:
					ticket := orderedTickets[random.Intn(len(orderedTickets))]
					selected, release, err := routes.Acquire(ticket)
					if err == nil {
						leases = append(leases, expectedRouteLease{session: selected.SessionID, selected: selected, release: release})
					} else if release != nil {
						t.Fatal("failed request retained an admission lease")
					}
				case 1:
					if len(leases) > 0 {
						index := random.Intn(len(leases))
						leases[index].release()
						leases[index].release()
						leases = append(leases[:index], leases[index+1:]...)
					}
				case 2, 3:
					if len(expected.Routes) == 0 {
						continue
					}
					next := Snapshot{Revision: expected.Revision + 1, Routes: append([]Route(nil), expected.Routes...)}
					index := random.Intn(len(next.Routes))
					old := next.Routes[index]
					newAccount := []string{old.Provider + "-account-a", old.Provider + "-account-b", ""}[random.Intn(3)]
					next.Routes[index].AuthID = newAccount
					busy := false
					for _, lease := range leases {
						busy = busy || (lease.session == old.SessionID && newAccount != old.AuthID)
					}
					dataBefore, err := os.ReadFile(routes.path)
					if err != nil {
						t.Fatal(err)
					}
					err = routes.Apply(next)
					if busy {
						if !errors.Is(err, ErrBusy) {
							t.Fatalf("unfinished request did not block account mutation: %v", err)
						}
						dataAfter, err := os.ReadFile(routes.path)
						if err != nil || string(dataBefore) != string(dataAfter) {
							t.Fatal("busy refusal wrote a deferred routing snapshot")
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						expected = next
					}
				case 4:
					if err := routes.Apply(expected); err != nil {
						t.Fatalf("lost acknowledgement replay refused: %v", err)
					}
					skipped := Snapshot{Revision: expected.Revision + 2, Routes: append([]Route(nil), expected.Routes...)}
					if err := routes.Apply(skipped); err != nil {
						t.Fatalf("newer revision after a gap was refused: %v", err)
					}
					expected = skipped
				case 5:
					if len(leases) == 0 {
						restored, err := OpenRoutes(routes.path)
						if err != nil {
							t.Fatal(err)
						}
						routes = restored
					}
				}
				verifyRouteJourney(t, routes, expected, tickets, leases)
			}
			for _, lease := range leases {
				lease.release()
			}
			leases = nil
			next := Snapshot{Revision: expected.Revision + 1, Routes: []Route{}}
			if err := routes.Apply(next); err != nil {
				t.Fatal(err)
			}
			verifyRouteJourney(t, routes, next, tickets, leases)
		})
	}
}

func TestRouteBatchChangingOneBusyMemberDoesNotPublishItsIdleMembers(t *testing.T) {
	for _, busyIndex := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("busy-%d", busyIndex), func(t *testing.T) {
			routes := testRoutes(t)
			initial := Snapshot{Revision: 1, Routes: []Route{testRoute("one", "batch-one", "codex", "alice"), testRoute("two", "batch-two", "codex", "alice"), testRoute("three", "batch-three", "claude", "clara")}}
			if err := routes.Apply(initial); err != nil {
				t.Fatal(err)
			}
			ticket := []string{"batch-one", "batch-two", "batch-three"}[busyIndex]
			selected, release, err := routes.Acquire(ticket)
			if err != nil {
				t.Fatal(err)
			}
			next := Snapshot{Revision: 2, Routes: append([]Route(nil), initial.Routes...)}
			for i := range next.Routes {
				next.Routes[i].AuthID = ""
			}
			if err := routes.Apply(next); !errors.Is(err, ErrBusy) {
				t.Fatalf("batch sign-out error=%v", err)
			}
			if !reflect.DeepEqual(initial, routes.Snapshot()) || selected != initial.Routes[busyIndex] {
				t.Fatal("refused batch partially signed out idle or active members")
			}
			for i, ticket := range []string{"batch-one", "batch-two", "batch-three"} {
				route, done, err := routes.Acquire(ticket)
				if err != nil || route != initial.Routes[i] {
					t.Fatal("refused batch changed another session's next request")
				}
				done()
			}
			release()
			if err := routes.Apply(next); err != nil {
				t.Fatal(err)
			}
			for _, ticket := range []string{"batch-one", "batch-two", "batch-three"} {
				if _, done, err := routes.Acquire(ticket); err == nil || done != nil {
					t.Fatal("admitted final sign-out retained an available account")
				}
			}
		})
	}
}
