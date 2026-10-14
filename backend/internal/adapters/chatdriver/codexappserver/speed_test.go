package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func advertiseSpeed(srv *scriptedServer) {
	srv.reply("model/list", `{"data":[{"id":"gpt-test","isDefault":true,"serviceTiers":[{"id":"priority","name":"Fast"},{"id":"ultrafast","name":"Ultrafast"}]},{"id":"normal-only","serviceTiers":[]}]}`)
	srv.reply("config/read", `{"config":{"model_provider":"openai"}}`)
	srv.reply("configRequirements/read", `{"requirements":null}`)
}

func TestSpeedTransitionsApplyTierOnEveryTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	advertiseSpeed(srv)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws", SpeedMode: domain.SpeedModeNormal})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	for _, step := range []struct {
		speed domain.SpeedMode
		tier  string
	}{
		{domain.SpeedModeFast, "priority"}, {domain.SpeedModeUltrafast, "ultrafast"},
		{domain.SpeedModeNormal, "default"}, {domain.SpeedModeFast, "priority"}, {"", ""},
	} {
		if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "go", Settings: ports.ChatTurnSettings{Model: "gpt-test", SpeedMode: step.speed}}); err != nil {
			t.Fatal(err)
		}
		srv.mu.Lock()
		var request frame
		for i := len(srv.seen) - 1; i >= 0; i-- {
			if srv.seen[i].Method == "turn/start" {
				request = srv.seen[i]
				break
			}
		}
		srv.mu.Unlock()
		var params struct {
			ServiceTier string `json:"serviceTier"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatal(err)
		}
		if params.ServiceTier != step.tier {
			t.Fatalf("%s sent tier %q, want %q", step.speed, params.ServiceTier, step.tier)
		}
	}
	var configParams struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(srv.awaitFrame(func(f frame) bool { return f.Method == "config/read" }).Params, &configParams); err != nil || configParams.Cwd != "/tmp/ws" {
		t.Fatalf("capability validation used the wrong project config: cwd=%q, error=%v", configParams.Cwd, err)
	}
}

func TestAcceleratedSpeedRejectsRepeatedCatalogCursor(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	srv.reply("model/list", `{"data":[],"nextCursor":"repeated"}`)
	_, err = conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "go", Settings: ports.ChatTurnSettings{SpeedMode: domain.SpeedModeFast}})
	if !errors.Is(err, ports.ErrUnsupportedSpeed) || srv.sentMethod("turn/start") {
		t.Fatalf("repeated catalog cursor dispatched inference: error=%v", err)
	}
}

func TestUnsupportedSpeedNeverStartsInference(t *testing.T) {
	for _, scenario := range []string{"missing-tier", "managed-policy", "custom-provider", "unknown-policy", "invalid-value"} {
		t.Run(scenario, func(t *testing.T) {
			d, srv := newTestDriver(t)
			advertiseSpeed(srv)
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			model, speed := "gpt-test", domain.SpeedModeFast
			switch scenario {
			case "missing-tier":
				model = "normal-only"
			case "managed-policy":
				srv.reply("configRequirements/read", `{"requirements":{"featureRequirements":{"fast_mode":false}}}`)
			case "custom-provider":
				srv.reply("config/read", `{"config":{"model_provider":"custom"}}`)
			case "unknown-policy":
				srv.reply("configRequirements/read", `{}`)
			case "invalid-value":
				speed = "turbo"
			}
			_, err = conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "go", Settings: ports.ChatTurnSettings{Model: model, SpeedMode: speed}})
			if !errors.Is(err, ports.ErrUnsupportedSpeed) {
				t.Fatalf("error = %v", err)
			}
			if srv.sentMethod("turn/start") {
				t.Fatal("unsupported speed started inference")
			}
		})
	}
}

func TestStartAndResumeRequireAcknowledgedAcceleratedTier(t *testing.T) {
	for _, method := range []string{"thread/start", "thread/resume"} {
		for _, actual := range []string{"priority", "fast", "default", "missing"} {
			t.Run(method+"/"+actual, func(t *testing.T) {
				d, srv := newTestDriver(t)
				advertiseSpeed(srv)
				reply := `{"thread":{"id":"thread-1"},"model":"gpt-test"`
				if actual != "missing" {
					reply += `,"serviceTier":"` + actual + `"`
				}
				srv.reply(method, reply+`}`)
				var conv ports.ChatConversation
				var err error
				if method == "thread/start" {
					conv, err = d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws", Model: "gpt-test", SpeedMode: domain.SpeedModeFast})
				} else {
					conv, err = d.Resume(context.Background(), ports.ChatResumeConfig{WorkspacePath: "/tmp/ws", ProviderConversationID: "thread-1", Model: "gpt-test", SpeedMode: domain.SpeedModeFast})
				}
				if actual == "priority" || actual == "fast" {
					if err != nil {
						t.Fatal(err)
					}
					_ = conv.Close()
				} else if !errors.Is(err, ports.ErrUnsupportedSpeed) {
					t.Fatalf("error = %v", err)
				}
			})
		}
	}
}
