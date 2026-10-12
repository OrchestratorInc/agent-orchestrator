package sandbox

import (
	"encoding/json"
	"testing"
	"time"
)

// Rich parameters merge in a fixed order at workspace create: the
// connection's parameters, then the project's, then AO's size/startup
// mapping. Project parameters, like size/startup, need a picked template.
func TestCoderPlanParameterMergeOrder(t *testing.T) {
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	defaults := ProvisioningDefaults{Provider: ProviderCoder}
	override := &CoderDeploymentOverride{
		BaseURL: "https://coder.acme.example", Owner: "dev", DurableRoot: "~",
		Parameters: map[string]string{"region": "org-eu", "size": "org-size", "team": "org-team"},
	}
	plan, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, &CoderSessionOptions{
		TemplateID: templateID, Size: "large",
		Parameters: map[string]string{"region": "project-us", "size": "project-size", "repos": `["a","b"]`},
	}, override)
	if err != nil {
		t.Fatal(err)
	}
	got := decodePlanCoderProfile(t, plan).Parameters
	want := map[string]string{"region": "project-us", "size": "large", "team": "org-team", "repos": `["a","b"]`}
	if len(got) != len(want) {
		t.Fatalf("parameters = %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("parameters[%s] = %q, want %q (all: %v)", name, got[name], value, got)
		}
	}

	override.TemplateID = templateID
	unpicked, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder,
		&CoderSessionOptions{Parameters: map[string]string{"region": "project-us"}}, override)
	if err != nil {
		t.Fatal(err)
	}
	if region := decodePlanCoderProfile(t, unpicked).Parameters["region"]; region != "org-eu" {
		t.Fatalf("region without a picked template = %q, want the connection's org-eu", region)
	}
}

// The connection's dev-environment options are stamped on the session
// profile, a ready check stretches the startup budget past its own timeout,
// and the default connection adds none of the new keys.
func TestCoderPlanStampsDevEnvironment(t *testing.T) {
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	defaults := ProvisioningDefaults{Provider: ProviderCoder}
	base := CoderDeploymentOverride{
		BaseURL: "https://coder.acme.example", Owner: "dev", TemplateID: templateID, DurableRoot: "~",
	}

	plain, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, &base)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Coder map[string]json.RawMessage `json:"coder"`
	}
	if err := json.Unmarshal(plain.ResourceProfile, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workerUser", "readyProbe", "readyProbeTimeoutSeconds", "reuseExistingCheckout"} {
		if _, present := raw.Coder[key]; present {
			t.Fatalf("default profile carries %s: %s", key, plain.ResourceProfile)
		}
	}
	profile := decodePlanCoderProfile(t, plain)
	if profile.RunsAsWorkspaceUser() || profile.ReuseExistingCheckout || profile.ReadyProbe != "" {
		t.Fatalf("default profile = %+v", profile)
	}

	devEnv := base
	devEnv.WorkerUser = "workspace"
	devEnv.ReuseExistingCheckout = true
	devEnv.ReadyProbe = "grep -q '^Ready' ~/.bootstrap-status"
	devEnv.ReadyProbeTimeoutSeconds = 1800
	plan, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, &devEnv)
	if err != nil {
		t.Fatal(err)
	}
	profile = decodePlanCoderProfile(t, plan)
	if !profile.RunsAsWorkspaceUser() || !profile.ReuseExistingCheckout ||
		profile.ReadyProbe != devEnv.ReadyProbe || profile.ReadyProbeTimeoutSeconds != 1800 {
		t.Fatalf("dev-environment profile = %+v", profile)
	}
	if want := 1800 + int(readyProbeStartupMargin/time.Second); profile.StartupTimeoutSeconds != want {
		t.Fatalf("startupTimeoutSeconds = %d, want %d (probe timeout + margin)", profile.StartupTimeoutSeconds, want)
	}

	aoWorkerReuse := base
	aoWorkerReuse.ReuseExistingCheckout = true
	plan, err = defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, &aoWorkerReuse)
	if err != nil {
		t.Fatal(err)
	}
	if profile := decodePlanCoderProfile(t, plan); profile.ReuseExistingCheckout {
		t.Fatal("checkout reuse stamped for the ao-worker user")
	}
}

func TestDecodeCoderSessionProfileDevEnvironment(t *testing.T) {
	decode := func(extra string) (CoderSessionProfile, error) {
		return DecodeCoderSessionProfile(json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"o","templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2","durableRoot":"$HOME"` + extra + `}}`))
	}
	profile, err := decode(`,"workerUser":"ao-worker","reuseExistingCheckout":true`)
	if err != nil {
		t.Fatal(err)
	}
	if profile.RunsAsWorkspaceUser() || profile.ReuseExistingCheckout {
		t.Fatalf("ao-worker profile = %+v", profile)
	}
	if _, err := decode(`,"workerUser":"root"`); err == nil {
		t.Fatal("unsupported worker user accepted")
	}
	if _, err := decode(`,"readyProbe":"true","readyProbeTimeoutSeconds":-1`); err == nil {
		t.Fatal("negative ready check timeout accepted")
	}
}
