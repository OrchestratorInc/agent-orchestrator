package codewhale

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func lifecycleTestRunner(_ context.Context, _ string, _ string, _ map[string]string, args ...string) ([]byte, error) {
	if len(args) == 1 && args[0] == "--version" {
		return []byte("codewhale 0.10.0 (1be1a703b975)\n"), nil
	}
	if len(args) == 3 && args[0] == "config" && args[1] == "get" && args[2] == "managed_config_path" {
		return []byte("error: key not found: managed_config_path\n"), errors.New("exit status 1")
	}
	return nil, errors.New("unexpected command")
}

func TestGetAgentHooksWritesHiddenOwnedInstructions(t *testing.T) {
	workspace := t.TempDir()
	p := &Plugin{resolvedBinary: "/opt/codewhale", runCommand: lifecycleTestRunner}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: "Follow AO standing instructions."}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, ".codewhale", "rules", standingInstructionsName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), standingInstructionsMark) || !strings.Contains(string(data), cfg.SystemPrompt) {
		t.Fatalf("instructions = %q", data)
	}
	ignore, err := os.ReadFile(filepath.Join(filepath.Dir(path), ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), "/"+standingInstructionsName) {
		t.Fatalf("gitignore = %q, err=%v", ignore, err)
	}
}

func TestGetAgentHooksRefusesForeignInstructions(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".codewhale", "rules", standingInstructionsName)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{resolvedBinary: "/opt/codewhale", runCommand: lifecycleTestRunner}
	if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err == nil {
		t.Fatal("expected foreign-file refusal")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "user-owned\n" {
		t.Fatalf("foreign file changed: %q", data)
	}
}

func TestGetAgentHooksRequiresLifecycleVersion(t *testing.T) {
	p := &Plugin{
		resolvedBinary: "/opt/codewhale",
		runCommand: func(_ context.Context, _ string, _ string, _ map[string]string, _ ...string) ([]byte, error) {
			return []byte("codewhale 0.9.12\n"), nil
		},
	}
	err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), minLifecycleVersion) {
		t.Fatalf("error = %v, want minimum-version failure", err)
	}
}

func TestGetAgentHooksRefusesExistingManagedConfigEnvironment(t *testing.T) {
	p := &Plugin{resolvedBinary: "/opt/codewhale", runCommand: lifecycleTestRunner}
	err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: t.TempDir(),
		Env:           map[string]string{managedConfigEnv: "/user/managed.toml"},
	})
	if err == nil || !strings.Contains(err.Error(), managedConfigEnv) {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareRuntimeLaunchWritesFencedOverlayAndResetsOutbox(t *testing.T) {
	dataDir := t.TempDir()
	codewhaleHome := t.TempDir()
	runPath := filepath.Join(dataDir, "running.json")
	if err := runfile.Write(runPath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	outbox := filepath.Join(dataDir, "agent-runtime", adapterID, "sessions", "proj-1", "lifecycle.jsonl")
	if err := os.MkdirAll(filepath.Dir(outbox), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outbox, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AO_RUNTIME_LAUNCH_ID": "launch-new", "AO_RUN_FILE": runPath, codewhaleHomeEnv: codewhaleHome}
	if err := (&Plugin{}).PrepareRuntimeLaunch(context.Background(), ports.WorkspaceHookConfig{
		DataDir: dataDir, SessionID: "proj-1", Env: env,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox was not reset: %v", err)
	}
	configPath := env[managedConfigEnv]
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"[lifecycle_outbox]",
		"path = " + strconv.Quote(outbox),
		"http://127.0.0.1:43123/api/v1/sessions/proj-1/activity/codewhale?launchId=launch-new",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("managed config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "[hooks]") || strings.Contains(text, "CODEWHALE_HOME") {
		t.Fatalf("managed overlay displaced user-owned state:\n%s", text)
	}
	wantRuntimeDir := filepath.Join(dataDir, "agent-runtime", adapterID, "sessions", "proj-1", "launches", "launch-new", "runtime")
	if env[codewhaleRuntimeDirEnv] != wantRuntimeDir {
		t.Fatalf("%s = %q, want %q", codewhaleRuntimeDirEnv, env[codewhaleRuntimeDirEnv], wantRuntimeDir)
	}
	locatorData, err := os.ReadFile(nativeSessionLocatorPath(dataDir, "proj-1", "launch-new"))
	if err != nil {
		t.Fatal(err)
	}
	var locator nativeSessionLocator
	if err := json.Unmarshal(locatorData, &locator); err != nil {
		t.Fatal(err)
	}
	if locator.RuntimeDir != wantRuntimeDir || locator.SessionsDir != filepath.Join(codewhaleHome, "sessions") || locator.SessionID != "" {
		t.Fatalf("locator = %+v", locator)
	}
}

func TestLifecycleSavedSessionIDFlowsToExactRestoreCommand(t *testing.T) {
	dataDir := t.TempDir()
	codewhaleHome := t.TempDir()
	workspace := t.TempDir()
	runPath := filepath.Join(dataDir, "running.json")
	if err := runfile.Write(runPath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"AO_RUNTIME_LAUNCH_ID": "launch-7",
		"AO_RUN_FILE":          runPath,
		codewhaleHomeEnv:       codewhaleHome,
	}
	p := &Plugin{resolvedBinary: "/opt/codewhale"}
	if err := p.PrepareRuntimeLaunch(context.Background(), ports.WorkspaceHookConfig{
		DataDir: dataDir, SessionID: "ao-1", WorkspacePath: workspace, Env: env,
	}); err != nil {
		t.Fatal(err)
	}

	const savedSessionID = "0e9dfb74-4a65-4067-964f-152e432cccb6"
	sessionsDir := filepath.Join(codewhaleHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	document := savedSessionIdentity{}
	document.Metadata.ID = savedSessionID
	document.Metadata.RuntimeStore = &struct {
		DataDir string `json:"data_dir"`
	}{DataDir: env[codewhaleRuntimeDirEnv]}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, savedSessionID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, ok, err := p.ResolveNativeSessionID(context.Background(), ports.NativeSessionResolveConfig{
		DataDir: dataDir, SessionID: "ao-1", LaunchID: "launch-7",
	})
	if err != nil || !ok || resolved != savedSessionID {
		t.Fatalf("resolved=%q ok=%v err=%v", resolved, ok, err)
	}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: resolved},
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	if got := cmd[len(cmd)-2:]; got[0] != "--resume" || got[1] != savedSessionID {
		t.Fatalf("restore tail = %#v, want saved UUID", got)
	}
}
