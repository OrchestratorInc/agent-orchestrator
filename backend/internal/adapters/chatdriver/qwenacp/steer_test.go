package qwenacp

import (
	"encoding/json"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestSteeringSupportedFollowsDrainVersion(t *testing.T) {
	for name, tc := range map[string]struct {
		info *acpsdk.Implementation
		want bool
	}{
		"first draining release": {info: &acpsdk.Implementation{Name: "qwen-code", Version: "0.18.0"}, want: true},
		"current release":        {info: &acpsdk.Implementation{Name: "qwen-code", Version: "0.25.1"}, want: true},
		"before the drain":       {info: &acpsdk.Implementation{Name: "qwen-code", Version: "0.17.1"}},
		"unparseable version":    {info: &acpsdk.Implementation{Name: "qwen-code", Version: "nightly"}},
		"no agent info":          {},
	} {
		t.Run(name, func(t *testing.T) {
			if got := steeringSupported(acpsdk.InitializeResponse{AgentInfo: tc.info}); got != tc.want {
				t.Fatalf("steeringSupported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDrainResponseIsAValidQwenDrainReply(t *testing.T) {
	// Qwen treats a missing or non-array messages field as a broken drain and,
	// with its watch flag set, a missing hasQueuedPrompt as unreliable.
	for _, tc := range []struct {
		texts []string
		want  string
	}{
		{texts: []string{}, want: `{"hasQueuedPrompt":false,"messages":[]}`},
		{texts: []string{"use staging"}, want: `{"hasQueuedPrompt":false,"messages":["use staging"]}`},
	} {
		encoded, err := json.Marshal(drainResponse(tc.texts))
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != tc.want {
			t.Fatalf("drainResponse(%q) = %s, want %s", tc.texts, encoded, tc.want)
		}
	}
}

func TestQwenBindingUsesTheMidTurnDrain(t *testing.T) {
	if steerDrain.Method != "craft/drainMidTurnQueue" || steerDrain.Nudge != "craft/midTurnSendNow" {
		t.Fatalf("steer drain = %#v", steerDrain)
	}
}
