package poolacp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandingHookValidation(t *testing.T) {
	for _, tc := range []struct { name, prompt, path string; valid bool }{
		{"ordinary", "Keep native defaults.", "/private/ao/hook.sh", true},
		{"shell metacharacters", "'$(touch nope)\n%\\", "/private/a' b/hook.sh", true},
		{"below cap", strings.Repeat("a", 32*1024-1), "/private/hook", true},
		{"at cap", strings.Repeat("a", 32*1024), "/private/hook", false},
		{"multibyte cap", strings.Repeat("界", 11000), "/private/hook", false},
		{"invalid UTF8", string([]byte{0xff}), "/private/hook", false},
		{"NUL prompt", "a\x00b", "/private/hook", false},
		{"relative path", "policy", "hook.sh", false},
		{"newline path", "policy", "/private/hook\n.sh", false},
		{"NUL path", "policy", "/private/hook\x00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings, helper, err := BuildStandingHook(tc.prompt, tc.path)
			if (err == nil) != tc.valid { t.Fatalf("valid=%v, error=%v", tc.valid, err) }
			if !tc.valid {
				if settings != nil || helper != nil { t.Fatal("invalid input returned partial artifacts") }
				return
			}
			if strings.Contains(string(settings), "name:") || strings.Contains(string(settings), "prompts:") || strings.Contains(string(settings), "permissions:") { t.Fatal("overlay must only append an unnamed hook") }
			if !strings.Contains(string(settings), "SessionStart:") || !strings.Contains(string(helper), "hook_specific_output") || !strings.Contains(string(helper), "additional_context") { t.Fatal("missing documented additive hook fields") }
			if !strings.HasPrefix(string(helper), "#!/bin/sh\nprintf '%s' '") { t.Fatal("helper must use literal constant printf format") }
		})
	}
}

func TestUnqualifiedLaunchAlwaysRefused(t *testing.T) {
	for _, cfg := range []acpdriver.LaunchConfig{{}, {WorkspacePath: "/workspace", SystemPrompt: "policy"}, {ReconnectOnly: true}} {
		args, env, err := configure(context.Background(), cfg)
		if !errors.Is(err, ports.ErrChatDriverIncompatible) || args != nil || env != nil { t.Fatalf("unqualified admission: %v", err) }
	}
	for _, capability := range []ports.ChatCapability{ports.ChatCapabilityStreaming, ports.ChatCapabilityTools, ports.ChatCapabilityApprovals, ports.ChatCapabilityInterrupt, ports.ChatCapabilityResume} {
		caps := conservativeCapabilities()
		value, present := caps[capability]
		if !present || value { t.Fatalf("must explicitly disable nativeacp default %s", capability) }
	}
}

func TestShellPayloadRoundTripWithoutExecution(t *testing.T) {
	prompt := "quotes: ' \"; substitutions: $(id) `id`; format: %s; unicode: 界\n"
	_, helper, err := BuildStandingHook(prompt, "/private/hook")
	if err != nil { t.Fatal(err) }
	// Decode only the generator's literal shell quoting; never execute a helper.
	literal := strings.TrimSuffix(strings.TrimPrefix(string(helper), "#!/bin/sh\nprintf '%s' '"), "'\n")
	payload := strings.ReplaceAll(literal, "'\"'\"'", "'")
	var decision struct {
		Output struct { Context string `json:"additional_context"` } `json:"hook_specific_output"`
	}
	if err := json.Unmarshal([]byte(payload), &decision); err != nil { t.Fatal(err) }
	if decision.Output.Context != prompt { t.Fatal("instruction payload changed") }
}
