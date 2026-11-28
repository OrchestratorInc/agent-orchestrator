package sessionmanager

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type agyImportBridgeAgent struct {
	transitionAgent
	env      chan map[string]string
	probeEnv map[string]string
}

func (a *agyImportBridgeAgent) NativeConversationExists(_ context.Context, _ ports.SessionRef, _ string, env map[string]string) (bool, error) {
	a.probeEnv = maps.Clone(env)
	return true, nil
}

func (a *agyImportBridgeAgent) PrepareInterfaceHandoffStorage(_ context.Context, _ ports.SessionRef, _, _ domain.SessionMode, _ string, env map[string]string) error {
	a.env <- maps.Clone(env)
	return errors.New("test stops after observing bridge storage")
}

func TestAgyHandoffBridgeUsesPreparedImportHome(t *testing.T) {
	for _, imported := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "imported"}[imported], func(t *testing.T) {
			manager, store, _, _, _ := newTransitionManager(t, domain.SessionModeChat)
			useFastInterfaceTransitionTimings(manager)
			original, prepared := t.TempDir(), t.TempDir()
			store.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: "/repo", Config: domain.ProjectConfig{Env: map[string]string{"GEMINI_HOME": original}}}
			rec := store.sessions["session-1"]
			rec.Harness = domain.HarnessAgy
			if imported {
				rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native-1", ConfigDir: original, ResumeNativeID: "native-1", ResumeConfigDir: prepared, Adopted: true}
			}
			store.sessions[rec.ID] = rec
			agent := &agyImportBridgeAgent{env: make(chan map[string]string, 1)}
			manager.agents = singleAgent{agent: agent}
			transition, err := manager.StartInterfaceTransition(context.Background(), rec.ID, domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
			if err != nil {
				t.Fatal(err)
			}
			settled := awaitTransition(t, store, transition.ID)
			if settled.ErrorCode != "HANDOFF_STORAGE_BRIDGE_FAILED" {
				t.Fatalf("transition = %+v", settled)
			}
			want := original
			if imported {
				want = prepared
			}
			if agent.probeEnv["GEMINI_HOME"] != want {
				t.Fatalf("readiness home = %q, want %q", agent.probeEnv["GEMINI_HOME"], want)
			}
			select {
			case env := <-agent.env:
				want := original
				if imported {
					want = prepared
				}
				if env["GEMINI_HOME"] != want {
					t.Fatalf("bridge home = %q, want %q", env["GEMINI_HOME"], want)
				}
			default:
				t.Fatal("bridge was not reached")
			}
		})
	}
}
