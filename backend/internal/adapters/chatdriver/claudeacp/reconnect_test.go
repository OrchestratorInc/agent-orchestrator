package claudeacp

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type reconnectPlugin struct{}

func (reconnectPlugin) ResolveBinary(context.Context) (string, error) { return "claude", nil }

// The startup health check reconnects through ports.ChatDriverReconnector. A
// wrapper that hides it makes every live Claude chat look stopped after a
// daemon restart.
func TestDriverExposesReconnect(t *testing.T) {
	d := New(reconnectPlugin{}, nil, nil)
	if _, ok := d.(ports.ChatDriverReconnector); !ok {
		t.Fatalf("%T does not implement ports.ChatDriverReconnector", d)
	}
}

func TestReconnectWithoutHostReportsNotRunning(t *testing.T) {
	d := New(reconnectPlugin{}, nil, nil).(ports.ChatDriverReconnector)
	_, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "stopped", ProviderConversationID: "native", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
	})
	if err == nil || !isNotRunning(err) {
		t.Fatalf("err = %v, want ErrChatHostNotRunning", err)
	}
}

func isNotRunning(err error) bool { return errors.Is(err, ports.ErrChatHostNotRunning) }
