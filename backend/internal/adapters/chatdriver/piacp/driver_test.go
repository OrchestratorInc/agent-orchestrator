package piacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePiPlugin struct {
	status ports.AgentAuthStatus
	err    error
}

func (p fakePiPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return p.status, p.err
}

func TestLaunchUsesPreinstalledAdapterAndPreservesPiEnvironment(t *testing.T) {
	dataDir := t.TempDir()
	cfg := buildConfig(
		fakePiPlugin{status: ports.AgentAuthStatusAuthorized},
		func(context.Context) (string, error) { return "/user/bin/pi-acp", nil },
		nil,
	)
	if err := cfg.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	launchCfg := acpdriver.LaunchConfig{
		SessionID:     "session-1",
		DataDir:       dataDir,
		WorkspacePath: filepath.Join(dataDir, "worktree"),
		Env: map[string]string{
			"PI_CODING_AGENT_DIR": "/user/pi-config",
			"ANTHROPIC_API_KEY":   "from-project",
			"PATH":                "/user/bin",
		},
		SystemPrompt: "AO standing instructions",
	}
	launch, err := cfg.Launch(context.Background(), launchCfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launch.Command != "/user/bin/pi-acp" || len(launch.Args) != 0 {
		t.Fatalf("launch = %#v, want exact pre-installed pi-acp with no installer arguments", launch)
	}
	for key, want := range launchCfg.Env {
		if got := launch.Env[key]; got != want {
			t.Errorf("env[%q] = %q, want %q", key, got, want)
		}
	}
	wantSocketDir := filepath.Join(dataDir, "pi-acp", "session-1", "run")
	if got := launch.Env["PI_ACP_SOCKET_DIR"]; got != wantSocketDir {
		t.Fatalf("PI_ACP_SOCKET_DIR = %q, want %q", got, wantSocketDir)
	}
	content, err := os.ReadFile(filepath.Join(instructionDir(launchCfg), "AGENTS.md"))
	if err != nil {
		t.Fatalf("read generated Pi instructions: %v", err)
	}
	if string(content) != "AO standing instructions\n" {
		t.Fatalf("generated instructions = %q", content)
	}

	meta := cfg.SessionMeta(launchCfg)
	piMeta := meta["piAcp"].(map[string]any)
	manifest := piMeta["manifest"].(map[string]any)
	roots := manifest["roots"].([]any)
	projectPaths := roots[0].(map[string]any)["paths"].(map[string]any)
	if projectPaths["cwd"] != launchCfg.WorkspacePath {
		t.Fatalf("project root = %#v, want ACP cwd %q", projectPaths, launchCfg.WorkspacePath)
	}
	aoPaths := roots[1].(map[string]any)["paths"].(map[string]any)
	if aoPaths["agentDir"] != filepath.Join(instructionDir(launchCfg), "agent") {
		t.Fatalf("AO instruction root = %#v", aoPaths)
	}
}

func TestLaunchHonorsCanceledContextBeforeWritingStandingInstructions(t *testing.T) {
	dataDir := t.TempDir()
	cfg := buildConfig(
		fakePiPlugin{status: ports.AgentAuthStatusAuthorized},
		func(context.Context) (string, error) { return "/user/bin/pi-acp", nil },
		nil,
	)
	launchCfg := acpdriver.LaunchConfig{
		SessionID:    "session-1",
		DataDir:      dataDir,
		SystemPrompt: "AO standing instructions",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := cfg.Launch(ctx, launchCfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("Launch error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(instructionDir(launchCfg)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instruction directory stat error = %v, want os.ErrNotExist", err)
	}
}

func TestMissingPiACPDoesNotDownload(t *testing.T) {
	want := errors.New("not found")
	cfg := buildConfig(fakePiPlugin{}, func(context.Context) (string, error) { return "", want }, nil)
	err := cfg.Probe(context.Background())
	if !errors.Is(err, ports.ErrChatDriverUnavailable) || !errors.Is(err, want) {
		t.Fatalf("Probe error = %v", err)
	}
}

func TestInstallCheckNamesTheMissingAdapter(t *testing.T) {
	missing := buildConfig(fakePiPlugin{}, func(context.Context) (string, error) { return "", errors.New("not found") }, nil)
	if err := missing.InstallCheck(context.Background()); err == nil || !strings.Contains(err.Error(), "npm i -g @victor-software-house/pi-acp") {
		t.Fatalf("InstallCheck error = %v, want the install command", err)
	}
	installed := buildConfig(fakePiPlugin{}, func(context.Context) (string, error) { return "/user/bin/pi-acp", nil }, nil)
	if err := installed.InstallCheck(context.Background()); err != nil {
		t.Fatalf("InstallCheck with pi-acp installed = %v", err)
	}
}

func TestProbeReusesPiAuth(t *testing.T) {
	cfg := buildConfig(
		fakePiPlugin{status: ports.AgentAuthStatusUnauthorized},
		func(context.Context) (string, error) { return "/user/bin/pi-acp", nil },
		nil,
	)
	if err := cfg.Probe(context.Background()); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("Probe error = %v, want ErrChatAuthRequired", err)
	}
}

func TestPiACPDistributionAndVersionPolicy(t *testing.T) {
	info := func(name, version string) acpsdk.InitializeResponse {
		return acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: name, Version: version}}
	}
	for _, tc := range []struct {
		name    string
		init    acpsdk.InitializeResponse
		wantErr bool
	}{
		{name: "tested minimum", init: info(distributionName, minimumVersion)},
		{name: "newer compatible", init: info(distributionName, "0.18.0")},
		{name: "old", init: info(distributionName, "0.17.0"), wantErr: true},
		{name: "wrong distribution", init: info("community/pi-acp", minimumVersion), wantErr: true},
		{name: "missing info", init: acpsdk.InitializeResponse{}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInitialize(tc.init)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateInitialize error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestPiACPFeatureMapping(t *testing.T) {
	cfg := buildConfig(fakePiPlugin{}, func(context.Context) (string, error) { return "pi-acp", nil }, nil)
	for _, capability := range []ports.ChatCapability{
		ports.ChatCapabilityStreaming,
		ports.ChatCapabilityTools,
		ports.ChatCapabilityInterrupt,
		ports.ChatCapabilityResume,
		ports.ChatCapabilityUsage,
		ports.ChatCapabilityDiffs,
	} {
		if !cfg.Capabilities.Has(capability) {
			t.Errorf("capability %q is false", capability)
		}
	}
	if cfg.Capabilities.Has(ports.ChatCapabilityApprovals) {
		t.Fatal("Pi ACP advertises approvals even though it cannot enforce AO permission modes")
	}
	if missing := ports.MissingProductionCapabilities(cfg.Capabilities); !reflect.DeepEqual(missing, []ports.ChatCapability{ports.ChatCapabilityApprovals}) {
		t.Fatalf("missing production capabilities = %v, want approvals so mutating Chat admission is refused", missing)
	}

	got := cfg.SessionOptions(ports.ChatTurnSettings{Model: "anthropic/claude-sonnet-4", Effort: "high"})
	want := []acpdriver.SessionOption{
		{ID: "model", Value: "anthropic/claude-sonnet-4"},
		{ID: "thought_level", Value: "high"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session options = %#v, want %#v", got, want)
	}
}

func TestPiReportsATurnWithoutAModelCall(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	check := buildConfig(nil, nil, nil).SessionPromptResponseFailure
	turn := func(total int) acpsdk.PromptResponse {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn, Usage: &acpsdk.Usage{TotalTokens: total}}
	}
	if err := check("s1", turn(15), true); err != nil {
		t.Fatalf("answered turn = %v, want no failure", err)
	}
	// A later turn with no reply leaves the session's total as it was.
	if err := check("s1", turn(15), false); err == nil || !strings.Contains(err.Error(), "Pi did not reach its model") {
		t.Fatalf("a turn with no reply and no new tokens = %v, want a reported failure", err)
	}
	// Tokens used: the model was reached, though it said nothing.
	if err := check("s1", turn(40), false); err != nil {
		t.Fatalf("a turn that used tokens = %v, want no failure", err)
	}
	// After a daemon restart no total is known; a turn with no reply still fails.
	if err := check("s2", turn(500), false); err == nil {
		t.Fatal("an unanswered first turn after a restart was taken as answered")
	}
	// A provider that reports no usage: an answered turn is fine.
	if err := check("s3", acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, true); err != nil {
		t.Fatalf("answered turn without usage = %v, want no failure", err)
	}
	if err := check("s4", acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, false); err != nil {
		t.Fatalf("cancelled turn = %v, want no failure", err)
	}
}

func TestPromptResponseFailureShowsTheModelErrorPiRecorded(t *testing.T) {
	agentDir := t.TempDir()
	folder := filepath.Join(agentDir, "sessions", "--workspace--")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	// As Pi records a reply the provider refused.
	record := `{"type":"session","id":"01a151e7"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[],"model":"gpt-5.5","stopReason":"error",` +
		`"errorMessage":"Codex error: {\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",` +
		`\"message\":\"The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account.\"}}"}}` + "\n"
	if err := os.WriteFile(filepath.Join(folder, "2026-10-19T02-05-17-807Z_01a151e7.jsonl"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	recorded := recordedModelError([]string{t.TempDir(), agentDir}, "01a151e7")
	if recorded != "The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account." {
		t.Fatalf("recorded error = %q", recorded)
	}
	empty := acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn, Usage: &acpsdk.Usage{}}
	err := promptResponseFailure(empty, recorded)
	if err == nil || !strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "sign-in") {
		t.Fatalf("failure = %v, want the model's refusal instead of a sign-in guess", err)
	}
	if !strings.Contains(err.Error(), "not supported when using Codex with a ChatGPT account") {
		t.Fatalf("failure = %q, want the model's own words", err)
	}
	if got := recordedModelError([]string{agentDir}, "missing"); got != "" {
		t.Fatalf("a session with no record = %q, want none", got)
	}
}

func TestLaunchPreloadsTheCurrentModelsIntoPiACP(t *testing.T) {
	dataDir := t.TempDir()
	entry := filepath.Join(t.TempDir(), "index.mjs")
	if err := os.WriteFile(entry, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	entry, _ = filepath.EvalSymlinks(entry)
	cfg := buildConfig(fakePiPlugin{}, func(context.Context) (string, error) { return entry, nil }, nil)
	launch, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{
		SessionID: "session-1", DataDir: dataDir, Env: map[string]string{"BUN_OPTIONS": "--smol"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	dir := filepath.Join(dataDir, "prompts", "session-1", "pi-acp-models")
	script := filepath.Join(dir, "models-preload.mjs")
	// The user's own Bun options are kept; the preload is added to them.
	if got := launch.Env["BUN_OPTIONS"]; got != "--smol --preload="+script {
		t.Fatalf("BUN_OPTIONS = %q", got)
	}
	if got := launch.Env["AO_PI_MODELS_JSON"]; got != filepath.Join(dir, "models.json") {
		t.Fatalf("AO_PI_MODELS_JSON = %q", got)
	}
	// The preload acts only in pi-acp itself and gives Pi's commands the user's options back.
	if launch.Env["AO_PI_ACP_ENTRY"] != entry || launch.Env["AO_PI_BUN_OPTIONS"] != "--smol" {
		t.Fatalf("AO_PI_ACP_ENTRY = %q, AO_PI_BUN_OPTIONS = %q", launch.Env["AO_PI_ACP_ENTRY"], launch.Env["AO_PI_BUN_OPTIONS"])
	}
	if written, err := os.ReadFile(script); err != nil || string(written) != string(modelsPreload) {
		t.Fatalf("preload script = %d bytes, %v; want the embedded one", len(written), err)
	}

	// A data directory Bun cannot name in BUN_OPTIONS leaves pi-acp as it is.
	spaced := filepath.Join(t.TempDir(), "with space")
	launch, err = cfg.Launch(context.Background(), acpdriver.LaunchConfig{SessionID: "session-2", DataDir: spaced})
	if err != nil {
		t.Fatalf("Launch with a spaced data directory: %v", err)
	}
	if _, set := launch.Env["BUN_OPTIONS"]; set {
		t.Fatalf("BUN_OPTIONS = %q, want none", launch.Env["BUN_OPTIONS"])
	}
}
