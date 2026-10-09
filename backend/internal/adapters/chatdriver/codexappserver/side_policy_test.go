package codexappserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const restrictedSideConfig = `{"config":{"features":{"multi_agent":false,"multi_agent_v2":false,"agent_message_board":false,"apps":false,"skill_mcp_dependency_install":false},"mcp_servers":{}},"layers":[]}`

func TestSidePolicyFlagsAndThreadSettingsPreserveMainDefaults(t *testing.T) {
	if got := sidePolicyArgv("codex", nil); !reflect.DeepEqual(got, []string{"codex", "app-server"}) {
		t.Fatal(got)
	}
	inherited := map[string]string{"PROJECT_VALUE": "kept"}
	env := sidePolicyEnv(inherited)
	if inherited[sidePolicyEnvKey] != "" || env["PROJECT_VALUE"] != "kept" {
		t.Fatal("main environment changed")
	}
	args := strings.Join(sidePolicyArgv("codex", env), " ")
	for _, feature := range sideDisabledFeatures {
		if !strings.Contains(args, "--disable "+feature) {
			t.Fatal(args)
		}
	}
	d, srv := newTestDriver(t)
	srv.reply("config/read", restrictedSideConfig)
	srv.reply("thread/fork", `{"thread":{"id":"isolated-side"}}`)
	conv, err := d.ForkIntoHost(context.Background(), "main", "completed-A", ports.ChatStartConfig{SidePolicy: true, WorkspacePath: t.TempDir(), Permissions: "auto", Effort: "high", SystemPrompt: "standing policy"})
	if err != nil {
		t.Fatal(err)
	}
	defer conv.Close()
	var params map[string]any
	if err := json.Unmarshal(srv.awaitFrame(func(f frame) bool { return f.Method == "thread/fork" }).Params, &params); err != nil {
		t.Fatal(err)
	}
	config := params["config"].(map[string]any)
	if params["lastTurnId"] != "completed-A" || config["model_reasoning_effort"] != "high" {
		t.Fatal(params)
	}
	for _, feature := range sideDisabledFeatures {
		if config["features."+feature] != false {
			t.Fatal(config)
		}
	}
	if srv.sentMethod("turn/start") {
		t.Fatal("opening side dispatched inherited work")
	}
}

func TestSidePolicyRejectsUnverifiedConfigBeforeThreadCreation(t *testing.T) {
	for _, config := range []string{`{"config":{}}`, strings.Replace(restrictedSideConfig, `"multi_agent":false`, `"multi_agent":null`, 1), strings.Replace(restrictedSideConfig, `"mcp_servers":{}`, `"mcp_servers":null`, 1), strings.Replace(restrictedSideConfig, `"multi_agent":false`, `"multi_agent":true`, 1), strings.Replace(restrictedSideConfig, `"mcp_servers":{}`, `"mcp_servers":{"mixed":{"enabled":true}}`, 1)} {
		t.Run(config, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("config/read", config)
			_, err := d.Start(context.Background(), ports.ChatStartConfig{SidePolicy: true, WorkspacePath: t.TempDir()})
			if err == nil || srv.sentMethod("thread/start") || srv.sentMethod("turn/start") {
				t.Fatalf("unsafe provider opened: %v", err)
			}
		})
	}
}

func TestSidePolicyVersionAndMCPAvailability(t *testing.T) {
	d, _ := newTestDriver(t)
	if err := d.ValidateSidePolicy(context.Background(), ports.ChatStartConfig{}); err == nil {
		t.Fatal("old Codex accepted")
	}
	d.versionProbe = func(context.Context, string) (string, error) { return "codex-cli 0.162.0", nil }
	if err := d.ValidateSidePolicy(context.Background(), ports.ChatStartConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := d.ValidateSidePolicy(context.Background(), ports.ChatStartConfig{MCPServers: []ports.ChatMCPServerConfig{{Name: "mixed"}}}); err == nil {
		t.Fatal("unfiltered MCP accepted")
	}
}

func TestSideResumeReappliesDelegationRestrictions(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("config/read", restrictedSideConfig)
	conv, err := d.Resume(context.Background(), ports.ChatResumeConfig{SidePolicy: true, ProviderConversationID: "side", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer conv.Close()
	var params map[string]any
	if err := json.Unmarshal(srv.awaitFrame(func(f frame) bool { return f.Method == "thread/resume" }).Params, &params); err != nil {
		t.Fatal(err)
	}
	config := params["config"].(map[string]any)
	if config["features.multi_agent"] != false || config["features.multi_agent_v2"] != false {
		t.Fatal(params)
	}
}
