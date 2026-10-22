package sandbox

import (
	"encoding/json"
	"testing"
	"time"
)

func decodePlanCoderProfile(t *testing.T, plan Plan) CoderSessionProfile {
	t.Helper()
	profile, err := DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	return profile
}

// A bring-your-own connection relaxes the mounted-volume check, carries the
// 20-minute startup budget, and may resolve its durable root to $HOME; the
// deployment Coder keeps the strict check and the deployment budget.
func TestCoderPlanStampsConnectionPolicy(t *testing.T) {
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	defaults := ProvisioningDefaults{
		Provider: ProviderCoder,
		Coder: CoderConfig{
			BaseURL: "https://coder.example.com", Owner: "owner", TemplateID: templateID,
			DurableRoot: "/persistent/ao", WorkerTokenTTL: time.Minute,
		},
	}
	deployment, err := defaults.SessionPlan("codex")
	if err != nil {
		t.Fatal(err)
	}
	profile := decodePlanCoderProfile(t, deployment)
	if profile.RequireMountedDurableRoot == nil || !*profile.RequireMountedDurableRoot {
		t.Fatalf("deployment profile requireMountedDurableRoot = %v, want true", profile.RequireMountedDurableRoot)
	}
	if profile.StartupTimeoutSeconds != 0 || profile.WorkspaceNamePrefix != "" {
		t.Fatalf("deployment profile = %+v, want no per-session budget or prefix", profile)
	}

	byo, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder,
		&CoderSessionOptions{WorkspaceNamePrefix: "ahmad"},
		&CoderDeploymentOverride{
			BaseURL: "https://coder.11x.example", Owner: "ahmad", TemplateID: templateID,
			DurableRoot: "~",
		})
	if err != nil {
		t.Fatal(err)
	}
	profile = decodePlanCoderProfile(t, byo)
	if profile.RequireMountedDurableRoot == nil || *profile.RequireMountedDurableRoot {
		t.Fatalf("BYO profile requireMountedDurableRoot = %v, want false", profile.RequireMountedDurableRoot)
	}
	if profile.StartupTimeoutSeconds != int(DefaultBYOCoderStartupTimeout/time.Second) {
		t.Fatalf("BYO startupTimeoutSeconds = %d, want %d", profile.StartupTimeoutSeconds, int(DefaultBYOCoderStartupTimeout/time.Second))
	}
	if profile.DurableRoot != CoderHomeDurableRoot || profile.WorkspaceNamePrefix != "ahmad" {
		t.Fatalf("BYO profile = %+v, want $HOME root and ahmad prefix", profile)
	}

	strict, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, &CoderDeploymentOverride{
		BaseURL: "https://coder.11x.example", Owner: "ahmad", TemplateID: templateID,
		DurableRoot: "/home/coder", RequireMountedDurableRoot: true, StartupTimeoutSeconds: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile = decodePlanCoderProfile(t, strict)
	if !*profile.RequireMountedDurableRoot || profile.StartupTimeoutSeconds != 900 {
		t.Fatalf("opted-in BYO profile = %+v, want strict mount and 900s budget", profile)
	}

	if _, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder,
		&CoderSessionOptions{WorkspaceNamePrefix: "Bad_Prefix"}, nil); err == nil {
		t.Fatal("invalid workspace name prefix was accepted")
	}
}

func TestCoderHomeDurableRootLayout(t *testing.T) {
	for _, root := range []string{"$HOME", "~", " ${HOME} "} {
		layout, err := NewCoderWorkspaceLayout(root)
		if err != nil {
			t.Fatalf("layout(%q): %v", root, err)
		}
		if layout.DurableRoot != CoderHomeDurableRoot || layout.Repository != "$HOME/repository" ||
			layout.Home != "$HOME/.ao/home" || layout.DurableIdentity != "$HOME/.ao/durable-session-id" {
			t.Fatalf("layout(%q) = %+v", root, layout)
		}
	}
	if _, err := NewCoderWorkspaceLayout("$HOME/sub"); err == nil {
		t.Fatal("a relative $HOME sub-path was accepted")
	}
}

// Rows stamped before requireMountedDurableRoot existed keep the strict check
// for the deployment Coder and drop it for a bring-your-own connection.
func TestRequiresMountedDurableRootLegacyDefault(t *testing.T) {
	var legacy CoderSessionProfile
	if err := json.Unmarshal([]byte(`{"durableRoot":"/home/coder"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if !legacy.RequiresMountedDurableRoot(false) || legacy.RequiresMountedDurableRoot(true) {
		t.Fatal("legacy profile mount policy is wrong")
	}
	strict := true
	legacy.RequireMountedDurableRoot = &strict
	if !legacy.RequiresMountedDurableRoot(true) {
		t.Fatal("an explicit strict flag was ignored for a BYO connection")
	}
}
