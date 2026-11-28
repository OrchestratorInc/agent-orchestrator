package domain

import "testing"

func readinessSnapshot(id string, installation AgentInstallationState, authentication AgentAuthenticationState) AgentReadinessSnapshot {
	return AgentReadinessSnapshot{
		ID:             id,
		Label:          id,
		Installation:   AgentInstallationObservation{State: installation},
		Authentication: AgentAuthenticationObservation{State: authentication},
	}
}

func TestPreferredProjectAgent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agents []AgentReadinessSnapshot
		want   AgentHarness
	}{
		{
			name: "confirmed agent beats a higher-priority unconfirmed one",
			agents: []AgentReadinessSnapshot{
				readinessSnapshot("claude-code", AgentInstallationUnknown, AgentAuthenticationUnknown),
				readinessSnapshot("codex", AgentInstallationInstalled, AgentAuthenticationAuthorized),
			},
			want: HarnessCodex,
		},
		{
			name: "an agent found installed beats an unchecked higher-priority one",
			agents: []AgentReadinessSnapshot{
				readinessSnapshot("claude-code", AgentInstallationUnknown, AgentAuthenticationUnknown),
				readinessSnapshot("codex", AgentInstallationInstalled, AgentAuthenticationUnknown),
			},
			want: HarnessCodex,
		},
		{
			name: "priority decides among confirmed agents; configured counts as signed in",
			agents: []AgentReadinessSnapshot{
				readinessSnapshot("cursor", AgentInstallationInstalled, AgentAuthenticationAuthorized),
				readinessSnapshot("claude-code", AgentInstallationInstalled, AgentAuthenticationConfigured),
			},
			want: HarnessClaudeCode,
		},
		{
			name: "missing and signed-out agents are left out",
			agents: []AgentReadinessSnapshot{
				readinessSnapshot("claude-code", AgentInstallationNotInstalled, AgentAuthenticationUnknown),
				readinessSnapshot("codex", AgentInstallationInstalled, AgentAuthenticationUnauthorized),
				readinessSnapshot("aider", AgentInstallationUnknown, AgentAuthenticationUnknown),
			},
			want: HarnessAider,
		},
		{
			name: "no launchable agent",
			agents: []AgentReadinessSnapshot{
				readinessSnapshot("claude-code", AgentInstallationNotInstalled, AgentAuthenticationUnknown),
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PreferredProjectAgent(tc.agents); got != tc.want {
				t.Fatalf("PreferredProjectAgent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPreferredInstalledProjectAgent(t *testing.T) {
	unchecked := []AgentReadinessSnapshot{
		readinessSnapshot("claude-code", AgentInstallationUnknown, AgentAuthenticationUnknown),
		readinessSnapshot("codex", AgentInstallationUnknown, AgentAuthenticationUnknown),
	}
	if got := PreferredInstalledProjectAgent(unchecked); got != "" {
		t.Fatalf("with no agent found installed, PreferredInstalledProjectAgent = %q, want none", got)
	}
	found := append(unchecked, readinessSnapshot("cursor", AgentInstallationInstalled, AgentAuthenticationUnknown))
	if got := PreferredInstalledProjectAgent(found); got != HarnessCursor {
		t.Fatalf("PreferredInstalledProjectAgent = %q, want the installed agent %q", got, HarnessCursor)
	}
	signedOut := []AgentReadinessSnapshot{readinessSnapshot("codex", AgentInstallationInstalled, AgentAuthenticationUnauthorized)}
	if got := PreferredInstalledProjectAgent(signedOut); got != "" {
		t.Fatalf("a signed-out agent can't launch, PreferredInstalledProjectAgent = %q", got)
	}
}

func TestPreferredProjectAgentNeedsChecks(t *testing.T) {
	unchecked := []AgentReadinessSnapshot{
		readinessSnapshot("claude-code", AgentInstallationUnknown, AgentAuthenticationUnknown),
		readinessSnapshot("codex", AgentInstallationInstalled, AgentAuthenticationAuthorized),
	}
	if !PreferredProjectAgentNeedsChecks(unchecked) {
		t.Fatal("an unconfirmed top-priority agent needs checks")
	}
	checked := []AgentReadinessSnapshot{
		readinessSnapshot("claude-code", AgentInstallationNotInstalled, AgentAuthenticationUnknown),
		readinessSnapshot("codex", AgentInstallationInstalled, AgentAuthenticationAuthorized),
	}
	if PreferredProjectAgentNeedsChecks(checked) {
		t.Fatal("a confirmed top usable agent needs no checks")
	}
}

func TestFillRoleAgentsKeepsChosenRoles(t *testing.T) {
	cfg := ProjectConfig{Worker: RoleOverride{Harness: HarnessClaudeCode}}
	if !cfg.FillRoleAgents(HarnessCodex) {
		t.Fatal("expected the empty orchestrator role to be filled")
	}
	if cfg.Worker.Harness != HarnessClaudeCode || cfg.Orchestrator.Harness != HarnessCodex {
		t.Fatalf("FillRoleAgents = worker %q orchestrator %q", cfg.Worker.Harness, cfg.Orchestrator.Harness)
	}
	if cfg.FillRoleAgents(HarnessCursor) {
		t.Fatal("a fully configured project must not change")
	}
	if (&ProjectConfig{}).FillRoleAgents("") {
		t.Fatal("an empty agent must not fill anything")
	}
}
