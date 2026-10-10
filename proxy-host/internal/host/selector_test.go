package host

import (
	"context"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"net/http"
	"testing"
)

func TestSelectorExactAccountAcrossMixedProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, dispatch := range []string{provider, "mixed"} {
			options := executor.Options{Headers: make(http.Header), Metadata: make(map[string]any)}
			options.Headers.Set(accountHeader, "selected")
			options.Headers.Set(providerHeader, provider)
			candidates := []*coreauth.Auth{{ID: "other", Provider: provider, Status: coreauth.StatusActive}, {ID: "selected", Provider: provider, Status: coreauth.StatusActive}}
			got, err := (exactSelector{}).Pick(context.Background(), dispatch, "model", options, candidates)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != "selected" {
				t.Fatalf("dispatch=%s selected=%s", dispatch, got.ID)
			}
			if options.Metadata[executor.PinnedAuthMetadataKey] != "selected" {
				t.Fatal("execution pin missing")
			}
		}
	}
}
func TestSelectorRefusesUnknownDisabledAndIncompatibleAccount(t *testing.T) {
	for _, tc := range []struct {
		name, dispatch, headerProvider, id string
		candidate                          *coreauth.Auth
	}{
		{"no-pin", "codex", "codex", "", &coreauth.Auth{ID: "alice", Provider: "codex"}},
		{"unknown", "codex", "codex", "missing", &coreauth.Auth{ID: "alice", Provider: "codex"}},
		{"wrong-dispatch", "claude", "codex", "alice", &coreauth.Auth{ID: "alice", Provider: "codex"}},
		{"wrong-provider", "mixed", "codex", "alice", &coreauth.Auth{ID: "alice", Provider: "claude"}},
		{"disabled-bool", "codex", "codex", "alice", &coreauth.Auth{ID: "alice", Provider: "codex", Disabled: true}},
		{"disabled-status", "codex", "codex", "alice", &coreauth.Auth{ID: "alice", Provider: "codex", Status: coreauth.StatusDisabled}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := executor.Options{Headers: make(http.Header), Metadata: make(map[string]any)}
			options.Headers.Set(accountHeader, tc.id)
			options.Headers.Set(providerHeader, tc.headerProvider)
			selected, err := (exactSelector{}).Pick(context.Background(), tc.dispatch, "model", options, []*coreauth.Auth{tc.candidate, {ID: "fallback", Provider: "codex", Status: coreauth.StatusActive}})
			if err == nil || selected != nil {
				t.Fatalf("selected=%+v err=%v", selected, err)
			}
		})
	}
}
func TestSelectorNilMetadataIsSafe(t *testing.T) {
	options := executor.Options{Headers: make(http.Header)}
	options.Headers.Set(accountHeader, "alice")
	options.Headers.Set(providerHeader, "codex")
	got, err := (exactSelector{}).Pick(context.Background(), "codex", "model", options, []*coreauth.Auth{{ID: "alice", Provider: "codex", Status: coreauth.StatusActive}})
	if err != nil || got == nil {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}
