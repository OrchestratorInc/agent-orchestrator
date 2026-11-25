package chat

import "testing"

// A finished handoff is kept for a while only so it is not repeated; it is no
// work for the usage-limit loop, which may rest. A running or retrying one is.
func TestAccountHandoffIdleIgnoresFinishedHandoffs(t *testing.T) {
	svc := New(Options{})
	svc.handoffs = map[string]*accountHandoff{"t1": {done: true}}
	if !svc.AccountHandoffIdle() {
		t.Fatal("a finished handoff kept the loop busy")
	}
	svc.handoffs["t2"] = &accountHandoff{}
	if svc.AccountHandoffIdle() {
		t.Fatal("a handoff that is still in progress should keep the loop busy")
	}
}
