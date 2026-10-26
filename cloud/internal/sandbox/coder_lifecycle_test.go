package sandbox

import (
	"encoding/json"
	"testing"
)

// The working directory and secret-file protection are stamped on the
// session profile; the directory only with the workspace worker user, the
// protection with either.
func TestCoderPlanStampsLifecycleSettings(t *testing.T) {
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	defaults := ProvisioningDefaults{Provider: ProviderCoder}
	override := CoderDeploymentOverride{
		BaseURL: "https://coder.acme.example", Owner: "dev", TemplateID: templateID, DurableRoot: "~",
		WorkerUser: "workspace", WorkingDirectory: "~/work/{repo}",
		ProtectSecretFiles: true,
	}
	plan, err := defaults.SessionPlanForProviderWithCoder("claude-code", ProviderCoder, nil, &override)
	if err != nil {
		t.Fatal(err)
	}
	profile := decodePlanCoderProfile(t, plan)
	if profile.WorkingDirectory != "~/work/{repo}" || !profile.ProtectsSecretFiles() {
		t.Fatalf("profile = %+v", profile)
	}

	aoWorker := override
	aoWorker.WorkerUser = ""
	plan, err = defaults.SessionPlanForProviderWithCoder("claude-code", ProviderCoder, nil, &aoWorker)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Coder map[string]json.RawMessage `json:"coder"`
	}
	if err := json.Unmarshal(plan.ResourceProfile, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw.Coder["workingDirectory"]; present {
		t.Fatalf("ao-worker profile carries workingDirectory: %s", plan.ResourceProfile)
	}
	if profile := decodePlanCoderProfile(t, plan); !profile.ProtectsSecretFiles() {
		t.Fatalf("ao-worker profile lost the protection: %+v", profile)
	}

	// The deployment Coder (no override) stamps none of it.
	deployment := ProvisioningDefaults{Provider: ProviderCoder, Coder: CoderConfig{
		BaseURL: "https://coder.ao.example", Owner: "ao", TemplateID: templateID, DurableRoot: "/home/coder",
		WorkerTokenTTL: DefaultWorkerTokenTTL,
	}}
	plan, err = deployment.SessionPlanForProviderWithCoder("claude-code", ProviderCoder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile := decodePlanCoderProfile(t, plan); profile.ProtectSecretFiles != nil || profile.WorkspaceNamePrefix != "" {
		t.Fatalf("deployment profile = %+v", profile)
	}
}

// A bring-your-own session without a project prefix is named after its
// project; a project prefix still wins.
func TestCoderPlanDefaultsWorkspaceNamePrefix(t *testing.T) {
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	defaults := ProvisioningDefaults{Provider: ProviderCoder}
	override := &CoderDeploymentOverride{
		BaseURL: "https://coder.acme.example", Owner: "dev", TemplateID: templateID, DurableRoot: "~",
		DefaultWorkspaceNamePrefix: "alice",
	}
	plan, err := defaults.SessionPlanForProviderWithCoder("claude-code", ProviderCoder, nil, override)
	if err != nil {
		t.Fatal(err)
	}
	if prefix := decodePlanCoderProfile(t, plan).WorkspaceNamePrefix; prefix != "alice" {
		t.Fatalf("prefix = %q, want the project default", prefix)
	}
	plan, err = defaults.SessionPlanForProviderWithCoder("claude-code", ProviderCoder,
		&CoderSessionOptions{WorkspaceNamePrefix: "ahmad"}, override)
	if err != nil {
		t.Fatal(err)
	}
	if prefix := decodePlanCoderProfile(t, plan).WorkspaceNamePrefix; prefix != "ahmad" {
		t.Fatalf("prefix = %q, want the project's own", prefix)
	}
}
