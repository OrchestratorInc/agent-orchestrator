package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeModelCatalogConversation holds the model choices Claude Code 2.1.300
// advertised in a Chat session: each family's newest model only under its
// alias, older models under their full IDs, and the launch model.
func claudeModelCatalogConversation(current string) *conversation {
	c := &conversation{}
	c.configOptions = []ports.ChatConfigOption{{
		ID: "model", Type: ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: current},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: "default", Name: "Default (recommended)", Description: "Sonnet"},
			{Value: "sonnet", Name: "Sonnet", Description: "Sonnet 5.5 · Efficient for routine tasks"},
			{Value: "fable", Name: "Fable", Description: "Fable 5.1 · Most capable for your hardest and longest-running tasks"},
			{Value: "opus", Name: "Opus", Description: "Opus 5.5 · Best for everyday, complex tasks"},
			{Value: "haiku", Name: "Haiku", Description: "Haiku 5.5 · Fastest for quick answers"},
			{Value: "claude-haiku-5-5", Name: "claude-haiku-5-5"},
			{Value: "claude-opus-4-8", Name: "Opus 4.8"},
		},
	}}
	// The Claude binding marks full IDs optional and names them by version,
	// as claudeacp's claudeSessionOptions does.
	names := map[string]string{
		"claude-opus-5-5": "Opus 5.5", "claude-sonnet-5-5": "Sonnet 5.5",
		"claude-fable-5-1": "Fable 5.1", "claude-fable-5-2": "Fable 5.2",
		"claude-haiku-5-5": "Haiku 5.5", "claude-opus-4-8": "Opus 4.8",
	}
	c.optionsFor = func(settings ports.ChatTurnSettings) []SessionOption {
		if name, ok := names[settings.Model]; ok {
			return []SessionOption{{ID: "model", Value: settings.Model, Optional: true, Name: name}}
		}
		return []SessionOption{{ID: "model", Value: settings.Model}}
	}
	return c
}

func TestConfigOptionValueNamedMatchesTheDescriptionLabel(t *testing.T) {
	c := claudeModelCatalogConversation("claude-haiku-5-5")
	for name, want := range map[string]string{
		"Opus 5.5":   "opus",
		"sonnet 5.5": "sonnet",
		"Fable 5.1":  "fable",
		"Opus 4.8":   "claude-opus-4-8",
		"Fable 5.2":  "",
		"Opus 5":     "",
		"":           "",
	} {
		if got := c.configOptionValueNamed("model", name); got != want {
			t.Errorf("configOptionValueNamed(%q) = %q, want %q", name, got, want)
		}
	}
}

// A switch the session cannot honor used to be skipped, so the old model kept
// answering while AO showed the new one (#6399).
func TestCheckModelSwitchRefusesAnOptionalModelTheSessionCannotUse(t *testing.T) {
	c := claudeModelCatalogConversation("claude-haiku-5-5")
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1", "claude-opus-4-8", "opus", "claude-haiku-5-5"} {
		if err := c.checkModelSwitch(context.Background(), model); err != nil {
			t.Errorf("checkModelSwitch(%q) = %v, want the switch allowed", model, err)
		}
	}
	err := c.checkModelSwitch(context.Background(), "claude-fable-5-2")
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("checkModelSwitch(claude-fable-5-2) = %v, want ErrChatConfigOptionInvalid", err)
	}
	// A new chat on the model would keep the agent's own model too, so the
	// refusal must not send the user there.
	if msg := err.Error(); strings.Contains(msg, "new chat") || !strings.Contains(msg, "pick one it lists") {
		t.Fatalf("refusal = %q, want it to point at the listed models, not a new chat", msg)
	}
}

func TestCheckModelSwitchLeavesTheOpenModelAndUnknownCatalogsAlone(t *testing.T) {
	// A session opened on a model it does not list must still start.
	c := claudeModelCatalogConversation("claude-fable-5-2")
	if err := c.checkModelSwitch(context.Background(), "claude-fable-5-2"); err != nil {
		t.Fatalf("checkModelSwitch(current model) = %v, want nil", err)
	}
	// Without an advertised model option nothing is known to refuse.
	c.configOptions = nil
	if err := c.checkModelSwitch(context.Background(), "claude-fable-5-2"); err != nil {
		t.Fatalf("checkModelSwitch(no catalog) = %v, want nil", err)
	}
}

// A chat opened on an optional model its agent does not offer keeps the
// agent's own model (applyTurnSettings skips it). Later sends on that same
// model are not a switch and must not be refused; a real switch still is.
func TestCheckModelSwitchAllowsTheUnofferedModelTheSessionOpenedOn(t *testing.T) {
	c := claudeModelCatalogConversation("default")
	c.sessionID = "session-1"
	if err := c.applyTurnSettings(context.Background(), ports.ChatTurnSettings{Model: "claude-fable-5-2"}); err != nil {
		t.Fatalf("opening on an unoffered model: %v", err)
	}
	if got := c.configOptionCurrent("model"); got != "default" {
		t.Fatalf("current model = %q, want the agent's own default kept", got)
	}
	if err := c.checkModelSwitch(context.Background(), "claude-fable-5-2"); err != nil {
		t.Fatalf("send on the model the session opened on: %v, want allowed", err)
	}
	c.optionsFor = func(settings ports.ChatTurnSettings) []SessionOption {
		return []SessionOption{{ID: "model", Value: settings.Model, Optional: true, Name: "Fable 5.9"}}
	}
	if err := c.checkModelSwitch(context.Background(), "claude-fable-5-9"); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("switch to another unoffered model = %v, want ErrChatConfigOptionInvalid", err)
	}
}

// Opening on an unoffered model, choosing the agent's own model, then asking
// for the opening model again is a switch the session cannot make: it must be
// refused, not exempted as the opening model and silently skipped.
func TestCheckModelSwitchForgetsTheOpeningModelOnceAnotherIsChosen(t *testing.T) {
	c := claudeModelCatalogConversation("default")
	c.sessionID = "session-1"
	ctx := context.Background()
	if err := c.applyTurnSettings(ctx, ports.ChatTurnSettings{Model: "claude-fable-5-2"}); err != nil {
		t.Fatalf("opening on an unoffered model: %v", err)
	}
	if err := c.checkModelSwitch(ctx, "default"); err != nil {
		t.Fatalf("switch to the agent's default: %v", err)
	}
	if err := c.applyTurnSettings(ctx, ports.ChatTurnSettings{Model: "default"}); err != nil {
		t.Fatalf("choosing the agent's default: %v", err)
	}
	if err := c.checkModelSwitch(ctx, "claude-fable-5-2"); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("switch back to the unoffered opening model = %v, want ErrChatConfigOptionInvalid", err)
	}
}

// Choosing a model through the composer's config option also ends the
// opening-model exemption, so switching back to the unoffered opening model
// is refused instead of silently skipped.
func TestSetConfigOptionForgetsTheUnofferedOpeningModel(t *testing.T) {
	agent := &fakeAgent{newConfig: []acpsdk.SessionConfigOption{
		selectConfigOption("model", "Model", "", "default", "default", "opus"),
	}}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "claude-fable-5-2" {
				return []SessionOption{{ID: "model", Value: settings.Model, Optional: true, Name: "Fable 5.2"}}
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	started, err := driver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(), Model: "claude-fable-5-2",
	})
	if err != nil {
		t.Fatalf("Start on an unoffered model: %v", err)
	}
	defer started.Close()
	conv := started.(*conversation)
	if err := conv.checkModelSwitch(context.Background(), "claude-fable-5-2"); err != nil {
		t.Fatalf("send on the opening model: %v, want allowed", err)
	}
	if _, err := conv.SetConfigOption(context.Background(), "model", ports.ChatConfigOptionValue{Select: "opus"}); err != nil {
		t.Fatalf("SetConfigOption(opus): %v", err)
	}
	if err := conv.checkModelSwitch(context.Background(), "claude-fable-5-2"); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("switch back to the opening model = %v, want ErrChatConfigOptionInvalid", err)
	}
}
