package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestOpenCodeTaskModelsComeFromACPRatherThanCLIOrCache(t *testing.T) {
	for _, agentID := range []string{"opencode", "opencode-v2"} {
		t.Run(agentID, func(t *testing.T) {
			env := providerTestEnv(t)
			env["OPENCODE_API_KEY"] = ""
			env["XDG_CACHE_HOME"] = t.TempDir()
			if path := providerCachePath(agentID, env); path != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"opencode":{"models":{"mimo-v2.6-flash-free":{"name":"MiMo free"}}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			request := ports.AgentModelDiscoveryRequest{AgentID: agentID, Binary: "must-not-run-models", WorkingDir: t.TempDir(), Env: env}
			discoverer := Discoverer{ACPOptions: map[string]ACPOptionListFunc{
				agentID: func(_ context.Context, got ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
					if !reflect.DeepEqual(got, request) {
						t.Fatalf("discovery inputs = %#v, want %#v", got, request)
					}
					choices := []ports.ChatConfigOptionChoice{{Value: "opencode-go/mimo-v2.6-flash", Name: "MiMo"}}
					if got.Env["OPENCODE_API_KEY"] != "" {
						choices = append(choices, ports.ChatConfigOptionChoice{Value: "opencode/mimo-v2.6-flash-free", Name: "MiMo free"})
					}
					return []ports.ChatConfigOption{{
						ID: "model", Category: "model", Type: ports.ChatConfigOptionSelect,
						Current: ports.ChatConfigOptionValue{Select: "opencode-go/mimo-v2.6-flash"}, Choices: choices,
					}}, nil
				},
			}}
			for _, connected := range []bool{false, true} {
				if connected {
					env["OPENCODE_API_KEY"] = "project-test-key"
				}
				got, err := discoverer.Discover(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"opencode-go/mimo-v2.6-flash"}
				if connected {
					want = append(want, "opencode/mimo-v2.6-flash-free")
				}
				if got.Source != "acp" || !reflect.DeepEqual(got.RunnableModels, want) {
					t.Fatalf("connected=%v: source=%s runnable=%v, want ACP %v", connected, got.Source, got.RunnableModels, want)
				}
				if !got.Models[0].IsDefault || got.Models[0].ID != want[0] {
					t.Fatalf("ACP default lost: %#v", got.Models)
				}
			}
		})
	}
}

func TestOpenCodeDiscoveryNeverFallsBackToCLI(t *testing.T) {
	boom := errors.New("ACP unavailable")
	for _, agentID := range []string{"opencode", "opencode-v2"} {
		for _, state := range []string{"missing", "failed", "empty"} {
			t.Run(agentID+"/"+state, func(t *testing.T) {
				d := Discoverer{ACPOptions: map[string]ACPOptionListFunc{}}
				if state != "missing" {
					d.ACPOptions[agentID] = func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
						if state == "failed" {
							return nil, boom
						}
						return nil, nil
					}
				}
				got, err := d.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: agentID})
				if err == nil || !strings.Contains(err.Error(), "ACP model discovery") || len(got.Models) != 0 || (state == "failed" && !errors.Is(err, boom)) {
					t.Fatalf("catalog=%#v error=%v, want ACP discovery failure", got, err)
				}
			})
		}
	}
}

func TestOpenCodeACPDefaultIgnoresChoiceAndCacheLabels(t *testing.T) {
	for _, agentID := range []string{"opencode", "opencode-v2"} {
		t.Run(agentID, func(t *testing.T) {
			env := providerTestEnv(t)
			env["XDG_CACHE_HOME"] = t.TempDir()
			if path := providerCachePath(agentID, env); path != "" {
				writeConfig(t, path, `{"provider":{"models":{"cached":{"name":"Cached (default)","default":true}}}}`)
			}
			for _, current := range []string{"provider/b", "provider/a", "", "provider/missing"} {
				t.Run(current, func(t *testing.T) {
					d := Discoverer{ACPOptions: map[string]ACPOptionListFunc{
						agentID: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
							return []ports.ChatConfigOption{{
								ID: "model", Type: ports.ChatConfigOptionSelect,
								Current: ports.ChatConfigOptionValue{Select: current},
								Choices: []ports.ChatConfigOptionChoice{
									{Value: "provider/a", Name: "Default"},
									{Value: "provider/b", Name: "Selected"},
									{Value: "provider/c", Name: "Other (default)"},
									{Value: "provider/d", Name: "Default (recommended)"},
								},
							}}, nil
						},
					}}
					got, err := d.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: agentID, Env: env})
					if err != nil {
						t.Fatal(err)
					}
					for _, model := range got.Models {
						if model.IsDefault != (model.ID == current) {
							t.Fatalf("current=%q: model=%#v, want only ACP's current model marked default", current, model)
						}
						if model.ID == "provider/c" && model.Label != "Other (default)" {
							t.Fatalf("ACP display label changed: %q", model.Label)
						}
					}
					if current == "provider/b" || current == "provider/a" {
						if !got.Models[0].IsDefault || got.Models[0].ID != current {
							t.Fatalf("selected model is not sorted first: %#v", got.Models)
						}
					}
				})
			}
		})
	}
}
