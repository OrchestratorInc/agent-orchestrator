package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func forkTestDriver(agent *fakeAgent) *Driver {
	driver := New(Config{
		Harness:                      domain.HarnessClaudeCode,
		Capabilities:                 ports.ChatCapabilities{ports.ChatCapabilityFork: true},
		Launch:                       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMeta:                  func(cfg LaunchConfig) map[string]any { return map[string]any{"systemPrompt": cfg.SystemPrompt} },
		EncodeProviderConversationID: func(id string) string { return "owned:" + id },
		DecodeProviderConversationID: func(id string) (string, error) {
			if !strings.HasPrefix(id, "owned:") {
				return "", errors.New("missing ownership")
			}
			return strings.TrimPrefix(id, "owned:"), nil
		},
	}, nil)
	driver.useTestProcess(fakeSpawn(agent))
	return driver
}

func TestACPIsolatedForkActivatesChildAndPreservesSettings(t *testing.T) {
	for _, load := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "load"}[load], func(t *testing.T) {
			agent := &fakeAgent{forkID: "child", capabilities: &acpsdk.AgentCapabilities{
				LoadSession: load, McpCapabilities: acpsdk.McpCapabilities{Http: true, Sse: true},
				SessionCapabilities: acpsdk.SessionCapabilities{
					Fork: &acpsdk.SessionForkCapabilities{}, Resume: &acpsdk.SessionResumeCapabilities{},
					AdditionalDirectories: &acpsdk.SessionAdditionalDirectoriesCapabilities{},
				},
			}}
			driver := forkTestDriver(agent)
			cfg := ports.ChatStartConfig{
				SessionID: "side-host", WorkspacePath: t.TempDir(), DataDir: t.TempDir(),
				Model: "", Permissions: ports.PermissionModeDefault, SystemPrompt: "side boundary",
				ProviderScopeID: "side", AdditionalDirectories: []string{t.TempDir()},
				MCPServers: []ports.ChatMCPServerConfig{
					{Name: "stdio", Type: "stdio", Command: "tool", Env: map[string]string{"AO_SESSION_ID": "side-host"}},
					{Name: "http", Type: "http", URL: "https://example.invalid/mcp", Headers: map[string]string{"Authorization": "test"}},
					{Name: "sse", Type: "sse", URL: "https://example.invalid/sse"},
				},
			}
			child, err := driver.ForkIntoHost(t.Context(), "owned:parent", "", cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Close() }()
			if child.ProviderConversationID() != "owned:child" ||
				!child.Capabilities().Has(ports.ChatCapabilityForkCurrent) || child.Capabilities().Has(ports.ChatCapabilityFork) {
				t.Fatalf("child identity or semantics: %s %v", child.ProviderConversationID(), child.Capabilities())
			}
			agent.mu.Lock()
			defer agent.mu.Unlock()
			if agent.forkCalls != 1 || agent.forkParams.SessionId != "parent" || agent.forkParams.Cwd != cfg.WorkspacePath ||
				agent.forkParams.Meta["systemPrompt"] != cfg.SystemPrompt || len(agent.forkParams.AdditionalDirectories) != 1 ||
				len(agent.forkParams.McpServers) != 3 || agent.forkParams.McpServers[0].Stdio.Env[0].Value != "side-host" {
				t.Fatalf("fork request: %+v", agent.forkParams)
			}
			if load && (agent.loadParams.SessionId != "child" || agent.loadCalls != 1 || agent.resumeCalls != 0) {
				t.Fatalf("load did not activate child: %+v", agent.loadParams)
			}
			if !load && (agent.resumeParams.SessionId != "child" || agent.resumeCalls != 1 || agent.loadCalls != 0) {
				t.Fatalf("resume did not activate child: %+v", agent.resumeParams)
			}
			if agent.newParams.Cwd != "" {
				t.Fatal("fork created a fresh session")
			}
		})
	}
}

func TestACPIsolatedForkRejectsMissingCapabilityAndHistoricalAnchor(t *testing.T) {
	agent := &fakeAgent{}
	driver := forkTestDriver(agent)
	for _, anchor := range []string{"historical-turn", ""} {
		if _, err := driver.ForkIntoHost(t.Context(), "owned:parent", anchor, ports.ChatStartConfig{WorkspacePath: t.TempDir()}); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
			t.Fatalf("unsupported fork: %v", err)
		}
	}
	if agent.forkCalls != 0 {
		t.Fatal("sent an unsupported fork request")
	}
}

func TestACPIsolatedForkRejectsInvalidChildAndCleansFailedAdoption(t *testing.T) {
	for _, childID := range []string{"", "parent", "child"} {
		t.Run(childID, func(t *testing.T) {
			agent := &fakeAgent{forkID: childID, failLoadFrom: 1, failLoadErr: errors.New("load failed"),
				capabilities: &acpsdk.AgentCapabilities{LoadSession: true,
					SessionCapabilities: acpsdk.SessionCapabilities{Fork: &acpsdk.SessionForkCapabilities{}, Delete: &acpsdk.SessionDeleteCapabilities{}}},
			}
			driver := forkTestDriver(agent)
			if _, err := driver.ForkIntoHost(t.Context(), "owned:parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir()}); err == nil {
				t.Fatal("invalid or unusable child accepted")
			}
			agent.mu.Lock()
			defer agent.mu.Unlock()
			want := 0
			if childID == "child" {
				want = 1
			}
			if len(agent.deletedForks) != want || (want == 1 && agent.deletedForks[0] != "child") {
				t.Fatalf("deleted forks: %v", agent.deletedForks)
			}
		})
	}
}

func TestACPReconnectForkUsesChildOwnershipWithoutForkingAgain(t *testing.T) {
	agent := &fakeAgent{}
	driver := forkTestDriver(agent)
	init, _ := json.Marshal(acpsdk.InitializeResponse{
		AgentCapabilities: acpsdk.AgentCapabilities{SessionCapabilities: acpsdk.SessionCapabilities{
			Fork: &acpsdk.SessionForkCapabilities{}, Resume: &acpsdk.SessionResumeCapabilities{},
		}},
	})
	driver.openProcess = func(context.Context, LaunchConfig, func(context.Context) (map[string]string, error)) (*process, error) {
		proc, err := fakeSpawn(agent)(Launch{}, "")
		proc.reconnected = true
		proc.acpState = &persistenthost.ACPState{
			SessionID: "child", InitializeResult: init, SessionResult: json.RawMessage(`{"sessionId":"child"}`),
		}
		return proc, err
	}
	cfg := ports.ChatResumeConfig{ProviderConversationID: "owned:child", WorkspacePath: t.TempDir()}
	child, err := driver.Resume(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = child.Close()
	if child.ProviderConversationID() != "owned:child" || agent.forkCalls != 0 || agent.resumeCalls != 0 || agent.loadCalls != 0 {
		t.Fatal("reconnect did not preserve the child")
	}
	if _, err := driver.ForkIntoHost(t.Context(), "owned:parent", "", ports.ChatStartConfig{WorkspacePath: cfg.WorkspacePath}); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("existing host collision: %v", err)
	}
}

func TestACPDeleteForkProtectsParentAndDecodesChild(t *testing.T) {
	agent := &fakeAgent{capabilities: &acpsdk.AgentCapabilities{
		SessionCapabilities: acpsdk.SessionCapabilities{Delete: &acpsdk.SessionDeleteCapabilities{}},
	}}
	driver := forkTestDriver(agent)
	parent, err := driver.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	deleter := parent.(ports.ChatForkDeleter)
	if err := deleter.DeleteFork(t.Context(), parent.ProviderConversationID()); err == nil {
		t.Fatal("parent deletion accepted")
	}
	if err := deleter.DeleteFork(t.Context(), "owned:child"); err != nil {
		t.Fatal(err)
	}
	if len(agent.deletedForks) != 1 || agent.deletedForks[0] != "child" {
		t.Fatalf("deletions: %v", agent.deletedForks)
	}
}

func TestACPForkReleasesSourceBeforeLoadingChild(t *testing.T) {
	var held atomic.Bool
	agent := &fakeAgent{forkID: "child", blockLoadCall: 1, loadStarted: make(chan struct{}), capabilities: &acpsdk.AgentCapabilities{
		LoadSession: true, SessionCapabilities: acpsdk.SessionCapabilities{Fork: &acpsdk.SessionForkCapabilities{}, Delete: &acpsdk.SessionDeleteCapabilities{}},
	}}
	agent.onFork = func(context.Context) error {
		if !held.Load() {
			return errors.New("source was not locked during copy")
		}
		return nil
	}
	driver := forkTestDriver(agent)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := driver.ForkIntoHost(ctx, "owned:parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir(), AcquireForkSource: func(ctx context.Context) (context.Context, func(), error) {
			agent.mu.Lock()
			initialized := agent.initParams.ProtocolVersion != 0 && agent.forkCalls == 0
			agent.mu.Unlock()
			if !initialized {
				return nil, nil, errors.New("source locked before initialize")
			}
			held.Store(true)
			return ctx, func() { held.Store(false) }, nil
		}})
		done <- err
	}()
	select {
	case <-agent.loadStarted:
	case err := <-done:
		t.Fatalf("child load did not start: %v", err)
	case <-time.After(time.Second):
		t.Fatal("child load did not start")
	}
	if held.Load() {
		t.Fatal("source still locked while loading child")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled load succeeded")
	}
}

func TestACPForkCancellationDeletesReturnedChild(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	agent := &fakeAgent{forkID: "child", capabilities: &acpsdk.AgentCapabilities{
		LoadSession: true, SessionCapabilities: acpsdk.SessionCapabilities{Fork: &acpsdk.SessionForkCapabilities{}, Delete: &acpsdk.SessionDeleteCapabilities{}},
	}}
	driver := forkTestDriver(agent)
	_, err := driver.ForkIntoHost(ctx, "owned:parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir(), RecordFork: func(ctx context.Context, id string) error {
		if id != "owned:child" {
			t.Fatalf("returned child = %s", id)
		}
		cancel()
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.deletedForks) != 1 || agent.deletedForks[0] != "child" || agent.loadCalls != 0 {
		t.Fatalf("cancelled fork cleanup: %v; loads = %d", agent.deletedForks, agent.loadCalls)
	}
}

func TestACPForkLostResponseIsInconclusive(t *testing.T) {
	agent := &fakeAgent{forkID: "child", capabilities: &acpsdk.AgentCapabilities{
		LoadSession: true, SessionCapabilities: acpsdk.SessionCapabilities{Fork: &acpsdk.SessionForkCapabilities{}},
	}}
	driver := forkTestDriver(agent)
	spawn := fakeSpawn(agent)
	var disconnect func() error
	driver.useTestProcess(func(launch Launch, workdir string) (*process, error) {
		proc, err := spawn(launch, workdir)
		if err == nil {
			disconnect = proc.stop
		}
		return proc, err
	})
	agent.onFork = func(context.Context) error { return disconnect() }
	_, err := driver.ForkIntoHost(t.Context(), "owned:parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("unknown child reported as ordinary failure: %v", err)
	}
}

func TestACPHungForkCancellationTerminatesHostBeforeUnlockingSource(t *testing.T) {
	agent := &fakeAgent{forkID: "child", capabilities: &acpsdk.AgentCapabilities{LoadSession: true, SessionCapabilities: acpsdk.SessionCapabilities{Fork: &acpsdk.SessionForkCapabilities{}}}}
	started := make(chan struct{})
	agent.onFork = func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
	driver := forkTestDriver(agent)
	spawn := fakeSpawn(agent)
	var terminated atomic.Bool
	driver.useTestProcess(func(launch Launch, workdir string) (*process, error) {
		proc, err := spawn(launch, workdir)
		if err != nil {
			return nil, err
		}
		stop := proc.stop
		proc.stop = func() error { terminated.Store(true); return stop() }
		return proc, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := driver.ForkIntoHost(ctx, "owned:parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir(), AcquireForkSource: func(ctx context.Context) (context.Context, func(), error) {
			return ctx, func() {
				if !terminated.Load() {
					t.Error("source unlocked before destination terminated")
				}
			}, nil
		}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fork not sent")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation waited for handshake timeout")
	}
}

func TestACPForkRecordsChildBeforeLoad(t *testing.T) {
	agent := &fakeAgent{forkID: "child", capabilities: &acpsdk.AgentCapabilities{LoadSession: true, SessionCapabilities: acpsdk.SessionCapabilities{Fork: &acpsdk.SessionForkCapabilities{}}}, loadStarted: make(chan struct{}), blockLoadCall: 1}
	driver := forkTestDriver(agent)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recorded, done := make(chan string, 1), make(chan error, 1)
	go func() {
		_, err := driver.ForkIntoHost(ctx, "owned:parent", "", ports.ChatStartConfig{WorkspacePath: t.TempDir(), RecordFork: func(ctx context.Context, id string) error { recorded <- id; return nil }})
		done <- err
	}()
	select {
	case <-agent.loadStarted:
	case <-time.After(time.Second):
		t.Fatal("load not started")
	}
	select {
	case id := <-recorded:
		if id != "owned:child" {
			t.Fatalf("recorded %s", id)
		}
	default:
		t.Fatal("child load preceded ownership record")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled load succeeded")
	}
}
