package poolacp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandingHookValidation(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, path string
		valid              bool
	}{
		{"ordinary", "Keep native defaults.", "/private/ao/hook.sh", true},
		{"shell metacharacters", "'$(touch nope)\n%\\", "/private/a' b/hook.sh", true},
		{"below cap", strings.Repeat("a", 30*1024), "/private/hook", true},
		{"serialized overhead", strings.Repeat("a", 32*1024-1), "/private/hook", false},
		{"JSON escaping overhead", strings.Repeat("\n", 17*1024), "/private/hook", false},
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
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if !tc.valid {
				if settings != nil || helper != nil {
					t.Fatal("invalid input returned partial artifacts")
				}
				return
			}
			if strings.Contains(string(settings), "name:") || strings.Contains(string(settings), "prompts:") || strings.Contains(string(settings), "permissions:") {
				t.Fatal("overlay must only append an unnamed hook")
			}
			if !strings.Contains(string(settings), "SessionStart:") || !strings.Contains(string(helper), "hook_specific_output") || !strings.Contains(string(helper), "additional_context") {
				t.Fatal("missing documented additive hook fields")
			}
			if !strings.HasPrefix(string(helper), "#!/bin/sh\nprintf '%s' '") {
				t.Fatal("helper must use literal constant printf format")
			}
		})
	}
}

func TestRequiredStandingHookWitness(t *testing.T) {
	cfg := acpdriver.LaunchConfig{DataDir: t.TempDir(), SessionID: domain.SessionID("owned-session"), WorkspacePath: t.TempDir(), SystemPrompt: "private policy"}
	args, _, err := configure(context.Background(), cfg)
	if err != nil || len(args) != 3 {
		t.Fatalf("configure: %v, %v", args, err)
	}
	if err := validateSessionReady(context.Background(), cfg, "native-id"); err == nil {
		t.Fatal("missing hook must fail closed")
	}
	dir, _ := hookDirectory(cfg)
	for _, event := range []string{`{"hook_event_name":"SessionStart","session_id":"wrong","cwd":"/wrong"}`, `not json`} {
		if err := os.WriteFile(filepath.Join(dir, "started.json"), []byte(event), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateSessionReady(context.Background(), cfg, "native-id"); err == nil {
			t.Fatal("bad hook witness accepted")
		}
	}
	event, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": "native-id", "cwd": cfg.WorkspacePath})
	if err := os.WriteFile(filepath.Join(dir, "started.json"), event, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionReady(context.Background(), cfg, "native-id"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := configure(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionReady(context.Background(), cfg, "native-id"); err == nil {
		t.Fatal("replacement launch reused stale witness")
	}
}
func TestPermissionAndModelMappings(t *testing.T) {
	for _, tc := range []struct {
		mode   ports.PermissionMode
		native string
	}{{ports.PermissionModeDefault, "default"}, {ports.PermissionModeAcceptEdits, "accept-edits"}, {ports.PermissionModeBypassPermissions, "always-allow"}, {ports.PermissionModeAuto, ""}} {
		if got := sessionMode(tc.mode); got != tc.native {
			t.Fatalf("%s: %s", tc.mode, got)
		}
		err := validateTurnSettings(tc.mode, ports.ChatTurnSettings{Approval: tc.mode})
		if (err == nil) != (tc.native != "") {
			t.Fatalf("admission %s: %v", tc.mode, err)
		}
	}
	options := sessionOptions(ports.ChatTurnSettings{Model: "provider-model"})
	if len(options) != 1 || options[0].ID != "model" || options[0].Value != "provider-model" {
		t.Fatal(options)
	}
}

func TestShellPayloadRoundTripWithoutExecution(t *testing.T) {
	prompt := "quotes: ' \"; substitutions: $(id) `id`; format: %s; unicode: 界\n"
	_, helper, err := BuildStandingHook(prompt, "/private/hook")
	if err != nil {
		t.Fatal(err)
	}
	// Decode only the generator's literal shell quoting; never execute a helper.
	literal := strings.TrimSuffix(strings.TrimPrefix(string(helper), "#!/bin/sh\nprintf '%s' '"), "'\n")
	payload := strings.ReplaceAll(literal, "'\"'\"'", "'")
	var decision struct {
		Output struct {
			Context string `json:"additional_context"`
		} `json:"hook_specific_output"`
	}
	if err := json.Unmarshal([]byte(payload), &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Output.Context != prompt {
		t.Fatal("instruction payload changed")
	}
}
