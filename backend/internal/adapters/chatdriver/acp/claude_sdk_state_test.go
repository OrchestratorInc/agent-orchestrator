package acp

import (
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Claude Code's own running/idle report becomes provider.state, so a cycle the
// agent starts without an ACP prompt still shows as work.
func TestClaudeSDKSessionStateBecomesProviderState(t *testing.T) {
	conv := &conversation{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessionID: "claude-session",
		events:    make(chan ports.ChatEvent, 8),
	}
	notify := func(params string) {
		t.Helper()
		if !conv.handleNotification(claudeSDKMessageMethod, []byte(params)) {
			t.Fatalf("%s was not handled", claudeSDKMessageMethod)
		}
	}

	notify(`{"sessionId":"claude-session","message":{"type":"system","subtype":"session_state_changed","state":"running"},"_meta":{"` +
		persistenthost.ACPEventIDMetaKey + `":"ev-1"}}`)
	notify(`{"sessionId":"claude-session","message":{"type":"system","subtype":"session_state_changed","state":"idle"}}`)
	// Ignored: an approval reports requires_action itself, other sessions and
	// other SDK messages say nothing about this conversation's work.
	notify(`{"sessionId":"claude-session","message":{"type":"system","subtype":"session_state_changed","state":"requires_action"}}`)
	notify(`{"sessionId":"other-session","message":{"type":"system","subtype":"session_state_changed","state":"running"}}`)
	notify(`{"sessionId":"claude-session","message":{"type":"assistant"}}`)

	want := []struct {
		status  domain.ThreadStatus
		eventID string
	}{{domain.ThreadStatusActive, "ev-1:0"}, {domain.ThreadStatusIdle, ""}}
	for _, w := range want {
		select {
		case event := <-conv.events:
			if event.Kind != ports.ChatEventProviderState || event.ThreadState == nil ||
				event.ThreadState.Status != w.status || event.ProviderEventID != w.eventID {
				t.Fatalf("event = %+v (state %+v), want provider.state %s with id %q", event, event.ThreadState, w.status, w.eventID)
			}
		default:
			t.Fatalf("missing provider.state %s", w.status)
		}
	}
	select {
	case event := <-conv.events:
		t.Fatalf("unexpected event %+v", event)
	default:
	}
}
