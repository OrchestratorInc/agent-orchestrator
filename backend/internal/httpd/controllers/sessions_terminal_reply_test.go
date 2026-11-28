package controllers

import (
	"context"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type replyAgent struct {
	ports.Agent
	got   ports.TerminalReplyConfig
	reply string
}

func (a *replyAgent) TerminalTurnReply(_ context.Context, cfg ports.TerminalReplyConfig) string {
	a.got = cfg
	return a.reply
}

type replyResolver map[domain.AgentHarness]ports.Agent

func (r replyResolver) Agent(h domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := r[h]
	return agent, ok
}

// A Stop without a reply is filled from the harness's own native history,
// located by AO's data dir and session, and only for adapters that read it.
func TestTerminalTurnReplyAsksOnlyAReplyReader(t *testing.T) {
	reader := &replyAgent{reply: "  DONE\x07  "}
	c := &SessionsController{DataDir: "/ao", TerminalReplies: replyResolver{
		domain.HarnessMastraCode: reader,
		domain.HarnessCodex:      struct{ ports.Agent }{},
	}}
	if got := c.terminalTurnReply(context.Background(), "mc-1", domain.HarnessMastraCode, "thread-1"); got != "DONE" {
		t.Fatalf("reply = %q, want the sanitized native reply", got)
	}
	if reader.got != (ports.TerminalReplyConfig{DataDir: "/ao", SessionID: "mc-1", NativeSessionID: "thread-1"}) {
		t.Fatalf("reader config = %+v", reader.got)
	}
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, "unknown", ""} {
		if got := c.terminalTurnReply(context.Background(), "mc-1", harness, ""); got != "" {
			t.Fatalf("%q without a reply reader returned %q", harness, got)
		}
	}
	if got := (&SessionsController{DataDir: "/ao"}).terminalTurnReply(context.Background(), "mc-1", domain.HarnessMastraCode, ""); got != "" {
		t.Fatalf("no resolver returned %q", got)
	}
}
