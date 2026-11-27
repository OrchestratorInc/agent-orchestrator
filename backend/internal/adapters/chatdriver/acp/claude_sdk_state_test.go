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

func TestClaudeStartMessageReportsItsToolServers(t *testing.T) {
	conv := &conversation{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessionID: "claude-session",
		events:    make(chan ports.ChatEvent, 8),
	}
	start := `{"sessionId":"claude-session","message":{"type":"system","subtype":"init","mcp_servers":[` +
		`{"name":"ao","status":"connected"},{"name":"claude_agent_acp","status":"connected"},{"name":"linear","status":"pending"},` +
		`{"name":"claude.ai Notion","status":"needs-auth"},{"name":"plugin:docs:docs","status":"failed"}]}}`
	for _, params := range []string{start, start, `{"sessionId":"other-session","message":{"type":"system","subtype":"init","mcp_servers":[{"name":"x","status":"connected"}]}}`} {
		if !conv.handleNotification(claudeSDKMessageMethod, []byte(params)) {
			t.Fatalf("%s was not handled", claudeSDKMessageMethod)
		}
	}
	event := <-conv.events
	want := []ports.ChatMCPServer{{Name: "ao", Status: "ready"}, {Name: "linear", Status: "starting"}, {Name: "claude.ai Notion", Status: "needs-auth"}, {Name: "plugin:docs:docs", Status: "failed"}}
	if event.Kind != ports.ChatEventMCPServers || len(event.MCPServers) != len(want) {
		t.Fatalf("event = %+v", event)
	}
	for i, server := range event.MCPServers {
		if server != want[i] {
			t.Fatalf("server %d = %+v, want %+v", i, server, want[i])
		}
	}
	select {
	case again := <-conv.events:
		t.Fatalf("an unchanged list or another session's list was reported: %+v", again)
	default:
	}
	settled := `{"sessionId":"claude-session","message":{"type":"system","subtype":"init","mcp_servers":[{"name":"ao","status":"connected"},{"name":"linear","status":"connected"}]}}`
	conv.handleNotification(claudeSDKMessageMethod, []byte(settled))
	later := []ports.ChatMCPServer{{Name: "linear", Status: "ready"}, {Name: "claude.ai Notion", Status: "disabled"}, {Name: "plugin:docs:docs", Status: "disabled"}}
	event = <-conv.events
	if len(event.MCPServers) != len(later) {
		t.Fatalf("a changed list = %+v", event)
	}
	for i, server := range event.MCPServers {
		if server != later[i] {
			t.Fatalf("change %d = %+v, want %+v", i, server, later[i])
		}
	}
	conv.handleNotification(claudeSDKMessageMethod, []byte(`{"sessionId":"claude-session","message":{"type":"system","subtype":"init","mcp_servers":[{"name":"claude_agent_acp","status":"connected"}]}}`))
	if event := <-conv.events; len(event.MCPServers) != 2 || event.MCPServers[0].Status != "disabled" || event.MCPServers[1].Status != "disabled" {
		t.Fatalf("an empty list = %+v", event)
	}
}
