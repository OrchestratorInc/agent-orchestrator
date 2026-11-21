package codexappserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCodexReconnectStatesPlanModeOnTheNextTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("initialize", -32600, "Already initialized")
	proc, err := d.spawn(context.Background(), "codex", "/tmp/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	d.persistent = true
	d.connectHost = func(_ context.Context, _ persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41}, nil
	}
	resumed, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "plan-reconnect", ProviderConversationID: "thread-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Model: "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if _, err := resumed.SendTurn(context.Background(), ports.ChatUserMessage{Text: "Implement the plan", Settings: ports.ChatTurnSettings{Model: "gpt-test", Plan: false}}); err != nil {
		t.Fatal(err)
	}
	request := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params struct {
		CollaborationMode *struct {
			Mode string `json:"mode"`
		} `json:"collaborationMode"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.CollaborationMode == nil || params.CollaborationMode.Mode != "default" {
		t.Fatalf("Plan=false after reconnect did not clear sticky plan mode: collaborationMode=%s", request.Params)
	}
}

func TestCodexReconnectLeavesAFastTierTheThreadMayStillBeOn(t *testing.T) {
	for _, tt := range []struct {
		name, config string
		wantNull     bool
	}{
		{"no configured tier", `{"config":{}}`, true},
		{"the user's configured tier", `{"config":{"service_tier":"fast"}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.replyError("initialize", -32600, "Already initialized")
			srv.respondTo("config/read", tt.config)
			proc, err := d.spawn(context.Background(), "codex", "/tmp/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			d.persistent = true
			d.connectHost = func(_ context.Context, _ persistenthost.Config) (*persistenthost.Transport, error) {
				return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41}, nil
			}
			resumed, err := d.Resume(context.Background(), ports.ChatResumeConfig{
				SessionID: "fast-reconnect", ProviderConversationID: "thread-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Model: "gpt-test",
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resumed.Close() }()
			if _, err := resumed.SendTurn(context.Background(), ports.ChatUserMessage{Text: "not fast", Settings: ports.ChatTurnSettings{Model: "gpt-test"}}); err != nil {
				t.Fatal(err)
			}
			request := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
			var params map[string]json.RawMessage
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			tier, sent := params["serviceTier"]
			if tt.wantNull && (!sent || string(tier) != "null") {
				t.Fatalf("Fast off after reconnect sent serviceTier=%s (sent %v), want an explicit null", tier, sent)
			}
			if !tt.wantNull && sent {
				t.Fatalf("serviceTier = %s, want it left to the user's config", tier)
			}
		})
	}
}

func TestCodexColdResumeStatesPlanModeOnTheNextTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo("thread/resume", `{"thread":{"id":"thread-1"},"model":"gpt-test","reasoningEffort":"low"}`)
	resumed, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "cold-plan", ProviderConversationID: "thread-1", WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Model: "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if _, err := resumed.SendTurn(context.Background(), ports.ChatUserMessage{Text: "Implement the plan", Settings: ports.ChatTurnSettings{Model: "gpt-test", Plan: false}}); err != nil {
		t.Fatal(err)
	}
	request := srv.awaitFrame(func(f frame) bool { return f.Method == "turn/start" })
	var params struct {
		CollaborationMode *struct {
			Mode string `json:"mode"`
		} `json:"collaborationMode"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.CollaborationMode == nil || params.CollaborationMode.Mode != "default" {
		t.Fatalf("Plan=false after a cold resume did not clear a persisted plan mode: %s", request.Params)
	}
}
