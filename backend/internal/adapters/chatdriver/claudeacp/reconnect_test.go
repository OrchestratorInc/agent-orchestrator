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
	if _, ok := d.(ports.ChatIsolatedForker); !ok {
		t.Fatalf("%T does not implement ports.ChatIsolatedForker", d)
	}
}

type forkRecorder struct {
	ports.ChatDriver
	source, anchor string
	cfg            ports.ChatStartConfig
}

func (d *forkRecorder) ForkIntoHost(_ context.Context, source, anchor string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.source, d.anchor, d.cfg = source, anchor, cfg
	return nil, ports.ErrChatAuthRequired
}

func TestDriverForwardsIsolatedForkIdentityAndPolicy(t *testing.T) {
	base := &forkRecorder{}
	driver := &checkpointDriver{ChatDriver: base}
	cfg := ports.ChatStartConfig{SessionID: "side", WorkspacePath: t.TempDir(), ProviderScopeID: "side-scope", Permissions: ports.PermissionModeAcceptEdits}
	if _, err := driver.ForkIntoHost(t.Context(), "parent", "", cfg); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("fork error was lost: %v", err)
	}
	if base.source != "parent" || base.anchor != "" || base.cfg.ProviderScopeID != cfg.ProviderScopeID || base.cfg.SessionID != cfg.SessionID || base.cfg.Permissions != cfg.Permissions {
		t.Fatalf("fork configuration was lost: %+v", base)
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
