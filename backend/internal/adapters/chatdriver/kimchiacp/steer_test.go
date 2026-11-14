package kimchiacp

import (
	"encoding/json"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

func TestSteeringAdvertisedReadsKimchiCapabilityMeta(t *testing.T) {
	for name, tc := range map[string]struct {
		meta map[string]any
		want bool
	}{
		"advertised":     {meta: map[string]any{"kimchi.dev": map[string]any{"steering": true}}, want: true},
		"disabled":       {meta: map[string]any{"kimchi.dev": map[string]any{"steering": false}}},
		"older kimchi":   {meta: map[string]any{"kimchi.dev": map[string]any{"compact": true}}},
		"no vendor meta": {},
		// The ACP extension flag is claude-agent-acp's, not Kimchi's method.
		"acp extension flag": {meta: map[string]any{"steering": map[string]any{"supported": true}}},
	} {
		t.Run(name, func(t *testing.T) {
			init := acpsdk.InitializeResponse{AgentCapabilities: acpsdk.AgentCapabilities{Meta: tc.meta}}
			if got := steeringAdvertised(init); got != tc.want {
				t.Fatalf("steeringAdvertised = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSteeringParamsSplitsTextAndImages(t *testing.T) {
	params, err := steeringParams("session-1", []acpsdk.ContentBlock{
		acpsdk.TextBlock("use staging"),
		acpsdk.ImageBlock("aGVsbG8=", "image/png"),
	})
	if err != nil {
		t.Fatalf("steeringParams: %v", err)
	}
	encoded, _ := json.Marshal(params)
	var got struct {
		SessionID   string                `json:"sessionId"`
		Prompt      string                `json:"prompt"`
		Attachments []acpsdk.ContentBlock `json:"attachments"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "session-1" || got.Prompt != "use staging" {
		t.Fatalf("params = %s", encoded)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Image == nil || got.Attachments[0].Image.MimeType != "image/png" {
		t.Fatalf("attachments = %s, want the one image", encoded)
	}

	textOnly, err := steeringParams("session-1", []acpsdk.ContentBlock{acpsdk.TextBlock("only text")})
	if err != nil {
		t.Fatalf("steeringParams text only: %v", err)
	}
	if _, has := textOnly.(map[string]any)["attachments"]; has {
		t.Fatalf("text-only params carry attachments: %#v", textOnly)
	}
}

func TestSteeringParamsRefusesBlocksKimchiCannotCarry(t *testing.T) {
	if _, err := steeringParams("session-1", []acpsdk.ContentBlock{
		acpsdk.TextBlock("see this"),
		acpsdk.ResourceLinkBlock("notes.md", "file:///tmp/notes.md"),
	}); err == nil {
		t.Fatal("resource link was accepted; Kimchi would drop it")
	}
	if _, err := steeringParams("session-1", []acpsdk.ContentBlock{
		acpsdk.ImageBlock("aGVsbG8=", "image/png"),
	}); err == nil {
		t.Fatal("image without text was accepted; Kimchi requires a prompt")
	}
}

func TestSteeringOutcomeMapsKimchiStatus(t *testing.T) {
	for raw, want := range map[string]acpdriver.SteerOutcome{
		`{"status":"injected"}`:       acpdriver.SteerInjected,
		`{"status":"promptRequired"}`: acpdriver.SteerNoRunningTurn,
	} {
		got, err := steeringOutcome(json.RawMessage(raw))
		if err != nil || got != want {
			t.Fatalf("steeringOutcome(%s) = %v, %v; want %v", raw, got, err, want)
		}
	}
	// claude-agent-acp's response shape is not Kimchi's; it must not pass as injected.
	if _, err := steeringOutcome(json.RawMessage(`{"outcome":"injected"}`)); err == nil {
		t.Fatal("unknown response shape was accepted")
	}
}

func TestSteeringTakesImagesFromKimchi120(t *testing.T) {
	for version, want := range map[string]bool{
		"1.0.8": false, "1.1.0": false, "1.2.0": true, "1.34.2": true, "dev": false, "": false,
	} {
		init := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "kimchi", Version: version}}
		if got := steeringTakesImages(init); got != want {
			t.Errorf("steeringTakesImages(%q) = %v, want %v", version, got, want)
		}
	}
	if steeringTakesImages(acpsdk.InitializeResponse{}) {
		t.Error("steeringTakesImages with no agentInfo = true, want false")
	}
}
