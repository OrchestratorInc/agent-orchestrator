package activity

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/mastracode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type renderedRuntime struct {
	fakeRuntime
	viewport string
}

func (r *renderedRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return r.viewport, r.err
}

func TestMastraActivityUsesCurrentViewportNeverOldRing(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	for _, available := range []bool{false, true} {
		raw := &fakeRuntime{output: "Tool Approval Required\n│ • Working"}
		var runtime outputReader = raw
		if available {
			runtime = &renderedRuntime{fakeRuntime: *raw, viewport: "╭────────╮\n│ ›      │\n╰────────╯\n▐build▌model▌"}
		}
		sink := &fakeSink{}
		o := New(fakeSessions{rows: []domain.SessionRecord{activeSession(now, domain.HarnessMastraCode)}}, sink, runtime, fakeAgents{domain.HarnessMastraCode: mastracode.New()}, Config{Clock: func() time.Time { return now }, Logger: testLogger()})
		if err := o.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		if available {
			if len(sink.signals) != 1 || sink.signals[0].State != domain.ActivityIdle {
				t.Fatalf("current empty composer did not settle: %+v", sink.signals)
			}
		} else if len(sink.signals) != 0 || raw.calls != 0 {
			t.Fatal("unavailable rendered viewport fell back to old ring text")
		}
	}
}
