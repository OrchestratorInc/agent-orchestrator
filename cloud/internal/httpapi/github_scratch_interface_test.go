package httpapi

import (
	"encoding/json"
	"testing"
)

func TestScratchOrchestratorDefaultInterface(t *testing.T) {
	for value, want := range map[string]bool{"": true, "tui": true, "chat": true, "gui": false} {
		if got := (scratchOrchestratorRequest{Harness: "codex", DefaultInterfaceMode: value}).validDefaultInterface(); got != want {
			t.Errorf("validDefaultInterface(%q) = %v, want %v", value, got, want)
		}
	}
}

// A scratch capability is bound to its request's JSON. One without the new
// field must bind to the same bytes it did before the field existed.
func TestScratchCapabilityBindingUnchangedWithoutDefaultInterface(t *testing.T) {
	private := true
	request := createScratchCapabilityRequest{
		DisplayName: "Scratch", GitHubInstallationID: "42", Private: &private, TargetEnvironment: "staging",
		Orchestrator: scratchOrchestratorRequest{Harness: "codex", Prompt: "start"},
	}
	got, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(struct {
		DisplayName          string `json:"displayName"`
		GitHubInstallationID string `json:"githubInstallationId"`
		Private              *bool  `json:"private,omitempty"`
		TargetEnvironment    string `json:"targetEnvironment"`
		Orchestrator         struct {
			Harness string `json:"harness"`
			Prompt  string `json:"prompt,omitempty"`
		} `json:"orchestrator"`
	}{"Scratch", "42", &private, "staging", struct {
		Harness string `json:"harness"`
		Prompt  string `json:"prompt,omitempty"`
	}{"codex", "start"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(legacy) {
		t.Fatalf("binding = %s, want %s", got, legacy)
	}
}
