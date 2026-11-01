package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/zcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Public registration is deferred instead of letting every normal role reach
// the adapter's instruction guard after creating a session and workspace.
func TestSpawnRejectsDeferredZCodeBeforeCreatingState(t *testing.T) {
	cases := map[string]ports.SpawnConfig{
		"worker":            {ProjectID: "mer", Kind: domain.KindWorker, Prompt: "task"},
		"promptless worker": {ProjectID: "mer", Kind: domain.KindWorker},
		"orchestrator":      {ProjectID: "mer", Kind: domain.KindOrchestrator},
		"standalone worker": {Kind: domain.KindWorker},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			agents := testAgentRegistry{}
			for _, ha := range agentregistry.Harnessed() {
				agents[ha.Harness] = ha.Agent
			}
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			rt := &fakeRuntime{}
			ws := &fakeWorkspace{}
			m := New(Deps{Runtime: rt, Agents: agents, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
			cfg.Harness = domain.HarnessZCode
			cfg.RequestedMode = domain.SessionModeTUI
			_, _, _, err := m.Spawn(t.Context(), cfg)
			if !errors.Is(err, ErrUnknownHarness) {
				t.Fatalf("Spawn() = %v, want ErrUnknownHarness for deferred ZCode", err)
			}
			if len(st.sessions) != 0 || ws.lastCfg.SessionID != "" || ws.destroyed != 0 || rt.created != 0 {
				t.Fatal("deferred ZCode must not create session, workspace, or runtime state")
			}
		})
	}
}

func TestZCodeRestoreFallbackRejectsStandingInstructions(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		for _, source := range []string{"inline", "file"} {
			t.Run(string(kind)+"/"+source, func(t *testing.T) {
				inline, file := "AO role instructions", ""
				if source == "file" {
					file = filepath.Join(t.TempDir(), "system.md")
					if err := os.WriteFile(file, []byte(inline), 0o600); err != nil {
						t.Fatal(err)
					}
					inline = ""
				}
				meta := domain.SessionMetadata{AgentSessionID: "sess_previous"}
				if kind == domain.KindWorker {
					meta.Prompt = "saved task"
				}
				argv, _, _, err := restoreArgv(t.Context(), zcode.New(), "ao-1", t.TempDir(), meta,
					inline, file, ports.AgentConfig{}, kind, domain.HarnessZCode, t.TempDir(), nil)
				if argv != nil || err == nil || !strings.Contains(err.Error(), "AO standing instructions") {
					t.Fatalf("restore = %v, %v; want instruction rejection before any fresh launch", argv, err)
				}
			})
		}
	}
}
