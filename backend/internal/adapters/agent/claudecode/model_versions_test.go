package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestParseVersionTooOldReadsBothAPIErrorForms(t *testing.T) {
	cases := map[string]string{
		"raw json": `API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"Claude Code 2.0.67 does not support this model; version 2.1.280 or newer is required. Run 'claude update', or update the Claude desktop app, then try again.","details":{"error_code":"claude_code_version_too_old"}}}`,
		"plain":    "API Error: 400 Claude Code 2.1.172 does not support this model; version 2.1.251 or newer is required. Run 'claude update', or update the Claude desktop app, then try again.",
	}
	want := map[string]VersionTooOld{
		"raw json": {Installed: "2.0.67", Required: "2.1.280"},
		"plain":    {Installed: "2.1.172", Required: "2.1.251"},
	}
	for name, text := range cases {
		got, ok := ParseVersionTooOld(text)
		if !ok || got != want[name] {
			t.Fatalf("%s: ParseVersionTooOld = %#v, %v; want %#v", name, got, ok, want[name])
		}
	}
	if _, ok := ParseVersionTooOld(`API Error: 404 {"type":"error","error":{"type":"not_found_error","message":"model: fable"}}`); ok {
		t.Fatal("unknown-alias 404 parsed as a version requirement")
	}
	if !IsVersionTooOld(`{"details":{"error_code":"claude_code_version_too_old"}}`) {
		t.Fatal("error code not recognized")
	}
}

func TestModelVersionRequirementsPersistAndOverrideSeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-code", "model-versions.json")
	requirements := NewModelVersionRequirements(path)
	if got, ok := requirements.Required("claude-opus-5-5"); !ok || got != "2.1.280" {
		t.Fatalf("seeded requirement = %q, %v", got, ok)
	}
	if err := requirements.Record("us.anthropic.claude-opus-5-5-v1:0", "2.1.281"); err != nil {
		t.Fatal(err)
	}
	if err := requirements.Record("claude-opus-5-5", "2.1.290"); err != nil {
		t.Fatal(err)
	}
	if err := requirements.Record("claude-opus-5-5", "not-a-version"); err == nil {
		t.Fatal("invalid version recorded")
	}

	reloaded := NewModelVersionRequirements(path)
	if got, ok := reloaded.Required("us.anthropic.claude-opus-5-5-v1:0"); !ok || got != "2.1.281" {
		t.Fatalf("reloaded learned requirement = %q, %v", got, ok)
	}
	if got, _ := reloaded.Required("claude-opus-5-5"); got != "2.1.290" {
		t.Fatalf("learned requirement did not override seed: %q", got)
	}
	if _, ok := reloaded.Required("claude-sonnet-5-5"); ok {
		t.Fatal("requirement invented for a model the API never gated")
	}
}

func TestGateCatalogMarksOnlyModelsNewerThanInstalled(t *testing.T) {
	requirements := NewModelVersionRequirements("")
	catalog := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "claude-opus-5-5", Label: "Opus 5.5", IsDefault: true},
		{ID: "claude-fable-5-1", Label: "Fable 5.1"},
		{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5"},
	}}

	gated := requirements.GateCatalog(catalog, "2.1.260")
	opus, fable, sonnet := gated.Models[0], gated.Models[1], gated.Models[2]
	if opus.RequiresVersion != "2.1.280" || opus.UnavailableReason == "" || opus.IsDefault {
		t.Fatalf("opus = %#v, want gated and no longer default", opus)
	}
	if fable.RequiresVersion != "" || fable.UnavailableReason != "" {
		t.Fatalf("fable = %#v, want available on 2.1.260", fable)
	}
	if sonnet.RequiresVersion != "" {
		t.Fatalf("sonnet = %#v, want ungated", sonnet)
	}
	if !catalog.Models[0].IsDefault || catalog.Models[0].RequiresVersion != "" {
		t.Fatal("GateCatalog mutated the input catalog")
	}

	if unknown := requirements.GateCatalog(catalog, ""); unknown.Models[0].RequiresVersion != "" {
		t.Fatal("unknown installed version gated a model")
	}
	if current := requirements.GateCatalog(catalog, "2.1.309"); current.Models[0].RequiresVersion != "" || !current.Models[0].IsDefault {
		t.Fatal("current CLI still gated")
	}
}

func TestInstalledVersionCachesByExecutableFingerprint(t *testing.T) {
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	original := claudeVersionCommand
	claudeVersionCommand = func(context.Context, string) ([]byte, error) {
		calls++
		return []byte("2.1.239 (Claude Code)\n"), nil
	}
	t.Cleanup(func() { claudeVersionCommand = original })

	for range 2 {
		got, ok := InstalledVersion(context.Background(), binary)
		if !ok || got != "2.1.239" {
			t.Fatalf("InstalledVersion = %q, %v", got, ok)
		}
	}
	if calls != 1 {
		t.Fatalf("version command calls = %d, want 1 (cached)", calls)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n# updated\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := InstalledVersion(context.Background(), binary); !ok || calls != 2 {
		t.Fatalf("updated binary was not re-probed: calls = %d", calls)
	}
}

func TestUnsupportedModelReasonGatesConcreteModelsOnly(t *testing.T) {
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := claudeVersionCommand
	claudeVersionCommand = func(context.Context, string) ([]byte, error) {
		return []byte("2.1.239 (Claude Code)\n"), nil
	}
	t.Cleanup(func() { claudeVersionCommand = original })
	ctx := context.Background()

	for _, model := range []string{"claude-opus-5-5", "claude-opus-5-5[1m]"} {
		if reason := UnsupportedModelReason(ctx, binary, model); reason == "" {
			t.Fatalf("%s on 2.1.239 was not refused", model)
		}
	}
	for _, model := range []string{"opus", "claude-sonnet-5-5"} {
		if reason := UnsupportedModelReason(ctx, binary, model); reason != "" {
			t.Fatalf("%s refused: %s", model, reason)
		}
	}
	if reason := UnsupportedModelReason(ctx, filepath.Join(t.TempDir(), "missing"), "claude-opus-5-5"); reason != "" {
		t.Fatalf("unknown installed version refused a model: %s", reason)
	}
}

func TestLastAssistantModelSkipsSyntheticAndTruncatedLines(t *testing.T) {
	data := []byte(`ated":true}
{"type":"user","message":{"role":"user"}}
{"type":"assistant","message":{"model":"claude-opus-4-8","role":"assistant"}}
{"type":"assistant","message":{"model":"<synthetic>","role":"assistant"}}
{"type":"system"}
`)
	if got := lastAssistantModelFromJSONL(data); got != "claude-opus-4-8" {
		t.Fatalf("last assistant model = %q", got)
	}
	if got := lastAssistantModelFromJSONL([]byte(`{"type":"user"}`)); got != "" {
		t.Fatalf("model without assistant message = %q", got)
	}
}

func TestLastAssistantModelReadsSessionTranscript(t *testing.T) {
	configDir := t.TempDir()
	sessionID := "8320947f-5806-4579-943f-b23a943bac1f"
	projectDir := filepath.Join(configDir, "projects", "-work-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"assistant","message":{"model":"claude-haiku-4-5-20251001"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LastAssistantModel(context.Background(), map[string]string{claudeConfigDirEnv: configDir}, sessionID)
	if err != nil || got != "claude-haiku-4-5-20251001" {
		t.Fatalf("LastAssistantModel = %q, %v", got, err)
	}
}
