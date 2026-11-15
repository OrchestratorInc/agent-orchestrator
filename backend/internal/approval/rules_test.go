package approval

import (
	"testing"

	rules "github.com/aoagents/agent-orchestrator/backend/pkg/approval"
)

// The local gate and AO Cloud must judge every action the same way.
func TestRulesAreThePublicRules(t *testing.T) {
	target := rules.Target{Action: "click", Role: "button", Name: "Place order", PageURL: "https://shop.example/checkout"}
	if got, want := Classify(target, ModeRisky), rules.Classify(target, rules.ModeRisky); got != want || !got.NeedsApproval {
		t.Fatalf("Classify = %+v, public = %+v", got, want)
	}
	if KindSavedLogin != "credential-use" || CategoryCredential != "credential" {
		t.Fatalf("saved-login kind/category changed: %q %q", KindSavedLogin, CategoryCredential)
	}
}
