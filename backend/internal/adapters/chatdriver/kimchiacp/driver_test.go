package kimchiacp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureReturnsACPMode(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"--mode", "acp"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if env != nil {
		t.Fatalf("env = %#v, want nil", env)
	}
}

func TestConfigurePassesModel(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Model: "glm-5.2"})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"--mode", "acp", "--model", "glm-5.2"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestConfigurePassesAutoPermission(t *testing.T) {
	tests := []ports.PermissionMode{
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
	}
	for _, perm := range tests {
		args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: perm})
		if err != nil {
			t.Fatalf("configure(%q): %v", perm, err)
		}
		want := []string{"--mode", "acp", "--auto"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("perm %q: args = %#v, want %#v", perm, args, want)
		}
	}
}

func TestConfigurePassesYoloPermission(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: ports.PermissionModeBypassPermissions})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"--mode", "acp", "--yolo"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestConfigurePassesModelAndPermissionTogether(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{
		Model: "glm-5.2", Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"--mode", "acp", "--model", "glm-5.2", "--yolo"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestConfigureAppendsSystemPromptWhenProvided(t *testing.T) {
	data := t.TempDir()
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: data, SessionID: "s1", SystemPrompt: "Follow AO worker rules."})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	// Kimchi reads a path given to the flag as a file, which keeps the command line short.
	file := filepath.Join(data, "prompts", "s1", "kimchi", "system-prompt.md")
	if got, err := os.ReadFile(file); err != nil || string(got) != "Follow AO worker rules." {
		t.Fatalf("system prompt file = %q, %v", got, err)
	}
	want := []string{"--mode", "acp", "--append-system-prompt", file}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestConfigureOmitsBlankSystemPrompt(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: "   "})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"--mode", "acp"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestKimchiPermissionModeMapsVocabulary(t *testing.T) {
	tests := map[ports.PermissionMode]string{
		ports.PermissionModeDefault:           "",
		ports.PermissionModeAcceptEdits:       "auto",
		ports.PermissionModeAuto:              "auto",
		ports.PermissionModeBypassPermissions: "yolo",
	}
	for permission, want := range tests {
		if got := kimchiPermissionMode(permission); got != want {
			t.Errorf("mode(%q) = %q, want %q", permission, got, want)
		}
	}
}

func TestSessionOptionsUseModelConfigOption(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); len(got) != 0 {
		t.Fatalf("empty settings = %#v", got)
	}
	got := sessionOptions(ports.ChatTurnSettings{Model: "glm-5.2"})
	want := []acpdriver.SessionOption{{ID: "model", Value: "glm-5.2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestSessionOptionsIncludePermissionMode(t *testing.T) {
	got := sessionOptions(ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions})
	want := []acpdriver.SessionOption{{ID: "permissions-mode", Value: "yolo"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestSessionOptionsIncludeModelAndPermissionMode(t *testing.T) {
	got := sessionOptions(ports.ChatTurnSettings{
		Model: "glm-5.2", Approval: ports.PermissionModeAuto,
	})
	want := []acpdriver.SessionOption{
		{ID: "model", Value: "glm-5.2"},
		{ID: "permissions-mode", Value: "auto"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestConfigureRejectsImportedRootMismatchWithoutChangingHome(t *testing.T) {
	home := t.TempDir()
	sourceRoot := filepath.Join(home, ".config", "kimchi", "harness")
	otherHome := t.TempDir()
	env := map[string]string{"HOME": otherHome, "USERPROFILE": otherHome, "KIMCHI_CODING_AGENT_DIR": sourceRoot}
	_, _, err := configure(context.Background(), acpdriver.LaunchConfig{
		ProviderConversationID: "native-import",
		Env:                    env,
	})
	if err == nil || !strings.Contains(err.Error(), "ignores KIMCHI_CODING_AGENT_DIR") {
		t.Fatalf("incompatible child root was not rejected before launch: %v", err)
	}
	if env["HOME"] != otherHome || env["USERPROFILE"] != otherHome || env["KIMCHI_CODING_AGENT_DIR"] != sourceRoot {
		t.Fatalf("root validation changed native tool/config home: %v", env)
	}
}

func TestConfigureAcceptsNativeRootAndEquivalentAlias(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "kimchi", "harness")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "native-root")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{root, alias} {
		env := map[string]string{"HOME": home, "USERPROFILE": home, "KIMCHI_CODING_AGENT_DIR": source}
		_, overrides, err := configure(context.Background(), acpdriver.LaunchConfig{
			ProviderConversationID: "native-import",
			Env:                    env,
		})
		if err != nil || overrides != nil {
			t.Fatalf("native root rejected or home overridden: %v, %v", overrides, err)
		}
	}
}
