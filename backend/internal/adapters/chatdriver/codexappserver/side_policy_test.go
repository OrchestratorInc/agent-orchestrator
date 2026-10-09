package codexappserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A side uses the ordinary provider launch configuration. Tool access is not
// restricted by its identity; the side manager supplies behavioral instructions.
func TestSideConversationsPreserveProviderToolDefaults(t *testing.T) {
	for _, operation := range []string{"start", "fork", "resume"} {
		t.Run(operation, func(t *testing.T) {
			d, srv := newTestDriver(t)
			cfg := ports.ChatStartConfig{WorkspacePath: t.TempDir(), Permissions: "auto", Effort: "high", SystemPrompt: "side standing instructions", ProviderScopeID: "side", ProviderIDsScoped: true}
			var conv ports.ChatConversation
			var err error
			method := "thread/" + operation
			switch operation {
			case "start":
				conv, err = d.Start(context.Background(), cfg)
			case "fork":
				srv.reply(method, `{"thread":{"id":"isolated-side"}}`)
				conv, err = d.ForkIntoHost(context.Background(), "main", "completed-A", cfg)
			case "resume":
				conv, err = d.Resume(context.Background(), ports.ChatResumeConfig{WorkspacePath: cfg.WorkspacePath, ProviderConversationID: "side", Permissions: cfg.Permissions, Effort: cfg.Effort, SystemPrompt: cfg.SystemPrompt, ProviderScopeID: "side", ProviderIDsScoped: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conv.Close()
			var params map[string]any
			if err := json.Unmarshal(srv.awaitFrame(func(f frame) bool { return f.Method == method }).Params, &params); err != nil {
				t.Fatal(err)
			}
			config := params["config"].(map[string]any)
			if len(config) != 1 || config["model_reasoning_effort"] != "high" || params["developerInstructions"] != cfg.SystemPrompt || params["sandbox"] == "read-only" {
				t.Fatalf("inherited configuration changed: %+v", params)
			}
			if operation == "fork" && params["lastTurnId"] != "completed-A" {
				t.Fatal(params)
			}
			if srv.sentMethod("config/read") || srv.sentMethod("turn/start") {
				t.Fatal("opening side queried restriction config or dispatched work")
			}
		})
	}
}
