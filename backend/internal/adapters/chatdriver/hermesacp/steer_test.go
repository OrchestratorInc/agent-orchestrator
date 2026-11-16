package hermesacp

import (
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestSteeringIsOfferedFromTheVerifiedHermesVersion(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		want          bool
	}{
		{"hermes-agent", "0.22.5", true},
		{"hermes-agent", "0.23.0", true},
		{"hermes-agent", "v1.0.0", true},
		{"hermes-agent", "0.22.4", true},
		{"hermes-agent", "0.22.3", false},
		{"hermes-agent", "0.0.0", true}, // Homebrew 0.22.4 reports no version
		{"hermes-agent", "unknown", true},
		{"hermes-agent", "", true},
		{"other-agent", "9.9.9", false},
	} {
		init := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: tc.name, Version: tc.version}}
		if got := steeringSupported(init); got != tc.want {
			t.Errorf("steering for %s %s = %v, want %v", tc.name, tc.version, got, tc.want)
		}
	}
	if steeringSupported(acpsdk.InitializeResponse{}) {
		t.Error("steering offered without agentInfo")
	}
}

func TestSteerCommandWrapsTextAndRefusesOtherBlocks(t *testing.T) {
	got, err := steerCommand([]acpsdk.ContentBlock{acpsdk.TextBlock("use staging"), acpsdk.TextBlock("and keep 30s")})
	if err != nil || len(got) != 1 || got[0].Text == nil || got[0].Text.Text != "/steer use staging\n\nand keep 30s" {
		t.Fatalf("steerCommand = %#v, %v", got, err)
	}
	if _, err := steerCommand([]acpsdk.ContentBlock{acpsdk.ImageBlock("aGk=", "image/png")}); err == nil {
		t.Fatal("an image was accepted for /steer")
	}
	if _, err := steerCommand([]acpsdk.ContentBlock{acpsdk.TextBlock("   ")}); err == nil {
		t.Fatal("blank guidance was accepted")
	}
}

func TestSteerAcknowledgementRecognisesOnlyHermesReplies(t *testing.T) {
	for text, want := range map[string]struct {
		recognised, declined bool
	}{
		"⏩ Steer queued for the active turn: use staging":       {true, false},
		"No active turn — queued for the next turn. (1 queued)": {true, false},
		"⚠️ Steer failed: agent busy":                           {true, true},
		"I will edit staging.yaml next.":                        {false, false},
		"Queued for the next turn. (1 queued)":                  {false, false},
	} {
		recognised, declined := steerAcknowledgement(text)
		if recognised != want.recognised || (declined != nil) != want.declined {
			t.Errorf("steerAcknowledgement(%q) = %v, %v; want recognised=%v declined=%v", text, declined, recognised, want.recognised, want.declined)
		}
	}
	if _, declined := steerAcknowledgement("⚠️ Steer failed: agent busy"); declined == nil || !strings.Contains(declined.Error(), "agent busy") {
		t.Fatalf("declined reason = %v, want Hermes' reason", declined)
	}
}

func TestBindingSteersWithHermesSlashCommand(t *testing.T) {
	if steerPrompt.Supported == nil || steerPrompt.Prompt == nil || steerPrompt.Ack == nil {
		t.Fatal("the Hermes steering binding is incomplete")
	}
}
