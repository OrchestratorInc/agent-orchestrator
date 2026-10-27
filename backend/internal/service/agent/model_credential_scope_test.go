package agent

import "testing"

func TestCredentialTypeFromScope(t *testing.T) {
	for _, tc := range []struct {
		scope string
		want  string
		ok    bool
	}{
		{scope: "@cred:anthropic_api_key", want: "anthropic_api_key", ok: true},
		{scope: "@cred:openrouter_api_key", want: "openrouter_api_key", ok: true},
		{scope: "", ok: false},
		{scope: "@cred:", ok: false},
		{scope: "my-project", ok: false},
		{scope: "agent-orchestrator", ok: false},
	} {
		got, ok := credentialTypeFromScope(tc.scope)
		if ok != tc.ok || got != tc.want {
			t.Errorf("credentialTypeFromScope(%q) = (%q,%v), want (%q,%v)", tc.scope, got, ok, tc.want, tc.ok)
		}
	}
}

func TestModelCatalogScopeKeepsOnlyCredentialScopes(t *testing.T) {
	// A credential scope has no backing project, so it survives verbatim; every
	// other scope, including a project ID, is the single device catalog.
	for _, tc := range []struct{ scope, want string }{
		{scope: "@cred:anthropic_api_key", want: "@cred:anthropic_api_key"},
		{scope: "agent-orchestrator", want: ""},
		{scope: "", want: ""},
		{scope: "@cred:", want: ""},
	} {
		if got := modelCatalogScope(tc.scope); got != tc.want {
			t.Errorf("modelCatalogScope(%q) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}

func TestModelDiscoveryRequestSetsCredentialType(t *testing.T) {
	// A credential scope carries the credential type onto the request for the
	// adapter to honor.
	s := &Service{}
	request, err := s.modelDiscoveryRequest("opencode", "@cred:openai_api_key", "/bin/opencode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if request.CredentialType != "openai_api_key" {
		t.Fatalf("CredentialType = %q, want openai_api_key", request.CredentialType)
	}
	if request.WorkingDir != "" {
		t.Fatalf("WorkingDir = %q, want empty for a credential scope", request.WorkingDir)
	}
}
