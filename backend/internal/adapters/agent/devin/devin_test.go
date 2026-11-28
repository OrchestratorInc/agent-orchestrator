package devin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "devin" {
		t.Fatalf("ID = %q, want devin", m.ID)
	}
	if m.Name != "Devin" {
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

func TestGetConfigSpecCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Plugin{}).GetConfigSpec(ctx); err == nil {
		t.Fatal("expected ctx error, got nil")
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	s, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != ports.PromptDeliveryInCommand {
		t.Fatalf("strategy = %q, want in_command", s)
	}
}

func TestPreLaunchCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&Plugin{}).PreLaunch(ctx, ports.LaunchConfig{WorkspacePath: "/workspace"}); err == nil {
		t.Fatal("expected ctx error, got nil")
	}
}

func TestEnsureDevinWorkspaceTrustedCreatesEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	seed := `{"userID":"abc","projects":{"/existing/proj":{"hasTrustDialogAccepted":true,"lastCost":1.5}}}`
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	work := "/Users/me/.ao/worktrees/01ABC"
	if err := ensureDevinWorkspaceTrusted(cfgPath, work); err != nil {
		t.Fatalf("ensureDevinWorkspaceTrusted: %v", err)
	}

	root := readJSONMap(t, cfgPath)
	projects := root["projects"].(map[string]any)
	newEntry := projects[work].(map[string]any)
	if newEntry["hasTrustDialogAccepted"] != true {
		t.Fatalf("new entry not trusted: %#v", newEntry)
	}
	existing := projects["/existing/proj"].(map[string]any)
	if existing["hasTrustDialogAccepted"] != true || existing["lastCost"].(float64) != 1.5 {
		t.Fatalf("existing project clobbered: %#v", existing)
	}
	if root["userID"] != "abc" {
		t.Fatalf("top-level key clobbered: %#v", root["userID"])
	}
}

func TestEnsureDevinNativeWorkspaceTrustedCreatesEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "trusted_workspaces.json")
	seed := `{"trusted_paths":["/existing/proj"]}`
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	work := "/Users/me/.ao/worktrees/01ABC"
	if err := ensureDevinNativeWorkspaceTrusted(cfgPath, work); err != nil {
		t.Fatalf("ensureDevinNativeWorkspaceTrusted: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var trusted devinTrustedWorkspaces
	if err := json.Unmarshal(data, &trusted); err != nil {
		t.Fatalf("parse trusted_workspaces.json: %v", err)
	}
	want := []string{"/existing/proj", work}
	if !reflect.DeepEqual(trusted.TrustedPaths, want) {
		t.Fatalf("trusted paths = %#v, want %#v", trusted.TrustedPaths, want)
	}
}

func TestEnsureDevinNativeWorkspaceTrustedIsIdempotentAndNoWriteWhenAlreadyTrusted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "trusted_workspaces.json")
	work := "/w"
	if err := os.WriteFile(cfgPath, []byte(`{"trusted_paths":["/w"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info1, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := ensureDevinNativeWorkspaceTrusted(cfgPath, work); err != nil {
		t.Fatalf("ensureDevinNativeWorkspaceTrusted: %v", err)
	}

	info2, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("expected no rewrite when already trusted")
	}
}

func TestEnsureDevinNativeWorkspaceTrustedCreatesMissingConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "missing", "trusted_workspaces.json")
	work := "/fresh/worktree"

	if err := ensureDevinNativeWorkspaceTrusted(cfgPath, work); err != nil {
		t.Fatalf("ensureDevinNativeWorkspaceTrusted: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var trusted devinTrustedWorkspaces
	if err := json.Unmarshal(data, &trusted); err != nil {
		t.Fatalf("parse trusted_workspaces.json: %v", err)
	}
	if !reflect.DeepEqual(trusted.TrustedPaths, []string{work}) {
		t.Fatalf("trusted paths = %#v, want [%q]", trusted.TrustedPaths, work)
	}
}

func TestEnsureDevinWorkspaceTrustedIsIdempotentAndNoWriteWhenAlreadyTrusted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	work := "/w"
	if err := os.WriteFile(cfgPath, []byte(`{"projects":{"/w":{"hasTrustDialogAccepted":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info1, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := ensureDevinWorkspaceTrusted(cfgPath, work); err != nil {
		t.Fatalf("ensureDevinWorkspaceTrusted: %v", err)
	}

	info2, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("expected no rewrite when already trusted")
	}
}

func TestEnsureDevinWorkspaceTrustedCreatesMissingConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	work := "/fresh/worktree"

	if err := ensureDevinWorkspaceTrusted(cfgPath, work); err != nil {
		t.Fatalf("ensureDevinWorkspaceTrusted: %v", err)
	}

	root := readJSONMap(t, cfgPath)
	projects := root["projects"].(map[string]any)
	entry := projects[work].(map[string]any)
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("entry not trusted in freshly-created config: %#v", entry)
	}
}

func TestGetLaunchCommandBypass(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "do the thing",
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"devin", "--permission-mode", "dangerous", "--", "do the thing"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandForwardsModel(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{Model: "  sonnet-4.6  "},
		Prompt: "fix it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"devin", "--model", "sonnet-4.6", "--", "fix it"}; !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandDefaultPerms(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt: "fix it",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"devin", "--", "fix it"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	if strings.Contains(strings.Join(cmd, " "), "permission-mode") {
		t.Fatal("should not have --permission-mode for default perms")
	}
	if strings.Contains(strings.Join(cmd, " "), "-p") {
		t.Fatal("should not use Devin print mode for prompted launches")
	}
}

func TestGetLaunchCommandAcceptEdits(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "refactor auth",
		Permissions: ports.PermissionModeAcceptEdits,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"devin", "--permission-mode", "accept-edits", "--", "refactor auth"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandAuto(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "ship it",
		Permissions: ports.PermissionModeAuto,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"devin", "--permission-mode", "auto", "--", "ship it"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandNoPrompt(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"devin"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Plugin{}).GetLaunchCommand(ctx, ports.LaunchConfig{Prompt: "x"}); err == nil {
		t.Fatal("expected ctx error, got nil")
	}
}

func TestGetRestoreCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID: "sess-abc123",
			},
		},
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{"devin", "--permission-mode", "dangerous", "-r", "sess-abc123"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandNoID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	_, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{}},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("ok=true with no agentSessionId, want false")
	}
}

func TestGetRestoreCommandWhitespaceID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	_, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "   ",
		}},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("ok=true with whitespace agentSessionId, want false")
	}
}

func TestSessionInfoReadsHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "devin-ses-1",
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
	if info.AgentSessionID != "devin-ses-1" {
		t.Fatalf("AgentSessionID = %q, want devin-ses-1", info.AgentSessionID)
	}
	if info.Title != "Fix login redirect" {
		t.Fatalf("Title = %q", info.Title)
	}
	if info.Summary != "Updated the auth callback and tests." {
		t.Fatalf("Summary = %q", info.Summary)
	}
}

func TestSessionInfoFalseWhenNoHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
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

func TestGetAgentHooksInstallsLocalDevinConfig(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "devin"}
	ws := t.TempDir()
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: ws,
		SessionID:     "devin-test-1",
	}); err != nil {
		t.Fatalf("GetAgentHooks: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(ws, ".devin", "config.local.json"))
	if err != nil {
		t.Fatalf("read config.local.json: %v", err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse config.local.json: %v\n%s", err, data)
	}
	sessionStart := config.Hooks["SessionStart"]
	if len(sessionStart) != 1 || len(sessionStart[0].Hooks) != 1 {
		t.Fatalf("SessionStart hooks = %#v, want one AO command", sessionStart)
	}
	hook := sessionStart[0].Hooks[0]
	if hook.Type != "command" || hook.Command != "ao hooks devin session-start" || hook.Timeout != 30 {
		t.Fatalf("SessionStart hook = %#v", hook)
	}
	gitignore, err := os.ReadFile(filepath.Join(ws, ".devin", ".gitignore"))
	if err != nil {
		t.Fatalf("read .devin/.gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "config.local.json") {
		t.Fatalf(".devin/.gitignore does not ignore config.local.json:\n%s", gitignore)
	}
}

func TestGetAgentHooksCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&Plugin{}).GetAgentHooks(ctx, ports.WorkspaceHookConfig{}); err == nil {
		t.Fatal("expected ctx error, got nil")
	}
}

func TestResolveDevinBinaryFallback(t *testing.T) {
	// When the binary is not on PATH or any well-known location, the resolver
	// MUST surface ports.ErrAgentBinaryNotFound rather than a silent string
	// fallback that lets a missing CLI launch into an empty tmux pane.
	bin, err := ResolveDevinBinary(context.Background())
	if err != nil {
		if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
			t.Fatalf("err = %v, want ports.ErrAgentBinaryNotFound", err)
		}
		return
	}
	if bin == "" {
		t.Fatal("ResolveDevinBinary returned empty path with no error")
	}
}

func TestResolveDevinBinaryCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveDevinBinary(ctx); err == nil {
		t.Fatal("expected ctx error, got nil")
	}
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}
