package chat_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const acpReplayFrames = 20543

// The test binary doubles as the detached host, as in the ACP driver's tests.
func TestMain(m *testing.M) {
	if len(os.Args) >= 8 && os.Args[1] == "chat-host" {
		if os.Args[5] != string(persistenthost.ProtocolACP) || os.Args[7] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[8:], Protocol: persistenthost.ProtocolACP,
			OwnershipFingerprint: os.Args[6],
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestACPReplayProviderHelper(t *testing.T) {
	if os.Getenv("AO_TEST_CHAT_ACP_REPLAY") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		switch request.Method {
		case "initialize":
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}},"authMethods":[]}}`+"\n", request.ID)
		case "session/new":
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"replay-provider"}}`+"\n", request.ID)
		case "session/prompt":
			_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"replay-provider","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"~"}}}}`)
			deadline := time.Now().Add(time.Minute)
			for {
				if _, err := os.Stat(os.Getenv("AO_TEST_CHAT_ACP_RELEASE")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(2)
				}
				time.Sleep(time.Millisecond)
			}
			for range acpReplayFrames {
				if _, err := fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"replay-provider","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}}`); err != nil {
					os.Exit(1)
				}
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", request.ID)
		}
	}
	os.Exit(0)
}

func TestACPReplayProjectsExactlyOnceAcrossDaemonRestarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st := openStore(t)
	dataDir, workspace := t.TempDir(), t.TempDir()
	logPath := filepath.Join(dataDir, "acp.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := os.ReadFile(logPath)
			t.Logf("ACP diagnostics:\n%s", logs)
		}
		_ = logFile.Close()
	})
	logger := slog.New(slog.NewTextHandler(logFile, nil))
	releasePath := filepath.Join(dataDir, "release")
	cfg := acp.Config{
		Harness: domain.HarnessOpenCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true,
			ports.ChatCapabilityApprovals: true, ports.ChatCapabilityInterrupt: true,
		},
		Probe: func(context.Context) error { return nil },
		Launch: func(context.Context, acp.LaunchConfig) (acp.Launch, error) {
			return acp.Launch{
				Command: os.Args[0], Args: []string{"-test.run=^TestACPReplayProviderHelper$"},
				Env: map[string]string{"AO_TEST_CHAT_ACP_REPLAY": "1", "AO_TEST_CHAT_ACP_RELEASE": releasePath},
			}, nil
		},
	}
	var ids atomic.Uint64
	newService := func() *chatsvc.Service {
		svc := chatsvc.New(chatsvc.Options{
			Store: st, Reader: fullSnapshotReader(st), Sessions: st,
			Drivers: fakeRegistry{driver: acp.New(cfg, logger)}, Activity: &recordingActivity{}, Log: logger,
			NewID: func() string { return fmt.Sprintf("replay-%d", ids.Add(1)) }, DataDir: dataDir,
		})
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			svc.StopAll(stopCtx)
		})
		return svc
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer stopCancel()
		if err := persistenthost.Shutdown(stopCtx, dataDir, string(testSession)); err != nil {
			t.Errorf("host cleanup: %v", err)
		}
	})
	firstService := newService()
	start := chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessOpenCode, DataDir: dataDir, WorkspacePath: workspace,
	}
	first, err := firstService.Start(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	hostBefore := readACPReplayHost(t, dataDir)
	turn, err := first.Send(ctx, ports.ChatUserMessage{Text: "burst while AO is detached"})
	if err != nil {
		t.Fatal(err)
	}
	awaitACPReplay(ctx, t, "initial output projected", func() bool {
		rows, readErr := st.LoadConversationSnapshot(ctx, first.ConversationID())
		if readErr != nil {
			t.Fatal(readErr)
		}
		return len(rows.Turns) == 1 && rows.Turns[0].State == domain.TurnStateRunning &&
			len(rows.Messages) == 2 && rows.Messages[1].Text == "~"
	})
	stopCtx, stopCancel := context.WithTimeout(ctx, 5*time.Second)
	firstService.StopAll(stopCtx)
	stopCancel()
	if err := os.WriteFile(releasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(dataDir, "chat-hosts", string(testSession), "acp-prompt.journal")
	awaitACPReplay(ctx, t, "detached burst journaled", func() bool {
		content, readErr := os.ReadFile(journal)
		return readErr == nil && bytes.Count(content, []byte{'\n'}) == acpReplayFrames+1
	})
	t.Logf("host journaled %d detached updates plus one already projected seed", acpReplayFrames)
	cfg.Launch = func(context.Context, acp.LaunchConfig) (acp.Launch, error) {
		return acp.Launch{}, errors.New("replacement launch disabled: must adopt same host")
	}
	start.ProviderConversationID = first.ProviderConversationID()
	start.ReconnectOnly = true
	secondService := newService()
	second, err := secondService.Start(ctx, start)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	hostAfter := readACPReplayHost(t, dataDir)
	if hostAfter.PID != hostBefore.PID || !hostAfter.StartedAt.Equal(hostBefore.StartedAt) ||
		second.ProviderConversationID() != first.ProviderConversationID() {
		t.Fatal("reconnect replaced the native provider")
	}
	// The real SQLite projector consumes immediately; replay naturally outruns it.
	awaitACPReplay(ctx, t, "replay completion committed", func() bool {
		logs, _ := os.ReadFile(logPath)
		if bytes.Contains(logs, []byte("notification queue overflow")) {
			t.Fatal("ACP replay overflowed while the real SQLite projector was consuming")
		}
		rows, readErr := st.LoadConversationSnapshot(ctx, second.ConversationID())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if second.State() == ports.ChatControllerStopped {
			t.Fatalf("replay stopped before completion: %+v", rows.Turns)
		}
		return len(rows.Turns) == 1 && rows.Turns[0].State == domain.TurnStateCompleted
	})
	rows, err := st.LoadConversationSnapshot(ctx, second.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	expected := "~" + strings.Repeat("x", acpReplayFrames)
	if rows.Turns[0].ProviderTurnID != turn.ProviderTurnID || len(rows.Messages) != 2 ||
		rows.Messages[1].Text != expected || rows.Messages[1].Streaming {
		t.Fatal("replay changed the turn or lost/duplicated content")
	}
	archive, err := st.ProviderEventsSince(ctx, second.ConversationID(), 0, acpReplayFrames+100)
	if err != nil {
		t.Fatal(err)
	}
	deltas, completions := 0, 0
	for _, event := range archive {
		switch event.Method {
		case string(ports.ChatEventMessageDelta):
			deltas++
		case string(ports.ChatEventTurnCompleted):
			completions++
		}
	}
	if deltas != acpReplayFrames+1 || completions != 1 {
		t.Fatalf("durable archive has %d deltas and %d completions", deltas, completions)
	}
	awaitACPReplay(ctx, t, "terminal ACK cleared the host journal", func() bool {
		info, statErr := os.Stat(journal)
		return statErr == nil && info.Size() == 0
	})
	t.Logf("same host committed exactly %d unique deltas, %d assistant bytes and one completion; terminal ACK cleared journal", deltas, len(expected))
	stopCtx, stopCancel = context.WithTimeout(ctx, 5*time.Second)
	secondService.StopAll(stopCtx)
	stopCancel()
	third, err := newService().Start(ctx, start)
	if err != nil {
		t.Fatalf("restart after completion ACK: %v", err)
	}
	if third.State() != ports.ChatControllerReady || third.ProviderConversationID() != first.ProviderConversationID() {
		t.Fatal("restart after completion ACK did not retain a ready native conversation")
	}
	t.Log("third daemon-style restart reattached ready to the same native conversation")
}

func awaitACPReplay(ctx context.Context, t *testing.T, stage string, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %s", stage)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func readACPReplayHost(t *testing.T, dataDir string) persistenthost.Descriptor {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "chat-hosts", string(testSession), "host.json"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor persistenthost.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	return descriptor
}
