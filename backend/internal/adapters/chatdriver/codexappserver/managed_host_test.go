package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 9 && os.Args[1] == "chat-host" && os.Args[7] == "--" {
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Protocol: persistenthost.Protocol(os.Args[5]), OwnershipFingerprint: os.Args[6],
			Argv: os.Args[8:], Env: os.Environ(),
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("AO_MANAGED_CHAT_TEST_PROVIDER") == "1" && slices.Contains(os.Args, "app-server") {
		managedProviderFixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func managedProviderFixture() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		result := `{}`
		switch request.Method {
		case "initialize":
			result = `{"userAgent":"test-provider"}`
		case "thread/start", "thread/resume":
			result = `{"thread":{"id":"managed-thread"},"model":"managed-model","reasoningEffort":"medium"}`
		case "model/list":
			result = `{"data":[],"nextCursor":null}`
		}
		_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%s,\"result\":%s}\n", request.ID, result)
	}
}

func TestManagedChatProductionHostLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := NewManaged(fakePlugin{bin: bin}, nil)
	cfg := managedTestConfig(t)
	cfg.Route.BindingRevision = 3
	cfg.Env["AO_MANAGED_CHAT_TEST_PROVIDER"] = "1"
	first, err := d.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), cfg.DataDir, string(cfg.SessionID)) })
	identity := first.(*managedConversation).HostIdentity()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	prepared := 0
	resume := ports.ChatResumeConfig{
		SessionID: cfg.SessionID, ControllerGeneration: "replacement-controller", DataDir: cfg.DataDir,
		WorkspacePath: cfg.WorkspacePath, ProviderConversationID: "managed-thread", ProviderScopeID: cfg.ProviderScopeID,
		Env: cfg.Env, Route: cfg.Route,
		PrepareEnv: func(context.Context) (map[string]string, error) { prepared++; return cfg.Env, nil },
	}
	second, err := d.Resume(ctx, resume)
	if err != nil {
		t.Fatal("reattach after controller replacement", err)
	}
	if second.(*managedConversation).HostIdentity() != identity || !second.(ports.ChatLiveReconnector).ReconnectedLive() || prepared != 0 {
		t.Fatal("reattach replaced the provider or its authorization")
	}
	if _, err := second.(ports.ChatModelLister).ListModels(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal("ordinary host stop", err)
	}

	third, err := d.Resume(ctx, resume)
	if err != nil {
		t.Fatal("cold history resume", err)
	}
	defer func() { _ = third.(ports.ChatProviderTerminator).Terminate() }()
	if third.(ports.ChatLiveReconnector).ReconnectedLive() || third.(*managedConversation).HostIdentity() == identity || prepared != 1 {
		t.Fatal("cold resume reused a retired owner or skipped authorization refresh")
	}
	if err := persistenthost.ShutdownHost(ctx, cfg.DataDir, string(cfg.SessionID), identity); err != nil {
		t.Fatal("completed stop retry lost its receipt", err)
	}
	if err := persistenthost.ShutdownHost(ctx, cfg.DataDir, string(cfg.SessionID), strings.Repeat("f", 64)); !errors.Is(err, persistenthost.ErrOwnershipInconclusive) {
		t.Fatal("foreign identity admitted teardown", err)
	}
	if _, err := third.(ports.ChatModelLister).ListModels(ctx); err != nil {
		t.Fatal("replacement did not survive", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	changed := *cfg.Route
	changed.BindingRevision++
	resume.Route = &changed
	if _, err := d.Resume(ctx, resume); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatal("changed binding adopted the old provider", err)
	}
}
