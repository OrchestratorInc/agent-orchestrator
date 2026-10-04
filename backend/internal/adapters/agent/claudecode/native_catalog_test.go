package claudecode

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

func nativeFixture(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	binary := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestNativePickerPreservesOfficialOrderAndCapabilities(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_CUSTOM_MODEL_OPTION", "")
	t.Setenv("CLAUDE_MODEL_CONFIG", "")
	cwd := t.TempDir()
	capture := filepath.Join(cwd, "invocation")
	binary := nativeFixture(t, `printf '%s\n' "$PWD" "$MODEL_SCOPE" "$@" > "$CAPTURE"
IFS= read -r request
case "$request" in *'"subtype":"initialize"'*) ;; *) exit 9 ;; esac
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-catalog","response":{"models":[{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)","description":"Use the default model (currently Opus 5.5) · prices","supportsEffort":true,"supportedEffortLevels":["low","high"]},{"value":"opus","resolvedModel":"claude-opus-5-5","displayName":"Opus","supportedEffortLevels":["high"]},{"value":"fable","displayName":"Fable"},{"value":"haiku","displayName":"Haiku","supportsEffort":false}]}}}'
exec sleep 30
`)
	got, err := nativePickerModels(context.Background(), binary, cwd, map[string]string{"MODEL_SCOPE": "project-account", "CAPTURE": capture})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range got {
		ids = append(ids, model.ID)
	}
	if !reflect.DeepEqual(ids, []string{"opus", "fable", "haiku"}) {
		t.Fatalf("models = %#v", got)
	}
	if got[0].Label != "Opus 5.5" || !got[0].IsDefault || !reflect.DeepEqual(got[0].Efforts, []string{"high"}) || got[1].Efforts != nil || got[2].Efforts == nil {
		t.Fatalf("metadata = %#v", got)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	invocation := string(raw)
	for _, want := range []string{cwd, "project-account", "--no-session-persistence", `{"disableAllHooks":true}`, "--strict-mcp-config", `{"mcpServers":{}}`} {
		if !strings.Contains(invocation, want) {
			t.Fatalf("missing %q in invocation", want)
		}
	}
}

func TestNativePickerFailureAndCancellation(t *testing.T) {
	tests := []struct{ name, output string }{
		{"malformed", "not-json"},
		{"empty", `{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-catalog","response":{"models":[]}}}`},
		{"rejected", `{"type":"control_response","response":{"subtype":"error","request_id":"ao-model-catalog","error":"secret"}}`},
		{"interaction", `{"type":"control_request","request_id":"hook","request":{"subtype":"can_use_tool"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := nativeFixture(t, "IFS= read -r request\nprintf '%s\\n' '"+tt.output+"'\n")
			_, err := nativePickerModels(context.Background(), binary, t.TempDir(), nil)
			if err == nil {
				t.Fatal("want failure")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("raw response leaked")
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		binary := nativeFixture(t, "exec sleep 30\n")
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := nativePickerModels(ctx, binary, t.TempDir(), nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatal("cancellation did not reap promptly")
		}
	})
}

func TestNativePickerBoundsOutput(t *testing.T) {
	binary := nativeFixture(t, "IFS= read -r request\nhead -c 5000000 /dev/zero | tr '\\000' x\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := nativePickerModels(ctx, binary, t.TempDir(), nil)
	if err == nil {
		t.Fatal("want bounded-output error")
	}
}

func TestNativeDefaultRetainsUnmatchedResolvedModel(t *testing.T) {
	models := normalizeNativeModels([]nativeModel{{Value: "default", ResolvedModel: "provider-custom", Description: "Use the default model (currently Custom)"}, {Value: "opus", ResolvedModel: "different", DisplayName: "Opus"}})
	if len(models) != 2 || models[0].ID != "provider-custom" || !models[0].IsDefault || models[0].Label != "Custom" {
		t.Fatalf("models = %#v", models)
	}
}

func TestNativeLabelsUseOfficialReleaseDescriptions(t *testing.T) {
	tests := []struct {
		model nativeModel
		want  string
	}{
		{nativeModel{DisplayName: "Fable", Description: "Fable 5.1 · capabilities", ResolvedModel: "claude-fable-5-1"}, "Fable 5.1"},
		{nativeModel{DisplayName: "Sonnet", Description: "Sonnet 5.5 · routine tasks"}, "Sonnet 5.5"},
		{nativeModel{DisplayName: "Haiku", ResolvedModel: "claude-haiku-4-5-20251001"}, "Haiku 4.5"},
		{nativeModel{DisplayName: "Custom", Description: "1M context · Model 9"}, "Custom"},
	}
	for _, tt := range tests {
		if got := nativeModelLabel(tt.model); got != tt.want {
			t.Fatalf("label = %q, want %q", got, tt.want)
		}
	}
}

func TestNativeConfiguredExtrasFollowNativeModels(t *testing.T) {
	native := []nativeModel{{Value: "default", ResolvedModel: "claude-opus-5-5", Description: "Use default (currently Opus 5.5)"}, {Value: "opus", ResolvedModel: "claude-opus-5-5", DisplayName: "Opus"}, {Value: "sonnet", ResolvedModel: "claude-sonnet-5-5", DisplayName: "Sonnet"}}
	settings := agentcreds.ClaudeSettings{Model: "forced-primary", Env: map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-5-5", "ANTHROPIC_DEFAULT_SONNET_MODEL": "forced-sonnet", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "sonnet", "ANTHROPIC_SMALL_FAST_MODEL": "forced-fast"}}
	got := appendNativeConfiguredModels(normalizeNativeModels(native), native, settings, map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "custom-option", "CLAUDE_MODEL_CONFIG": `{"availableModels":["forced-sonnet","account-extra","opus"]}`})
	ids := []string{}
	for _, model := range got {
		ids = append(ids, model.ID)
	}
	want := []string{"opus", "sonnet", "forced-primary", "forced-sonnet", "forced-fast", "custom-option", "account-extra"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("IDs = %v, want %v", ids, want)
	}
	if got[0].IsDefault || !got[2].IsDefault {
		t.Fatal("configured model must remain the effective launch default")
	}
}

func TestNativeConfiguredDefaultWhenNativeDefaultAbsent(t *testing.T) {
	native := []nativeModel{{Value: "sonnet", ResolvedModel: "claude-sonnet-5-5", DisplayName: "Sonnet"}}
	for _, configured := range []string{"claude-sonnet-5-5", "forced-primary"} {
		got := appendNativeConfiguredModels(normalizeNativeModels(native), native, agentcreds.ClaudeSettings{Model: configured}, map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "", "CLAUDE_MODEL_CONFIG": ""})
		if len(got) == 1 && !got[0].IsDefault || len(got) == 2 && !got[1].IsDefault {
			t.Fatalf("models = %#v", got)
		}
	}
}

func TestNativeExtrasFingerprintTracksExplicitAndProcessChanges(t *testing.T) {
	clearClaudeCredentialEnv(t)
	for _, key := range []string{"ANTHROPIC_CUSTOM_MODEL_OPTION", "CLAUDE_MODEL_CONFIG"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "one")
			before := ProviderCatalogFingerprint(context.Background(), "claude", "", nil)
			t.Setenv(key, "two")
			if ProviderCatalogFingerprint(context.Background(), "claude", "", nil) == before {
				t.Fatal("process change not observed")
			}
			if ProviderCatalogFingerprint(context.Background(), "claude", "", map[string]string{key: "one"}) != before {
				t.Fatal("explicit environment did not override process")
			}
		})
	}
}

func TestNativeConfiguredAliasDefaultDoesNotChangeOfficialSequence(t *testing.T) {
	native := []nativeModel{{Value: "default", ResolvedModel: "claude-opus-5-5"}, {Value: "opus", ResolvedModel: "claude-opus-5-5", DisplayName: "Opus"}, {Value: "haiku", ResolvedModel: "claude-haiku-4-5", DisplayName: "Haiku"}}
	got := appendNativeConfiguredModels(normalizeNativeModels(native), native, agentcreds.ClaudeSettings{Model: "haiku"}, map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "", "CLAUDE_MODEL_CONFIG": ""})
	if len(got) != 2 || got[0].ID != "opus" || got[0].IsDefault || got[1].ID != "haiku" || !got[1].IsDefault {
		t.Fatalf("models=%+v", got)
	}
}

func TestNativeSupplementChecksConcreteIDsAndRejectsRestrictions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("CLAUDE_MODEL_CONFIG", "")
	capture := filepath.Join(t.TempDir(), "requests")
	binary := nativeFixture(t, `IFS= read -r request
printf '%s\n' "$request" > "$CAPTURE"
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-catalog","response":{"models":[{"value":"sonnet","resolvedModel":"claude-sonnet-current","displayName":"Sonnet"}]}}}'
IFS= read -r request
printf '%s\n' "$request" >> "$CAPTURE"
printf '%s\n' '{"type":"control_response","response":{"subtype":"error","request_id":"ao-model-check-1","error":"Model is not in your organization'"'"'s allowed models. Falling back to default."}}'
IFS= read -r request
printf '%s\n' "$request" >> "$CAPTURE"
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-check-2"}}'
exec sleep 30
`)
	models, err := nativePickerModelsWithSupplement(context.Background(), binary, t.TempDir(), map[string]string{"CAPTURE": capture}, func(models []ports.AgentModelInfo, represented map[string]bool, check func(string) (bool, error)) ([]ports.AgentModelInfo, error) {
		if !represented["sonnet"] || !represented["claude-sonnet-current"] {
			t.Fatal("native alias identities missing")
		}
		for _, id := range []string{"claude-haiku-blocked", "claude-opus-allowed"} {
			allowed, err := check(id)
			if err != nil {
				return nil, err
			}
			if allowed {
				models = append(models, ports.AgentModelInfo{ID: id, IsAdditional: true})
			}
		}
		return models, nil
	})
	if err != nil || len(models) != 2 || models[1].ID != "claude-opus-allowed" || !models[1].IsAdditional {
		t.Fatalf("models=%+v error=%v", models, err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"type":"user"`) || strings.Count(string(raw), `"subtype":"initialize"`) != 1 || !strings.Contains(string(raw), `"model":"claude-opus-allowed"`) {
		t.Fatalf("unexpected requests: %s", raw)
	}
}

func TestNativeSupplementFailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"unsupported", `{"type":"control_response","response":{"subtype":"error","request_id":"ao-model-check-1","error":"Unknown operation"}}`},
		{"interaction", `{"type":"control_request","request_id":"tool"}`},
		{"malformed", `not-json`},
		{"uncorrelated", `{"type":"control_response","response":{"subtype":"success","request_id":"other"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := nativeFixture(t, `IFS= read -r request
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-catalog","response":{"models":[{"value":"sonnet"}]}}}'
IFS= read -r request
`+"printf '%s\\n' '"+tc.reply+"'\n")
			checked := false
			_, err := nativePickerModelsWithSupplement(context.Background(), binary, t.TempDir(), nil, func(models []ports.AgentModelInfo, represented map[string]bool, check func(string) (bool, error)) ([]ports.AgentModelInfo, error) {
				checked = true
				_, err := check("claude-concrete")
				return models, err
			})
			if !checked {
				t.Fatal("fixture failed before candidate check")
			}
			if err == nil {
				t.Fatal("want failed expansion")
			}
		})
	}
}

func TestNativeModelCheckVersionGate(t *testing.T) {
	for _, tc := range []struct {
		version   string
		supported bool
	}{{"2.1.267 (Claude Code)", false}, {"2.1.268 (Claude Code)", true}, {"2.1.289 (Claude Code)", true}, {"2.2.0", true}, {"unknown", false}} {
		if got := supportsNativeModelChecks(tc.version); got != tc.supported {
			t.Errorf("%q supported=%v", tc.version, got)
		}
	}
}

func TestExpandedNativeCatalogMergesOnlyCheckedAdditionalIDs(t *testing.T) {
	clearClaudeCredentialEnv(t)
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	server := withStubValidator(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-current"},{"id":"claude-haiku-extra","display_name":"Haiku Extra"}]}`))
	})
	old := claudeModelAuthReport
	t.Cleanup(func() { claudeModelAuthReport = old })
	claudeModelAuthReport = func(context.Context, string, string, map[string]string) (claudeAuthReport, bool) {
		return claudeAuthReport{APIProvider: "gateway"}, true
	}
	capture := filepath.Join(t.TempDir(), "requests")
	binary := nativeFixture(t, `if [ "$1" = "--version" ]; then printf '%s\n' '2.1.289 (Claude Code)'; exit 0; fi
IFS= read -r request
printf '%s\n' "$request" > "$CAPTURE"
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-catalog","response":{"models":[{"value":"sonnet","resolvedModel":"claude-sonnet-current","displayName":"Sonnet"}]}}}'
IFS= read -r request
printf '%s\n' "$request" >> "$CAPTURE"
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-check-1"}}'
exec sleep 30
`)
	env := map[string]string{"ANTHROPIC_API_KEY": "test-account", "ANTHROPIC_BASE_URL": server.URL, "CAPTURE": capture}
	got, err := ExpandedNativeCatalog(context.Background(), binary, t.TempDir(), env)
	if err != nil || !got.AdditionalModelsLoaded || !got.AdditionalModelsAvailable || len(got.Models) != 2 || got.Models[0].ID != "sonnet" || got.Models[1].ID != "claude-haiku-extra" || !got.Models[1].IsAdditional {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"subtype":"set_model"`) != 1 || strings.Contains(string(raw), `"model":"claude-sonnet-current"`) {
		t.Fatalf("unexpected candidate checks %s", raw)
	}
	// A later final identity observation changing accounts rejects the combined result.
	calls := 0
	claudeModelAuthReport = func(_ context.Context, _ string, _ string, env map[string]string) (claudeAuthReport, bool) {
		calls++
		if calls == 3 {
			env["ANTHROPIC_API_KEY"] = "different-account"
		}
		return claudeAuthReport{APIProvider: "gateway"}, true
	}
	got, err = ExpandedNativeCatalog(context.Background(), binary, t.TempDir(), env)
	if !errors.Is(err, ports.ErrAgentModelDiscoveryIdentityChanged) || got.AdditionalModelsLoaded || len(got.Models) != 0 {
		t.Fatalf("mixed identity catalog=%+v err=%v", got, err)
	}
}

func TestNativeSupplementBudgetExhaustionFailsWholeExpansion(t *testing.T) {
	binary := nativeFixture(t, `IFS= read -r request
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-catalog","response":{"models":[{"value":"sonnet"}]}}}'
IFS= read -r request
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"ao-model-check-1"}}'
exec sleep 30
`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	checked := 0
	models, err := nativePickerModelsWithSupplement(ctx, binary, t.TempDir(), nil, func(models []ports.AgentModelInfo, represented map[string]bool, check func(string) (bool, error)) ([]ports.AgentModelInfo, error) {
		for _, id := range []string{"concrete-one", "concrete-two"} {
			allowed, err := check(id)
			if err != nil {
				return nil, err
			}
			if allowed {
				checked++
			}
		}
		return models, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || models != nil || checked != 1 {
		t.Fatalf("partial models=%+v checked=%d error=%v", models, checked, err)
	}
}
