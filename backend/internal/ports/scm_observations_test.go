package ports_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A PR counts as the account's own only when a human account's login matches
// its author; a bot or login-less account authors nothing.
func TestSCMIdentityAuthored(t *testing.T) {
	for _, tc := range []struct {
		id     ports.SCMIdentity
		author string
		want   bool
	}{
		{ports.SCMIdentity{Login: "alice", Human: true}, "alice", true},
		{ports.SCMIdentity{Login: " Alice ", Human: true}, "aLICE ", true},
		{ports.SCMIdentity{Login: "alice", Human: true}, "bob", false},
		{ports.SCMIdentity{Login: "alice", Human: true}, "", false},
		{ports.SCMIdentity{Login: "alice", Human: false}, "alice", false},
		{ports.SCMIdentity{Login: " ", Human: true}, "", false},
	} {
		if got := tc.id.Authored(tc.author); got != tc.want {
			t.Errorf("%+v.Authored(%q) = %v, want %v", tc.id, tc.author, got, tc.want)
		}
	}
}
