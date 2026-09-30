package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectSettingsLegacyAndPartialMerge(t *testing.T) {
	existing := json.RawMessage(`{"workerAgent":"codex","orchestratorAgent":"claude-code","worker":{"agentConfig":{"model":"worker-model","permissions":"auto"}},"orchestrator":{"agent":"cursor"},"coder":{"templateId":"keep"},"agentRules":"keep rules","autoReview":false}`)
	merged, err := MergeProjectSettingsConfig(existing, json.RawMessage(`{"worker":{"agentConfig":{"effort":"max"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := DecodeProjectSettings(merged)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Worker.Agent != "codex" || settings.Worker.AgentConfig.Model != "worker-model" || settings.Worker.AgentConfig.Permissions != "auto" || settings.Worker.AgentConfig.Effort != "max" || settings.Orchestrator.Agent != "cursor" || *settings.AutoReview {
		t.Fatalf("settings = %+v, worker = %+v", settings, settings.Worker)
	}
	if strings.Contains(string(merged), "workerAgent") || strings.Contains(string(merged), "orchestratorAgent") || !strings.Contains(string(merged), `"templateId":"keep"`) || !strings.Contains(string(merged), `"agentRules":"keep rules"`) {
		t.Fatalf("config lost hidden settings or retained legacy choices: %s", merged)
	}
}

func TestProjectSettingsValidation(t *testing.T) {
	for _, raw := range []string{
		`null`, `{"displayName":null}`, `{"displayName":" "}`, `{"defaultBranch":""}`,
		`{"workerAgent":"codex"}`, `{"config":null}`, `{"config":{"sessionPrefix":"unused"}}`,
		`{"config":{"autoInjectReview":false}}`, `{"config":{"autoReview":"false"}}`,
		`{"config":{"worker":{"agentConfig":{"model":null}}}}`,
		`{"config":{"worker":{"agent":"unknown"}}}`,
		`{"config":{"worker":{"agent":"codex","agentConfig":{"permissions":"unknown"}}}}`,
		`{"config":{"reviewers":[{"harness":"cursor","agentConfig":{"effort":"high"}}]}}`,
		`{"config":{"reviewers":[{"harness":"claude-code","agentConfig":{"effort":"xhigh"}}]}}`,
		`{"config":{"reviewers":[{"harness":"opencode","agentConfig":{"permissions":"accept-edits"}}]}}`,
		`{"config":{"reviewers":[{"harness":"codex"},{"harness":"claude-code"}]}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			patch, err := ParseProjectSettingsPatch(json.RawMessage(raw))
			if err == nil {
				_, err = MergeProjectSettingsConfig(json.RawMessage(`{}`), patch.Config)
			}
			if err == nil {
				t.Fatal("invalid settings were accepted")
			}
		})
	}
}

func TestEffectiveReviewerAndSessionDefaults(t *testing.T) {
	settings, err := DecodeProjectSettings(json.RawMessage(`{"worker":{"agent":"codex","agentConfig":{"model":"worker","effort":"max","permissions":"accept-edits"}},"reviewers":[{"harness":"claude-code","agentConfig":{"model":"reviewer","effort":"high","permissions":"auto"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	harness, config, err := SessionAgentConfig(json.RawMessage(`{"workerAgent":"codex","worker":{"agentConfig":{"model":"default","effort":"max"}}}`), "worker", "codex", "explicit")
	if err != nil || harness != "codex" || config.Model != "explicit" || config.Effort != "max" {
		t.Fatalf("session defaults = %s %+v %v", harness, config, err)
	}
	reviewer := EffectiveReviewer(settings, "codex", "worker", "trusted", config)
	if reviewer.Harness != "claude-code" || reviewer.AgentConfig.Model != "reviewer" || reviewer.AgentConfig.Permissions != "auto" {
		t.Fatalf("reviewer = %+v", reviewer)
	}
	fallback := EffectiveReviewer(ProjectSettingsConfig{}, "codex", "worker", "trusted", config)
	if fallback.Harness != "codex" || fallback.AgentConfig.Model != "worker" || fallback.AgentConfig.Effort != "max" || fallback.AgentConfig.Permissions != "bypass-permissions" {
		t.Fatalf("fallback = %+v", fallback)
	}
}
