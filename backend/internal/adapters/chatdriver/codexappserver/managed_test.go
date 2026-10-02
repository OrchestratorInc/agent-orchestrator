package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func managedTestConfig(t *testing.T) ports.ChatStartConfig {
	t.Helper()
	return ports.ChatStartConfig{SessionID: "managed-test", ControllerGeneration: "owner-a", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Model: "gpt-test", Effort: "medium", ProviderScopeID: "scope-a", ProviderIDsScoped: true,
		Env:   map[string]string{managedCodexTokenEnv: "synthetic-route-a"},
		Route: &ports.AgentProviderRoute{BaseURL: "http://127.0.0.1:43127", TokenEnv: managedCodexTokenEnv}}
}

func managedTestDriver(t *testing.T) (*ManagedDriver, *scriptedServer, *persistenthost.Config, *int) {
	t.Helper()
	base, server := newTestDriver(t)
	d := NewManaged(base.plugin, base.log)
	var launch persistenthost.Config
	stopped := 0
	d.open = func(ctx context.Context, cfg persistenthost.Config) (managedHost, error) {
		launch = cfg
		proc, err := base.spawn(ctx, cfg.Argv[0], cfg.Workdir, cfg.Env)
		if err != nil {
			return managedHost{}, err
		}
		proc.terminate = func() error { stopped++; return proc.stop() }
		return managedHost{process: proc, identity: strings.Repeat("a", 64)}, nil
	}
	return d, server, &launch, &stopped
}

func TestManagedChatPinsBeforeFirstProtocolRequest(t *testing.T) {
	d, server, launch, stopped := managedTestDriver(t)
	cfg := managedTestConfig(t)
	conv, err := d.Start(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	request := server.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["modelProvider"] != "ao_accounts_manager" || params["model"] != cfg.Model || launch.OwnershipFingerprint == "" {
		t.Fatal("thread opened without explicit provider or launch identity")
	}
	if conv.Capabilities()[ports.ChatCapabilityRateLimits] {
		t.Fatal("device quota was advertised as managed account quota")
	}
	if _, err := conv.(ports.ChatUsageReporter).ReadRateLimits(t.Context()); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatal("native account quota request was admitted")
	}
	if server.sentMethod("account/rateLimits/read") || server.sentMethod("account/read") || server.sentMethod("account/login/start") {
		t.Fatal("managed launch used device account APIs")
	}
	if conv.(*managedConversation).HostIdentity() == "" {
		t.Fatal("managed host identity was lost")
	}
	if err := conv.(*managedConversation).Terminate(); err != nil || *stopped != 1 {
		t.Fatal("managed conversation did not use its exact teardown", err)
	}
}

func TestManagedChatRefusesIncompleteRoutesBeforeLaunch(t *testing.T) {
	for _, scenario := range []string{"missing route", "missing token", "missing generation", "foreign endpoint", "extra roots", "tool server"} {
		t.Run(scenario, func(t *testing.T) {
			d, server, launch, stopped := managedTestDriver(t)
			cfg := managedTestConfig(t)
			switch scenario {
			case "missing route":
				cfg.Route = nil
			case "missing token":
				cfg.Env = nil
			case "missing generation":
				cfg.ControllerGeneration = ""
			case "foreign endpoint":
				cfg.Route.BaseURL = "https://example.test"
			case "extra roots":
				cfg.AdditionalDirectories = []string{t.TempDir()}
			case "tool server":
				cfg.MCPServers = []ports.ChatMCPServerConfig{{Name: "unconfigured"}}
			}
			if _, err := d.Start(t.Context(), cfg); err == nil {
				t.Fatal("unsupported or incomplete launch succeeded")
			}
			if launch.SessionID != "" || *stopped != 0 || server.sentMethod("initialize") {
				t.Fatal("invalid request reached a provider process")
			}
		})
	}
}

func TestManagedChatResumeDoesNotFallBack(t *testing.T) {
	d, server, _, stopped := managedTestDriver(t)
	server.replyError("thread/resume", -1, "synthetic missing history")
	cfg := managedTestConfig(t)
	_, err := d.Resume(t.Context(), ports.ChatResumeConfig{SessionID: cfg.SessionID, ControllerGeneration: cfg.ControllerGeneration,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Route: cfg.Route, ProviderConversationID: "thread-1"})
	if !errors.Is(err, ports.ErrChatResumeFailed) || *stopped != 1 || server.sentMethod("thread/start") {
		t.Fatal("unsuccessful resume did not preserve its failure boundary", err)
	}
}

func TestManagedChatStartDoesNotReportResumeFailure(t *testing.T) {
	for _, scenario := range []string{"request error", "missing thread"} {
		t.Run(scenario, func(t *testing.T) {
			d, server, _, stopped := managedTestDriver(t)
			if scenario == "request error" {
				server.replyError("thread/start", -1, "synthetic start failure")
			} else {
				server.reply("thread/start", `{"thread":{"id":""}}`)
			}
			_, err := d.Start(t.Context(), managedTestConfig(t))
			if err == nil || errors.Is(err, ports.ErrChatResumeFailed) || *stopped != 1 {
				t.Fatal("new conversation failure was misclassified or leaked its provider", err)
			}
		})
	}
}

func TestManagedChatReattachmentDoesNotRotateAuthorizationOrReplay(t *testing.T) {
	d, server, _, stopped := managedTestDriver(t)
	open := d.open
	d.open = func(ctx context.Context, cfg persistenthost.Config) (managedHost, error) {
		host, err := open(ctx, cfg)
		if err == nil {
			host.process.reconnected, host.process.nextRequestID = true, 41
		}
		return host, err
	}
	cfg := managedTestConfig(t)
	prepared := 0
	conv, err := d.Resume(t.Context(), ports.ChatResumeConfig{SessionID: cfg.SessionID, ControllerGeneration: cfg.ControllerGeneration,
		DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Route: cfg.Route, ProviderConversationID: "thread-1",
		PrepareEnv: func(context.Context) (map[string]string, error) { prepared++; return cfg.Env, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	if _, err := conv.(ports.ChatModelLister).ListModels(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := server.awaitFrame(func(f frame) bool { return f.Method == "model/list" })
	if request.ID == nil || string(*request.ID) != "42" || prepared != 0 || *stopped != 0 || server.sentMethod("initialize") || server.sentMethod("thread/resume") {
		t.Fatal("live adoption replayed or rotated the existing controller")
	}
}
