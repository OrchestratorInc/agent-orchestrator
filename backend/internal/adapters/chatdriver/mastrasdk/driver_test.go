package mastrasdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type installedPlugin struct {
	binary       string
	status       ports.AgentAuthStatus
	resolveCalls int
}

func (p *installedPlugin) ResolveBinary(context.Context) (string, error) {
	p.resolveCalls++
	return p.binary, nil
}
func (p *installedPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return p.status, nil
}

func TestCandidateRejectsUnsupportedLifecycleSettingsBeforeDiscovery(t *testing.T) {
	p := &installedPlugin{}
	driver := NewCandidate(p, nil)
	for _, cfg := range []ports.ChatStartConfig{
		{ReadOnly: true}, {DisableNetwork: true}, {Ephemeral: true},
		{AdditionalDirectories: []string{"/other"}},
	} {
		if _, err := driver.Start(t.Context(), cfg); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
			t.Fatalf("Start: %v", err)
		}
	}
	for _, cfg := range []ports.ChatResumeConfig{{}, {ProviderConversationID: "native", FreshIfMissing: true}} {
		if _, err := driver.Resume(t.Context(), cfg); !errors.Is(err, ports.ErrChatResumeFailed) {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := driver.(ports.ChatDriverReconnector).Reconnect(t.Context(), cfg); !errors.Is(err, ports.ErrChatResumeFailed) {
			t.Fatalf("Reconnect: %v", err)
		}
	}
	if p.resolveCalls != 0 {
		t.Fatal("unsupported request discovered/launched a provider")
	}
}

func TestCandidateRejectsUnsupportedPolicyAndEffort(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeAcceptEdits, ports.PermissionModeBypassPermissions, "invented"} {
		if err := validateSettings(mode, ports.ChatTurnSettings{}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
			t.Fatalf("initial %q: %v", mode, err)
		}
		if err := validateSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Approval: mode}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
			t.Fatalf("turn %q: %v", mode, err)
		}
	}
	if err := validateSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Effort: "high"}); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatal(err)
	}
	if err := validateSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Model: "native/model"}); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateLaunchOwnsPrivateInstructionsAndNativeStorage(t *testing.T) {
	for _, key := range []string{"MASTRA_APP_DATA_DIR", "MASTRA_DB_PATH", "MASTRA_DB_URL", "MASTRA_STORAGE_BACKEND", "MASTRACODE_YOLO"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Resolution only: no executable or native/provider process is run.
	node := filepath.Join(binDir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nexit 93\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	native := filepath.Join(root, "native-cli.js")
	if err := os.WriteFile(native, []byte("// independently installed entrypoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := acpdriver.LaunchConfig{SessionID: domain.SessionID("owned-session"), DataDir: root,
		WorkspacePath: t.TempDir(), SystemPrompt: "private additive context", Env: map[string]string{"KEEP": "yes", "MASTRA_APP_DATA_DIR": filepath.Join(root, "mastra-code", "profile")}}
	launch, err := binding(&installedPlugin{binary: native}).Launch(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != node || len(launch.Args) != 3 || launch.Args[1] != native {
		t.Fatalf("launch = %+v", launch)
	}
	if strings.Contains(strings.Join(launch.Args, " "), cfg.SystemPrompt) {
		t.Fatal("private instructions in process argv")
	}
	var saved struct {
		Owner        string `json:"owner"`
		Instructions string `json:"hostInstructions"`
		Workspace    string `json:"workspace"`
	}
	data, err := os.ReadFile(launch.Args[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Owner != "owned-session" || saved.Instructions != cfg.SystemPrompt || saved.Workspace != cfg.WorkspacePath {
		t.Fatal("private launch configuration lost")
	}
	if launch.Env["KEEP"] != "yes" || !strings.HasPrefix(launch.Env["MASTRA_DB_PATH"], root+string(os.PathSeparator)) {
		t.Fatal("native storage not owned")
	}
	cfg.Env["MASTRA_DB_PATH"] = "/unrelated/native.db"
	if _, err := binding(&installedPlugin{binary: native}).Launch(t.Context(), cfg); err == nil {
		t.Fatal("accepted unrelated native database")
	}
	delete(cfg.Env, "MASTRA_DB_PATH")
	profile := launch.Env["MASTRA_APP_DATA_DIR"]
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "settings.json"), []byte(`{"backgroundTools":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := binding(&installedPlugin{binary: native}).Launch(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "background tools") {
		t.Fatalf("unsupported background settings were not rejected before SDK boot: %v", err)
	}
}

func TestOwnedRuntimeFilesRefuseReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	if err := writePrivate(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(path, []byte("changed")); err == nil {
		t.Fatal("overwrote changed runtime configuration")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "first" {
		t.Fatal("existing data changed")
	}
}

func TestCandidateRejectsStockACPAndUnexpectedBridgeIdentity(t *testing.T) {
	for _, response := range []acpsdk.InitializeResponse{
		{},
		{AgentInfo: &acpsdk.Implementation{Name: "mastracode", Version: "1.11.0"}},
		{AgentInfo: &acpsdk.Implementation{Name: "ao-mastra-sdk-candidate", Version: "future"}},
	} {
		if err := validateInitialize(response); err == nil {
			t.Fatal("accepted unreviewed bridge contract")
		}
	}
	if err := validateInitialize(acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentInfo:         &acpsdk.Implementation{Name: "ao-mastra-sdk-candidate", Version: "1.11.0"},
		AgentCapabilities: acpsdk.AgentCapabilities{LoadSession: true},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateCopiesOnlyNativeProfileConfigurationWithoutOverwriting(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "owned")
	for _, name := range []string{"auth.json", "settings.json", "history.db"} {
		if err := os.WriteFile(filepath.Join(from, name), []byte(`{"testOnly":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyNativeProfile(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "history.db")); !os.IsNotExist(err) {
		t.Fatal("copied unrelated native history")
	}
	if err := os.WriteFile(filepath.Join(to, "auth.json"), []byte(`{"refreshed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyNativeProfile(from, to); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(to, "auth.json"))
	if string(data) != `{"refreshed":true}` {
		t.Fatal("overwrote owned credentials")
	}
	sourceData, _ := os.ReadFile(filepath.Join(from, "auth.json"))
	if string(sourceData) != `{"testOnly":true}` {
		t.Fatal("changed source profile")
	}
}
