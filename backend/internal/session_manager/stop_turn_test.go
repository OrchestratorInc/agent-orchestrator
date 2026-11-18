package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type stopKeyAgent struct {
	fakeAgent
	key string
}

func (a stopKeyAgent) InterruptKey() string { return a.key }

func TestStopTUITurnUsesHarnessKey(t *testing.T) {
	for _, key := range []string{"\x03", "\x1b", ""} {
		t.Run(key, func(t *testing.T) {
			m, st, rt, _ := newManager()
			st.sessions["s1"] = draftProbeRecord(domain.ActivityActive)
			m.agents = singleAgent{agent: stopKeyAgent{key: key}}
			err := m.StopTUITurn(context.Background(), "s1")
			if key == "" {
				if !errors.Is(err, ErrInterruptUnsupported) || len(rt.interrupts) != 0 {
					t.Fatalf("undeclared interrupt: err=%v writes=%v", err, rt.interrupts)
				}
				return
			}
			if err != nil || len(rt.inputs) != 1 || rt.inputs[0] != key {
				t.Fatalf("interrupt: err=%v keys=%q, want %q", err, rt.inputs, key)
			}
		})
	}
}

func TestStopTUITurnPreservesDraftAndAllowsRetry(t *testing.T) {
	probe, rt, _ := draftProbeManager(transitionSurfaceAgent{}, draftTerminalOutput)
	log := []string{}
	rt.log = &log
	m, st, _, _ := newManager()
	m.agents, m.runtime, m.interfaceTransition = probe.agents, rt, fastComposerProbe
	st.sessions["s1"] = draftProbeRecord(domain.ActivityActive)
	m.store = st
	if err := m.StopTUITurn(context.Background(), "s1"); !errors.Is(err, ErrComposerBusy) {
		t.Fatalf("draft refusal = %v, want ErrComposerBusy", err)
	}
	if len(rt.interrupts) != 0 {
		t.Fatalf("draft received interrupt: %v", rt.interrupts)
	}
	rt.outputForCall = func(int) string { return idleTerminalOutput }
	if err := m.StopTUITurn(context.Background(), "s1"); err != nil {
		t.Fatalf("retry after clearing draft: %v", err)
	}
	if len(rt.inputs) != 1 {
		t.Fatalf("interrupt writes = %q, want one", rt.inputs)
	}
}
