package sessionmanager

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSpawn_TUIPiDiscoveredEffort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture CLI uses a Node shebang")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	for _, harness := range []domain.AgentHarness{domain.HarnessPi, domain.HarnessPrimeAgent} {
		t.Run(string(harness), func(t *testing.T) {
			owner, aiName := "@earendil-works/pi-coding-agent", "@earendil-works/pi-ai"
			if harness == domain.HarnessPrimeAgent {
				owner, aiName = "prime-agent", "prime-agent-ai"
			}
			root := t.TempDir()
			// Configured-default lookup reads the process home, not the request env.
			t.Setenv("HOME", root)
			files := map[string]string{
				"package.json": `{"name":"` + owner + `","dependencies":{"@earendil-works/pi-ai":"1"}}`,
				"dist/cli.mjs": "#!/usr/bin/env node\nconsole.log('provider model context\\nfixture reasoning 128k\\nfixture plain 128k');\n",
				"node_modules/@earendil-works/pi-ai/package.json":  `{"name":"` + aiName + `","type":"module","exports":{".":{"import":"./dist/index.js"}}}`,
				"node_modules/@earendil-works/pi-ai/dist/index.js": "throw new Error('do not initialize the SDK');",
				"node_modules/@earendil-works/pi-ai/dist/models.generated.js": `export const MODELS = {fixture:{
				 reasoning:{id:"reasoning",provider:"fixture",reasoning:true,thinkingLevelMap:{minimal:null,low:"low",medium:null,high:"high",xhigh:null}},
				 plain:{id:"plain",provider:"fixture",reasoning:false,thinkingLevelMap:{high:"high"}}
				}};`,
			}
			for name, contents := range files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			catalog, err := (modelcatalog.Discoverer{}).Discover(t.Context(), ports.AgentModelDiscoveryRequest{
				AgentID: string(harness), Binary: filepath.Join(root, "dist/cli.mjs"), WorkingDir: root,
				Env: map[string]string{"HOME": root, "PI_CODING_AGENT_DIR": root, "XDG_CONFIG_HOME": root, "XDG_DATA_HOME": root},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.Models) != 2 {
				t.Fatalf("discovered models = %#v, want reasoning and plain", catalog.Models)
			}
			for _, model := range catalog.Models {
				var want []string
				if model.ID == "fixture/reasoning" {
					want = []string{"low", "high"}
				}
				if !slices.Equal(model.Efforts, want) {
					t.Errorf("%s discovered efforts = %v, want %v", model.ID, model.Efforts, want)
				}
			}
			for _, tc := range []struct {
				name, model, effort string
				inherited, rejected bool
			}{
				{"explicit", "fixture/reasoning", "high", false, false},
				{"inherited", "fixture/reasoning", "high", true, false},
				{"unsupported level", "fixture/reasoning", "xhigh", false, true},
				{"no reasoning", "fixture/plain", "high", false, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					m, st, rt, _ := newManager()
					m.dataDir = root
					agent := &recordingAgent{}
					m.agents = singleAgent{agent: agent}
					m.SetModelCatalog(tuningCatalog{catalog: catalog})
					request := ports.SpawnConfig{
						ProjectID: "mer", Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeTUI,
						AgentConfig: ports.AgentConfig{Model: tc.model, Effort: tc.effort}, EffortOverride: true,
					}
					if tc.inherited {
						project := st.projects["mer"]
						project.Config.Worker = domain.RoleOverride{Harness: harness, AgentConfig: domain.AgentConfig{Model: tc.model, Effort: tc.effort}}
						st.projects["mer"] = project
						request.AgentConfig, request.EffortOverride = ports.AgentConfig{}, false
					}
					_, _, _, err := m.Spawn(t.Context(), request)
					if tc.rejected {
						if !errors.Is(err, ports.ErrUnsupportedEffort) || rt.created != 0 || len(st.sessions) != 0 || agent.launchCalls != 0 {
							t.Fatalf("spawn = %v, runtimes=%d, sessions=%d, launches=%d; want effort rejection before launch", err, rt.created, len(st.sessions), agent.launchCalls)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if agent.lastConfig.Effort != tc.effort || agent.launchCalls != 1 || rt.created != 1 {
						t.Fatalf("launch effort=%q, launches=%d, runtimes=%d; want %q, 1, 1", agent.lastConfig.Effort, agent.launchCalls, rt.created, tc.effort)
					}
				})
			}
		})
	}
}
