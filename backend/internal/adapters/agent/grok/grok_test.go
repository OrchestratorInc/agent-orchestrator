package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "grok" {
		t.Fatalf("ID = %q, want grok", m.ID)
	}
	if m.Name != "Grok Build" {
		t.Fatalf("Name = %q", m.Name)
	}
	hasAgent := false
	for _, c := range m.Capabilities {
		if c == adapters.CapabilityAgent {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatal("missing CapabilityAgent")
	}
}

func TestGetConfigSpecReportsModel(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(spec.Fields) != 1 || spec.Fields[0].Key != "model" {
		t.Fatalf("unexpected fields: %#v", spec.Fields)
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	s, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != ports.PromptDeliveryAfterStart {
		t.Fatalf("strategy = %q, want after_start", s)
	}
}

func TestPromptReadinessHints(t *testing.T) {
	hints, err := (&Plugin{}).PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if hints.InitialDelay != 750*time.Millisecond || hints.PollInterval != 200*time.Millisecond || hints.Timeout != 8*time.Second || hints.Lines != 80 {
		t.Fatalf("hints = %#v", hints)
	}
	if !reflect.DeepEqual(hints.Patterns, []string{"Grok Build"}) {
		t.Fatalf("patterns = %#v, want Grok Build", hints.Patterns)
	}
	if !hints.RenderedScreen {
		t.Fatal("RenderedScreen = false; Grok's footer is only visible on the rendered alternate screen")
	}
	if !reflect.DeepEqual(hints.BlockingPatterns, []string{"Do you trust the contents of this directory?"}) {
		t.Fatalf("blocking patterns = %#v, want Grok's trust dialog heading", hints.BlockingPatterns)
	}
}

func TestGetLaunchCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:       "do the thing",
		SystemPrompt: "ao standing instructions",
		Permissions:  ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	wantPrefix := []string{"grok", "--no-auto-update", "--permission-mode", "bypassPermissions", "--rules", "ao standing instructions"}
	if !reflect.DeepEqual(cmd, wantPrefix) {
		t.Fatalf("cmd = %#v, want prefix %#v", cmd, wantPrefix)
	}
	if strings.Contains(strings.Join(cmd, " "), "system-prompt-override") {
		t.Fatalf("cmd = %#v must append rules, not override Grok's system prompt", cmd)
	}
	assertNoPromptFlag(t, cmd)
}

func TestGetLaunchCommandForwardsModel(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{Model: "  grok-code-fast  "}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"grok", "--no-auto-update", "--model", "grok-code-fast"}; !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandDefaultPerms(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt: "fix it",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"grok", "--no-auto-update"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	if strings.Contains(strings.Join(cmd, " "), "permission-mode") {
		t.Fatal("should not have --permission-mode for default perms")
	}
	assertNoPromptFlag(t, cmd)
}

func TestGetLaunchCommandAcceptEdits(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "refactor auth",
		Permissions: ports.PermissionModeAcceptEdits,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"grok", "--no-auto-update", "--permission-mode", "acceptEdits"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	assertNoPromptFlag(t, cmd)
}

func TestGetLaunchCommandAuto(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "ship it",
		Permissions: ports.PermissionModeAuto,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"grok", "--no-auto-update", "--permission-mode", "auto"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	assertNoPromptFlag(t, cmd)
}

func TestGetLaunchCommandDoesNotPassLeadingDashPromptAsSubcommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt: "-add a health check",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"grok", "--no-auto-update"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	assertNoPromptFlag(t, cmd)
}

func assertNoPromptFlag(t *testing.T, cmd []string) {
	t.Helper()
	for _, arg := range cmd {
		if arg == "-p" || arg == "--single" {
			t.Fatalf("cmd = %#v unexpectedly contains single-turn prompt flag %q", cmd, arg)
		}
	}
}

func TestGetLaunchCommandSystemPromptFromFile(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	promptFile := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(promptFile, []byte("file standing instructions\n\n"), 0o600); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:           "fix it",
		SystemPromptFile: promptFile,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"grok", "--no-auto-update", "--rules", "file standing instructions"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	assertNoPromptFlag(t, cmd)
}

func TestGetLaunchCommandMissingSystemPromptFile(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	_, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:           "fix it",
		SystemPromptFile: filepath.Join(t.TempDir(), "missing.md"),
	})
	if err == nil {
		t.Fatal("expected error for missing system prompt file")
	}
	if !strings.Contains(err.Error(), "grok: read system prompt file") {
		t.Fatalf("err = %v, want system prompt file read error", err)
	}
}

func TestGetRestoreCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID: "sess-abc123",
			},
		},
		SystemPrompt: "ao restore instructions",
		Permissions:  ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{"grok", "--no-auto-update", "--permission-mode", "bypassPermissions", "--rules", "ao restore instructions", "-r", "sess-abc123"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	if strings.Contains(strings.Join(cmd, " "), "system-prompt-override") {
		t.Fatalf("cmd = %#v must append rules, not override Grok's system prompt", cmd)
	}
}

func TestGetRestoreCommandMapsPermissionModes(t *testing.T) {
	tests := []struct {
		name       string
		permission ports.PermissionMode
		want       []string
	}{
		{"accept edits", ports.PermissionModeAcceptEdits, []string{"grok", "--no-auto-update", "--permission-mode", "acceptEdits", "-r", "sess-abc123"}},
		{"auto", ports.PermissionModeAuto, []string{"grok", "--no-auto-update", "--permission-mode", "auto", "-r", "sess-abc123"}},
		{"bypass permissions", ports.PermissionModeBypassPermissions, []string{"grok", "--no-auto-update", "--permission-mode", "bypassPermissions", "-r", "sess-abc123"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "grok"}
			cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Session: ports.SessionRef{
					Metadata: map[string]string{
						ports.MetadataKeyAgentSessionID: "sess-abc123",
					},
				},
				Permissions: tt.permission,
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !ok {
				t.Fatal("ok=false, want true")
			}
			if !reflect.DeepEqual(cmd, tt.want) {
				t.Fatalf("cmd = %#v, want %#v", cmd, tt.want)
			}
		})
	}
}

func TestGetRestoreCommandNoID(t *testing.T) {
	tests := []struct {
		name string
		ref  ports.SessionRef
	}{
		{"empty metadata", ports.SessionRef{Metadata: map[string]string{}}},
		{"blank agent session metadata", ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "   "}}},
		{"ao session id only", ports.SessionRef{ID: "ao-7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "grok"}
			cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Session: tt.ref,
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if ok {
				t.Fatal("ok=true with no agentSessionId, want false")
			}
			if cmd != nil {
				t.Fatalf("cmd = %#v, want nil", cmd)
			}
		})
	}
}

func TestSessionInfoReadsHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "grok-ses-1",
			ports.MetadataKeyTitle:          "Fix login redirect",
			ports.MetadataKeySummary:        "Updated the auth callback and tests.",
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if info.AgentSessionID != "grok-ses-1" {
		t.Fatalf("AgentSessionID = %q, want grok-ses-1", info.AgentSessionID)
	}
	if info.Title != "Fix login redirect" {
		t.Fatalf("Title = %q", info.Title)
	}
	if info.Summary != "Updated the auth callback and tests." {
		t.Fatalf("Summary = %q", info.Summary)
	}
}

func TestSessionInfoFalseWhenNoHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("ok=true with empty metadata, want false")
	}
	if !reflect.DeepEqual(info, ports.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero", info)
	}
}

func TestGetAgentHooksInstallsGrokCommandsInClaudeSettings(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	ctx := context.Background()
	ws := t.TempDir()
	settingsPath := grokClaudeSettingsPath(ws)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my own stop hook","timeout":5},{"type":"command","command":"ao hooks claude-code stop","timeout":30}]}]},"permissions":{"defaultMode":"plan"}}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{
		WorkspacePath: ws,
		SessionID:     "grok-test-1",
	}

	if err := plugin.GetAgentHooks(ctx, cfg); err != nil {
		t.Fatalf("GetAgentHooks: %v", err)
	}
	if err := plugin.GetAgentHooks(ctx, cfg); err != nil {
		t.Fatalf("second GetAgentHooks: %v", err)
	}
	if installed, err := plugin.AreHooksInstalled(ctx, ws); err != nil || !installed {
		t.Fatalf("AreHooksInstalled after install = (%v, %v), want (true, nil)", installed, err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks       map[string][]hooksjson.MatcherGroup `json:"hooks"`
		Permissions json.RawMessage                     `json:"permissions"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Hooks == nil {
		t.Fatalf("hooks object missing: %s", data)
	}
	for _, spec := range grokManagedHooks {
		if got := countGrokHookCommand(config.Hooks[spec.Event], spec.Command); got != 1 {
			t.Fatalf("%s command %q count = %d, want 1", spec.Event, spec.Command, got)
		}
		claudeCommand := strings.Replace(spec.Command, "ao hooks grok ", "ao hooks claude-code ", 1)
		if spec.Event != "Stop" && countGrokHookCommand(config.Hooks[spec.Event], claudeCommand) != 0 {
			t.Fatalf("%s unexpectedly installed Claude command %q", spec.Event, claudeCommand)
		}
	}
	if countGrokHookCommand(config.Hooks["Stop"], "my own stop hook") != 1 {
		t.Fatalf("existing Stop hook not preserved: %#v", config.Hooks["Stop"])
	}
	if countGrokHookCommand(config.Hooks["Stop"], "ao hooks claude-code stop") != 1 {
		t.Fatalf("existing Claude AO Stop hook not preserved: %#v", config.Hooks["Stop"])
	}
	if len(config.Permissions) == 0 {
		t.Fatalf("unrelated settings clobbered: %s", data)
	}
	if m := grokMatcherForCommand(config.Hooks["SessionStart"], "ao hooks grok session-start"); m == nil || *m != "startup" {
		t.Fatalf("SessionStart matcher = %v, want startup", m)
	}
	if m := grokMatcherForCommand(config.Hooks["UserPromptSubmit"], "ao hooks grok user-prompt-submit"); m != nil {
		t.Fatalf("UserPromptSubmit matcher = %v, want none", m)
	}
	if m := grokMatcherForCommand(config.Hooks["Notification"], "ao hooks grok notification"); m != nil {
		t.Fatalf("Notification matcher = %v, want none", m)
	}

	if err := plugin.UninstallHooks(ctx, ws); err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}
	if installed, err := plugin.AreHooksInstalled(ctx, ws); err != nil || installed {
		t.Fatalf("AreHooksInstalled after uninstall = (%v, %v), want (false, nil)", installed, err)
	}

	data, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	config = struct {
		Hooks       map[string][]hooksjson.MatcherGroup `json:"hooks"`
		Permissions json.RawMessage                     `json:"permissions"`
	}{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, spec := range grokManagedHooks {
		if got := countGrokHookCommand(config.Hooks[spec.Event], spec.Command); got != 0 {
			t.Fatalf("%s command %q count = %d after uninstall, want 0", spec.Event, spec.Command, got)
		}
	}
	if countGrokHookCommand(config.Hooks["Stop"], "my own stop hook") != 1 {
		t.Fatalf("user Stop hook not preserved after uninstall: %#v", config.Hooks["Stop"])
	}
	if countGrokHookCommand(config.Hooks["Stop"], "ao hooks claude-code stop") != 1 {
		t.Fatalf("Claude AO Stop hook not preserved after uninstall: %#v", config.Hooks["Stop"])
	}
	if len(config.Permissions) == 0 {
		t.Fatalf("unrelated settings clobbered after uninstall: %s", data)
	}
}

func countGrokHookCommand(groups []hooksjson.MatcherGroup, command string) int {
	count := 0
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				count++
			}
		}
	}
	return count
}

func grokMatcherForCommand(groups []hooksjson.MatcherGroup, command string) *string {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return group.Matcher
			}
		}
	}
	return nil
}
