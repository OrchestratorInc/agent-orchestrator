package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A Claude reviewer under a custom endpoint must not carry a subscription
// token or a provider switch beside it: Claude Code prefers both over the
// endpoint's key and base URL.
func TestLauncherSpawnClearsCompetingClaudeCredentialsForCustomEndpoint(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reviewer := &fakeReviewer{env: map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-inherited",
		"CLAUDE_CODE_USE_BEDROCK": "1",
	}}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, dataDir)
	if _, err := l.Spawn(context.Background(), launchSpec()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := rt.createCfg.Env
	if env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("custom endpoint not applied to the reviewer: %v", env)
	}
	for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_API_KEY"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s = %q (set=%v), want blanked beside the custom endpoint", key, value, ok)
		}
	}
}

// A reviewer's endpoint flags and process env come from one resolved
// endpoint: the stored one, with the project's values for any variable it sets.
func TestLauncherEndpointEnvAppliesProjectValues(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("codex", harnessendpoint.Input{BaseURL: "https://stored.example.com", APIKey: "sk-stored", Model: "stored-model"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	l := &agentLauncher{dataDir: dataDir}
	spec := launchSpec()
	spec.Harness = domain.ReviewerCodex
	spec.ProjectEnv = map[string]string{
		harnessendpoint.CodexBaseURLEnv: "https://project.example.com",
		harnessendpoint.CodexAPIKeyEnv:  "sk-project",
		"OPENAI_API_KEY":                "sk-native",
		"UNRELATED":                     "x",
	}
	env, err := l.endpointEnv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if env[harnessendpoint.CodexBaseURLEnv] != "https://project.example.com" || env[harnessendpoint.CodexAPIKeyEnv] != "sk-project" {
		t.Fatalf("project endpoint values not applied: %v", env)
	}
	// A project on another origin gets none of the saved endpoint, not even
	// its model, which that gateway may not serve.
	if _, ok := env[harnessendpoint.CodexModelEnv]; ok {
		t.Fatalf("saved model followed the project's gateway: %v", env)
	}
	if value, found := env["OPENAI_API_KEY"]; !found || value != "" {
		t.Fatal("the project must not restore a competing key beside a saved endpoint")
	}
	if _, ok := env["UNRELATED"]; ok {
		t.Fatalf("only endpoint variables belong in the endpoint env: %v", env)
	}

	// A project URL on the saved origin keeps the saved key and model.
	spec.ProjectEnv = map[string]string{harnessendpoint.CodexBaseURLEnv: "https://stored.example.com/v2"}
	if env, err = l.endpointEnv(spec); err != nil {
		t.Fatal(err)
	}
	if env[harnessendpoint.CodexBaseURLEnv] != "https://stored.example.com/v2" || env[harnessendpoint.CodexAPIKeyEnv] != "sk-stored" || env[harnessendpoint.CodexModelEnv] != "stored-model" {
		t.Fatalf("same-origin project URL must keep the saved key and model: %v", env)
	}

	// A project URL on another origin with no key of its own stops the
	// launch rather than send the saved key, or any other, there.
	spec.ProjectEnv = map[string]string{harnessendpoint.CodexBaseURLEnv: "https://project.example.com"}
	if env, err = l.endpointEnv(spec); !errors.Is(err, harnessendpoint.ErrIncompleteOverride) || env != nil {
		t.Fatalf("URL-only project override: env=%t err=%v", env != nil, err)
	}

	empty := &agentLauncher{dataDir: t.TempDir()}
	if env, err := empty.endpointEnv(launchSpec()); err != nil || env == nil || len(env) != 0 {
		t.Fatalf("no stored or project endpoint: env = %v, err = %v, want a resolved empty env", env, err)
	}
}

func TestLauncherSpawnClearsNativeCodexCredentialsForProjectEndpoint(t *testing.T) {
	for _, name := range []string{"project endpoint", "native provider"} {
		t.Run(name, func(t *testing.T) {
			reviewer := &fakeReviewer{env: map[string]string{"CODEX_API_KEY": "sk-inherited"}}
			rt := &fakeRuntime{}
			launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, t.TempDir())
			spec := launchSpec()
			spec.Harness = domain.ReviewerCodex
			spec.ProjectEnv = map[string]string{"OPENAI_API_KEY": "sk-native"}
			if name == "project endpoint" {
				spec.ProjectEnv[harnessendpoint.CodexBaseURLEnv] = "https://project.example.com"
				spec.ProjectEnv[harnessendpoint.CodexAPIKeyEnv] = "sk-project"
			}
			if _, err := launcher.Spawn(t.Context(), spec); err != nil {
				t.Fatal(err)
			}
			env := rt.createCfg.Env
			if name == "native provider" {
				if env["OPENAI_API_KEY"] != "sk-native" || env["CODEX_API_KEY"] != "sk-inherited" {
					t.Fatal("native credentials must remain available without a custom endpoint")
				}
				return
			}
			if env[harnessendpoint.CodexBaseURLEnv] != "https://project.example.com" || env[harnessendpoint.CodexAPIKeyEnv] != "sk-project" {
				t.Fatal("project endpoint URL and key must reach the reviewer process")
			}
			codex, _ := harnessendpoint.Lookup("codex")
			for _, key := range codex.ClearEnv {
				if value, found := env[key]; !found || value != "" {
					t.Errorf("%s must be blank with an active custom endpoint", key)
				}
			}
		})
	}
}

type endpointChangingRuntime struct {
	fakeRuntime
	store *harnessendpoint.Store
}

func (r *endpointChangingRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if _, err := r.store.Save("codex", harnessendpoint.Input{BaseURL: "https://new.example.com", APIKey: "sk-new"}, time.Now()); err != nil {
		return err
	}
	return r.fakeRuntime.Destroy(ctx, handle)
}

func TestReviewerUsesOneEndpointSnapshotForCommandAndProcess(t *testing.T) {
	for _, name := range []string{"configured", "absent"} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			store := harnessendpoint.ForDataDir(dataDir)
			wantURL, wantKey := "", ""
			if name == "configured" {
				wantURL, wantKey = "https://old.example.com", "sk-old"
				if _, err := store.Save("codex", harnessendpoint.Input{BaseURL: wantURL, APIKey: wantKey}, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			reviewer := &fakeReviewer{}
			rt := &endpointChangingRuntime{store: store}
			launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, dataDir)
			spec := launchSpec()
			spec.Harness = domain.ReviewerCodex
			if _, err := launcher.Spawn(t.Context(), spec); err != nil {
				t.Fatal(err)
			}
			snapshot := reviewer.gotInv.EndpointEnv
			if snapshot == nil || snapshot[harnessendpoint.CodexBaseURLEnv] != wantURL || snapshot[harnessendpoint.CodexAPIKeyEnv] != wantKey {
				t.Fatal("reviewer command must receive the resolved endpoint snapshot")
			}
			env := rt.createCfg.Env
			if env[harnessendpoint.CodexBaseURLEnv] != wantURL || env[harnessendpoint.CodexAPIKeyEnv] != wantKey {
				t.Fatal("reviewer process must use the same endpoint as command construction")
			}
		})
	}
}

func TestReviewerRejectsBrokenEndpointBeforeStartingOrReplacingProcess(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "harness-endpoints", "codex", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	launcher := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.Harness = domain.ReviewerCodex
	if _, err := launcher.Spawn(t.Context(), spec); err == nil {
		t.Fatal("spawn accepted an unreadable endpoint")
	}
	if _, err := launcher.RestoreTerminal(t.Context(), spec); err == nil {
		t.Fatal("restore accepted an unreadable endpoint")
	}
	if rt.created || rt.destroyed != "" {
		t.Fatal("an endpoint error must be returned before touching the runtime")
	}
}

// A reviewer for a project that sets only its own gateway URL must not start:
// the saved key is never sent to a different origin, and no inherited
// credential goes in its place. Nothing is started or replaced.
func TestLauncherStopsOnIncompleteProjectGatewayOverride(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net"}
	if _, err := l.Spawn(context.Background(), spec); !errors.Is(err, harnessendpoint.ErrIncompleteOverride) {
		t.Fatalf("Spawn: err = %v, want an incomplete override", err)
	}
	if _, err := l.RestoreTerminal(context.Background(), spec); !errors.Is(err, harnessendpoint.ErrIncompleteOverride) {
		t.Fatalf("RestoreTerminal: err = %v, want an incomplete override", err)
	}
	if rt.created || rt.destroyed != "" {
		t.Fatal("an incomplete override must stop before the runtime is touched")
	}
}

// A complete project gateway override (its own URL and key on another origin)
// reaches the reviewer with only the project's key: neither the saved key nor
// an inherited credential rides along.
func TestLauncherUsesCompleteProjectGatewayOverride(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reviewer := &fakeReviewer{env: map[string]string{"ANTHROPIC_API_KEY": "sk-inherited", "CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-inherited"}}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net", "ANTHROPIC_AUTH_TOKEN": "sk-project"}
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := rt.createCfg.Env
	if env["ANTHROPIC_BASE_URL"] != "https://other.example.net" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-project" {
		t.Fatalf("project gateway not applied: url=%q", env["ANTHROPIC_BASE_URL"])
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s must be blank beside the project's key (set=%v)", key, ok)
		}
	}
	for key, value := range env {
		if value == "sk-endpoint" {
			t.Fatalf("saved endpoint key reached the project's gateway via %s", key)
		}
	}
}

// The reviewer keeps the endpoint's cleared variables blank even when the
// project sets them, like a worker session.
func TestLauncherKeepsClearedVariablesBlankOverProjectEnv(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, dataDir)
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-project"}
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := rt.createCfg.Env
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s = %q (set=%v), want blank while the custom endpoint is active", key, value, ok)
		}
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("endpoint key = %q, want the saved key", env["ANTHROPIC_AUTH_TOKEN"])
	}
}
