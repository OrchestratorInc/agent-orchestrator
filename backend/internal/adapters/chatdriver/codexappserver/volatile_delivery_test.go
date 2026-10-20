package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDeliveryPolicySeparatesVolatileConsumersFromDurableControllers(t *testing.T) {
	for _, operation := range []string{"start", "resume", "fork", "reconnect"} {
		for _, volatile := range []bool{false, true} {
			name := operation + "/durable"
			if volatile {
				name = operation + "/volatile"
			}
			t.Run(name, func(t *testing.T) {
				driver, server := newTestDriver(t)
				server.reply("thread/fork", `{"thread":{"id":"child"}}`)
				proc, err := driver.spawn(t.Context(), "codex", "/tmp/ws", nil)
				if err != nil {
					t.Fatal(err)
				}
				driver.persistent = true
				driver.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
					want := persistenthost.ProtocolCodex
					if volatile {
						want = persistenthost.ProtocolRaw
					}
					if cfg.Protocol != want {
						t.Fatalf("delivery protocol = %q, want %q", cfg.Protocol, want)
					}
					return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout,
						CodexReplay: !volatile, Reconnected: operation == "reconnect"}, nil
				}
				start := ports.ChatStartConfig{SessionID: "consumer", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), VolatileEvents: volatile}
				resume := ports.ChatResumeConfig{SessionID: start.SessionID, DataDir: start.DataDir,
					WorkspacePath: start.WorkspacePath, ProviderConversationID: "thread-1", VolatileEvents: volatile}
				var conv ports.ChatConversation
				switch operation {
				case "start":
					conv, err = driver.Start(t.Context(), start)
				case "resume":
					conv, err = driver.Resume(t.Context(), resume)
				case "fork":
					conv, err = driver.ForkIntoHost(t.Context(), "parent", "anchor", start)
				case "reconnect":
					conv, err = driver.Reconnect(t.Context(), resume)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = conv.Close() })
				if volatile && server.sentMethod(persistenthost.CodexReplayMethod) {
					t.Fatal("RAM-only consumer entered durable replay")
				}
				if operation == "start" {
					request := server.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
					if strings.Contains(string(request.Params), `"ephemeral"`) {
						t.Fatal("event delivery policy changed native history persistence")
					}
				}
			})
		}
	}
}

func TestVolatileConsumerDoesNotDowngradeExistingDurableHost(t *testing.T) {
	driver, _ := newTestDriver(t)
	driver.persistent = true
	driver.connectHost = persistenthost.ConnectOrStart
	dataDir := t.TempDir()
	hostDir := filepath.Join(dataDir, "chat-hosts", "side")
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := json.Marshal(persistenthost.Descriptor{Version: persistenthost.ProtocolVersion,
		SessionID: "side", PID: os.Getpid(), Address: "127.0.0.1:1", Token: "test-token", Protocol: persistenthost.ProtocolCodex})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hostDir, "host.json")
	if err := os.WriteFile(path, descriptor, 0o600); err != nil {
		t.Fatal(err)
	}
	conv, err := driver.Resume(t.Context(), ports.ChatResumeConfig{SessionID: "side", DataDir: dataDir,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread", VolatileEvents: true})
	if conv != nil || !errors.Is(err, ports.ErrChatRecoveryInconclusive) || !errors.Is(err, persistenthost.ErrIncompatible) || !strings.Contains(err.Error(), "close and reopen") {
		t.Fatalf("existing durable host was not preserved with an actionable mismatch: conversation=%v error=%v", conv, err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != string(descriptor) {
		t.Fatalf("mismatched host descriptor changed: %s, %v", after, readErr)
	}
}
