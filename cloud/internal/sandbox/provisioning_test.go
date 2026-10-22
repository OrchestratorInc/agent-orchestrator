package sandbox

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Rewriting a plan onto a project snapshot keeps the harness snapshot it
// replaced as baseSnapshot, the fallback if the project snapshot is collected.
func TestWithFreestyleSnapshotKeepsTheBaseSnapshot(t *testing.T) {
	plan := Plan{
		Provider:         ProviderFreestyle,
		ResourceProfile:  json.RawMessage(`{"freestyle":{"snapshot":"snap-codex"}}`),
		BootstrapContext: json.RawMessage(`{"freestyle":{"snapshot":"snap-codex"}}`),
	}
	rewritten, err := WithFreestyleSnapshot(plan, "snap-project")
	if err != nil {
		t.Fatal(err)
	}
	again, err := WithFreestyleSnapshot(rewritten, "snap-project-2")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{again.ResourceProfile, again.BootstrapContext} {
		var document struct {
			Freestyle struct {
				Snapshot     string `json:"snapshot"`
				BaseSnapshot string `json:"baseSnapshot"`
			} `json:"freestyle"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if document.Freestyle.Snapshot != "snap-project-2" || document.Freestyle.BaseSnapshot != "snap-codex" {
			t.Fatalf("freestyle = %+v, want snap-project-2 on base snap-codex", document.Freestyle)
		}
	}
}

func TestNewCoderWorkspaceLayout(t *testing.T) {
	t.Parallel()
	layout, err := NewCoderWorkspaceLayout("/home/coder")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"repository":       "/home/coder/repository",
		"worker data":      "/home/coder/.ao/worker",
		"home":             "/home/coder/.ao/home",
		"Claude config":    "/home/coder/.ao/home/.claude",
		"Codex home":       "/home/coder/.ao/home/.codex",
		"durable identity": "/home/coder/.ao/durable-session-id",
	}
	got := map[string]string{
		"repository":       layout.Repository,
		"worker data":      layout.WorkerData,
		"home":             layout.Home,
		"Claude config":    layout.ClaudeConfig,
		"Codex home":       layout.CodexHome,
		"durable identity": layout.DurableIdentity,
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Errorf("%s = %q, want %q", name, got[name], expected)
		}
	}
}

func TestCoderConfigRejectsUnsafeDurableRoot(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"", "/", "relative", "/home/coder/../workspace", "/home/coder\nother"} {
		root := root
		t.Run(root, func(t *testing.T) {
			t.Parallel()
			config := CoderConfig{
				BaseURL: "https://coder.example.com", Owner: "owner", TemplateID: "template",
				DurableRoot: root, WorkerTokenTTL: time.Minute,
			}
			if err := config.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestCoderSessionPlanPersistsDurableRoot(t *testing.T) {
	t.Parallel()
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	plan, err := (ProvisioningDefaults{
		Provider: ProviderCoder,
		Coder: CoderConfig{
			BaseURL: "https://coder.example.com", Owner: "owner", TemplateID: templateID,
			AgentName: "dev", Parameters: map[string]string{" region ": "us-west-2"},
			DurableRoot: "/persistent/ao", WorkerTokenTTL: time.Minute,
		},
	}).SessionPlan("codex")
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]json.RawMessage{
		"resource profile":  plan.ResourceProfile,
		"bootstrap context": plan.BootstrapContext,
	} {
		var decoded struct {
			Coder struct {
				BaseURL     string `json:"baseUrl"`
				DurableRoot string `json:"durableRoot"`
			} `json:"coder"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if decoded.Coder.DurableRoot != "/persistent/ao" {
			t.Errorf("%s durable root = %q", name, decoded.Coder.DurableRoot)
		}
		if name == "resource profile" && decoded.Coder.BaseURL != "https://coder.example.com" {
			t.Errorf("%s base URL = %q", name, decoded.Coder.BaseURL)
		}
	}
	profile, err := DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != "https://coder.example.com" ||
		profile.Owner != "owner" || profile.TemplateID != templateID ||
		profile.AgentName != "dev" || profile.Parameters["region"] != "us-west-2" ||
		profile.DurableRoot != "/persistent/ao" {
		t.Fatalf("unexpected durable profile: %+v", profile)
	}
}

func TestSessionPlanForProviderOverridesDefault(t *testing.T) {
	t.Parallel()
	const templateID = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
	// A control plane configured with more than one provider carries every
	// provider's config; the default here is Docker.
	defaults := ProvisioningDefaults{
		Provider: ProviderDocker,
		Docker: DockerConfig{
			Host:           "unix:///var/run/docker.sock",
			WorkerImage:    "ao-cloud-worker:local",
			Network:        "ao-cloud-local",
			Namespace:      "ao-cloud-local",
			WorkerTokenTTL: time.Minute,
		},
		Coder: CoderConfig{
			BaseURL: "https://coder.example.com", Owner: "owner", TemplateID: templateID,
			AgentName: "dev", DurableRoot: "/persistent/ao", WorkerTokenTTL: time.Minute,
		},
	}
	// An explicit override selects Coder even though the default is Docker.
	coderPlan, err := defaults.SessionPlanForProvider("codex", ProviderCoder)
	if err != nil {
		t.Fatal(err)
	}
	if coderPlan.Provider != ProviderCoder {
		t.Fatalf("override provider = %q, want %q", coderPlan.Provider, ProviderCoder)
	}
	// An empty override falls back to the deployment default.
	defaultPlan, err := defaults.SessionPlanForProvider("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if defaultPlan.Provider != ProviderDocker {
		t.Fatalf("fallback provider = %q, want %q", defaultPlan.Provider, ProviderDocker)
	}
	// SessionPlan stays equivalent to an empty override (the default).
	plainPlan, err := defaults.SessionPlan("codex")
	if err != nil {
		t.Fatal(err)
	}
	if plainPlan.Provider != ProviderDocker {
		t.Fatalf("SessionPlan provider = %q, want %q", plainPlan.Provider, ProviderDocker)
	}
}

func TestSessionPlanForProviderWithCoderOptions(t *testing.T) {
	t.Parallel()
	const (
		defaultTemplate = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
		chosenTemplate  = "2a2e262c-b31c-4202-946d-a19ad45d1fd3"
	)
	defaults := ProvisioningDefaults{
		Provider: ProviderCoder,
		Coder: CoderConfig{
			BaseURL: "https://coder.example.com", Owner: "owner", TemplateID: defaultTemplate,
			AgentName: "dev", DurableRoot: "/persistent/ao", WorkerTokenTTL: time.Minute,
		},
	}

	// nil options => default template, no size/startup params (unchanged behavior).
	plan, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.TemplateID != defaultTemplate {
		t.Fatalf("default template = %q, want %q", profile.TemplateID, defaultTemplate)
	}
	if _, ok := profile.Parameters["size"]; ok {
		t.Fatalf("default plan should carry no size parameter: %+v", profile.Parameters)
	}

	// A chosen template with size + startup overrides the template and layers the
	// rich parameters on.
	plan, err = defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, &CoderSessionOptions{
		TemplateID: chosenTemplate, Size: "large", StartupScript: "make dev",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.TemplateID != chosenTemplate {
		t.Fatalf("chosen template = %q, want %q", profile.TemplateID, chosenTemplate)
	}
	if profile.Parameters["size"] != "large" || profile.Parameters["startup_script"] != "make dev" {
		t.Fatalf("chosen plan parameters = %+v", profile.Parameters)
	}

	// Size without a chosen template is ignored: the default template does not
	// declare it, so we must not send it (Coder would reject the build).
	plan, err = defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, &CoderSessionOptions{Size: "large"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.TemplateID != defaultTemplate {
		t.Fatalf("size-only options must keep the default template, got %q", profile.TemplateID)
	}
	if _, ok := profile.Parameters["size"]; ok {
		t.Fatalf("size without a chosen template must be ignored: %+v", profile.Parameters)
	}
}

func TestDecodeCoderSessionProfileRejectsIncompleteContract(t *testing.T) {
	t.Parallel()
	profiles := []json.RawMessage{
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`{"coder":{"owner":"owner","templateId":"template","durableRoot":"/mnt/ao"}}`),
		json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","templateId":"template","durableRoot":"/mnt/ao"}}`),
		json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"owner","durableRoot":"/mnt/ao"}}`),
		json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"owner","templateId":"template"}}`),
		json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com/path","owner":"owner","templateId":"template","durableRoot":"/mnt/ao"}}`),
		json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"owner","templateId":"template","durableRoot":"/mnt/ao","parameters":{"x":"one"," x ":"two"}}}`),
	}
	for index, raw := range profiles {
		if _, err := DecodeCoderSessionProfile(raw); err == nil {
			t.Errorf("profile %d unexpectedly decoded: %s", index, raw)
		}
	}
}

// A per-organization override replaces the deployment-default Coder connection's
// non-secret fields in the stamped session profile, while a nil override leaves
// the deployment default untouched.
func TestSessionPlanForProviderWithCoderOverride(t *testing.T) {
	t.Parallel()
	const (
		deployTemplate = "2a2e262c-b31c-4202-946d-a19ad45d1fd2"
		orgTemplate    = "3b3f373d-c42d-5313-a57e-b2abe56e2fe3"
	)
	defaults := ProvisioningDefaults{
		Provider: ProviderCoder,
		Release:  "test",
		Coder: CoderConfig{
			BaseURL: "https://coder.example.com", Owner: "deploy-owner", TemplateID: deployTemplate,
			AgentName: "deploy-dev", DurableRoot: "/persistent/ao", WorkerTokenTTL: time.Minute,
		},
	}

	plan, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, &CoderDeploymentOverride{
		BaseURL: "https://org-coder.example.com", Owner: "org-owner", TemplateID: orgTemplate,
		AgentName: "org-dev", Parameters: map[string]string{"region": "eu"}, DurableRoot: "/srv/org",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != "https://org-coder.example.com" || profile.Owner != "org-owner" ||
		profile.TemplateID != orgTemplate || profile.AgentName != "org-dev" ||
		profile.DurableRoot != "/srv/org" || profile.Parameters["region"] != "eu" {
		t.Fatalf("override not applied: %+v", profile)
	}

	// A nil override keeps the deployment default.
	plan, err = defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != "https://coder.example.com" || profile.Owner != "deploy-owner" ||
		profile.TemplateID != deployTemplate {
		t.Fatalf("nil override disturbed the deployment default: %+v", profile)
	}
}

// A bring-your-own-Coder org carries no default template (it is chosen per
// project). The per-project pick supplies the template; with neither the org nor
// the project supplying one, the plan fails with ErrCoderTemplateRequired so the
// edge can tell the user to choose a template.
func TestSessionPlanForProviderWithCoderTemplatelessOverride(t *testing.T) {
	t.Parallel()
	const projectTemplate = "3b3f373d-c42d-5313-a57e-b2abe56e2fe3"
	// A bring-your-own-only deployment may leave the deployment default unset.
	defaults := ProvisioningDefaults{Provider: ProviderCoder, Release: "test"}
	templateless := &CoderDeploymentOverride{
		BaseURL: "https://org-coder.example.com", Owner: "org-owner",
		DurableRoot: "/srv/org",
	}

	// Project supplies the template.
	plan, err := defaults.SessionPlanForProviderWithCoder(
		"codex", ProviderCoder, &CoderSessionOptions{TemplateID: projectTemplate}, templateless,
	)
	if err != nil {
		t.Fatalf("project-supplied template: %v", err)
	}
	profile, err := DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.TemplateID != projectTemplate || profile.Owner != "org-owner" {
		t.Fatalf("project template not applied over templateless org: %+v", profile)
	}

	// No template anywhere => a clear, user-fixable error.
	if _, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, nil, templateless); !errors.Is(err, ErrCoderTemplateRequired) {
		t.Fatalf("templateless session error = %v, want ErrCoderTemplateRequired", err)
	}
	// An options object that carries only size (no template) still resolves nothing.
	if _, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, &CoderSessionOptions{Size: "large"}, templateless); !errors.Is(err, ErrCoderTemplateRequired) {
		t.Fatalf("size-only templateless session error = %v, want ErrCoderTemplateRequired", err)
	}
}

// The deployment's own Coder offers only its allowed templates: a test or
// bring-your-own template on the shared Coder is refused up front instead of
// booting a workspace the deployment cannot use. The default template is
// always allowed, and bring-your-own connections are not limited.
func TestSessionPlanForProviderWithCoderAllowedTemplates(t *testing.T) {
	t.Parallel()
	const (
		defaultTemplate = "d85825e0-6bd4-4fe4-853d-1b48891b8066"
		devKit          = "f81b3138-f642-4746-ba51-11ef8ec718b0"
		testTemplate    = "d7f2324d-51ea-4429-bfdc-c4a544535095"
	)
	defaults := ProvisioningDefaults{Provider: ProviderCoder, Release: "test", Coder: CoderConfig{
		BaseURL: "https://coder.example.com", Owner: "aoadmin", TemplateID: defaultTemplate,
		AgentName: "main", DurableRoot: "/home/coder", WorkerTokenTTL: DefaultWorkerTokenTTL,
		AllowedTemplateIDs: []string{devKit},
	}}
	for name, options := range map[string]*CoderSessionOptions{
		"deployment default": nil,
		"default picked":     {TemplateID: defaultTemplate},
		"allowed template":   {TemplateID: devKit},
	} {
		if _, err := defaults.SessionPlanForProviderWithCoder("codex", ProviderCoder, options, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := defaults.SessionPlanForProviderWithCoder(
		"codex", ProviderCoder, &CoderSessionOptions{TemplateID: testTemplate}, nil,
	); !errors.Is(err, ErrCoderTemplateUnavailable) {
		t.Fatalf("test template error = %v, want ErrCoderTemplateUnavailable", err)
	}
	byo := &CoderDeploymentOverride{BaseURL: "https://org-coder.example.com", Owner: "dev", DurableRoot: "$HOME"}
	if _, err := defaults.SessionPlanForProviderWithCoder(
		"codex", ProviderCoder, &CoderSessionOptions{TemplateID: testTemplate}, byo,
	); err != nil {
		t.Fatalf("bring-your-own template: %v", err)
	}
	unlimited := defaults
	unlimited.Coder.AllowedTemplateIDs = nil
	if _, err := unlimited.SessionPlanForProviderWithCoder(
		"codex", ProviderCoder, &CoderSessionOptions{TemplateID: testTemplate}, nil,
	); err != nil {
		t.Fatalf("no allowed list: %v", err)
	}
}

// The workspace name is chosen once, when the sandbox row is written, from the
// profile's prefix and the session's kind, display name and id.
func TestStampCoderWorkspaceName(t *testing.T) {
	t.Parallel()
	const sessionID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	issuedAt := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	raw := json.RawMessage(`{"provider":"coder","sessionId":"` + sessionID + `","coder":{"baseUrl":"https://coder.example.com","owner":"owner","templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2","durableRoot":"/mnt/ao","workspaceNamePrefix":"acme","parameters":{"size":"large"},"startupTimeoutSeconds":600}}`)

	stamped, err := StampCoderWorkspaceName(raw, "worker", "Fix the login bug", sessionID, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := DecodeCoderSessionProfile(stamped)
	if err != nil {
		t.Fatalf("decode stamped profile: %v (%s)", err, stamped)
	}
	if profile.WorkspaceName != "acme-w-fix-1a2b3c4d5e6f" || profile.WorkspaceNameRetry != "acme-w-fix-1a2b3c4d5e6f4a7b8c9d0" {
		t.Fatalf("workspace names = %q, %q; want acme-w-fix-1a2b3c4d5e6f and its retry", profile.WorkspaceName, profile.WorkspaceNameRetry)
	}
	if profile.WorkspaceNameIssuedAt == nil || !profile.WorkspaceNameIssuedAt.Equal(issuedAt) {
		t.Fatalf("issued at = %v, want %v", profile.WorkspaceNameIssuedAt, issuedAt)
	}
	if profile.WorkspaceNamePrefix != "acme" || profile.Parameters["size"] != "large" || profile.StartupTimeoutSeconds != 600 {
		t.Fatalf("stamping changed other profile fields: %+v", profile)
	}
	var top map[string]any
	if err := json.Unmarshal(stamped, &top); err != nil || top["provider"] != "coder" || top["sessionId"] != sessionID {
		t.Fatalf("stamping changed top-level fields: %s (%v)", stamped, err)
	}

	// A profile that already names its workspace keeps that name.
	again, err := StampCoderWorkspaceName(stamped, "worker", "Something else", sessionID, issuedAt.Add(time.Hour))
	if err != nil || string(again) != string(stamped) {
		t.Fatalf("restamp = %s (%v), want the stamped profile unchanged", again, err)
	}

	orchestrator, err := StampCoderWorkspaceName(raw, "orchestrator", "Orchestrator", sessionID, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if profile, err := DecodeCoderSessionProfile(orchestrator); err != nil || profile.WorkspaceName != "acme-o-1a2b3c4d5e6f" {
		t.Fatalf("orchestrator workspace name = %q (%v), want acme-o-1a2b3c4d5e6f", profile.WorkspaceName, err)
	}

	// Profiles of other providers carry no Coder section and are left alone.
	docker := json.RawMessage(`{"provider":"docker","sessionId":"` + sessionID + `"}`)
	if got, err := StampCoderWorkspaceName(docker, "worker", "Fix", sessionID, issuedAt); err != nil || string(got) != string(docker) {
		t.Fatalf("non-Coder profile = %s (%v), want unchanged", got, err)
	}
}

func TestDecodeCoderSessionProfileRejectsInvalidWorkspaceName(t *testing.T) {
	t.Parallel()
	profile := func(name string) json.RawMessage {
		return json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"owner","templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2","durableRoot":"/mnt/ao","workspaceName":"` + name + `"}}`)
	}
	if decoded, err := DecodeCoderSessionProfile(profile(" acme-w-fix-1a2b3c4d5e6f ")); err != nil || decoded.WorkspaceName != "acme-w-fix-1a2b3c4d5e6f" {
		t.Fatalf("valid stored workspace name = %q (%v)", decoded.WorkspaceName, err)
	}
	if _, err := DecodeCoderSessionProfile(profile("Bad_Name")); err == nil {
		t.Fatal("an invalid stored workspace name decoded")
	}
}

// Using the retry name makes it the workspace name and consumes it, so a
// session retries at most once.
func TestUseCoderWorkspaceRetryName(t *testing.T) {
	t.Parallel()
	const sessionID = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	raw := json.RawMessage(`{"provider":"coder","coder":{"baseUrl":"https://coder.example.com","owner":"owner","templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2","durableRoot":"/mnt/ao","workspaceNamePrefix":"acme"}}`)
	issuedAt := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	stamped, err := StampCoderWorkspaceName(raw, "worker", "Fix", sessionID, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	updated, from, to, ok, err := UseCoderWorkspaceRetryName(stamped)
	if err != nil || !ok || from != "acme-w-fix-1a2b3c4d5e6f" || to != "acme-w-fix-1a2b3c4d5e6f4a7b8c9d0" {
		t.Fatalf("use retry = %q -> %q, %v, %v", from, to, ok, err)
	}
	profile, err := DecodeCoderSessionProfile(updated)
	if err != nil || profile.WorkspaceName != to || profile.WorkspaceNameRetry != "" ||
		profile.WorkspaceNameIssuedAt == nil || !profile.WorkspaceNameIssuedAt.Equal(issuedAt) || profile.WorkspaceNamePrefix != "acme" {
		t.Fatalf("profile after retry = %+v (%v)", profile, err)
	}
	if _, _, _, ok, err := UseCoderWorkspaceRetryName(updated); err != nil || ok {
		t.Fatalf("second retry = %v, %v; want none left", ok, err)
	}
	// A legacy profile has no stored name and so no retry.
	if _, _, _, ok, err := UseCoderWorkspaceRetryName(raw); err != nil || ok {
		t.Fatalf("legacy retry = %v, %v; want none", ok, err)
	}
}
