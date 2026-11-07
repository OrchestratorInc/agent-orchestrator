package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
)

type openCodeLoginCall struct {
	args          []string
	configContent string
}

func runOpenCodeLoginForTest(t *testing.T, major, selection string, fail func(args []string) error) ([]openCodeLoginCall, error) {
	t.Helper()
	var calls []openCodeLoginCall
	deps := Deps{
		In:  strings.NewReader(selection),
		Out: io.Discard,
		Err: io.Discard,
		LookPath: func(string) (string, error) {
			return "", errors.New("not on PATH")
		},
		RunInteractiveCommand: func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
			if name != "/managed/bin/opencode" {
				t.Errorf("executable = %q, want adapter-resolved OpenCode", name)
			}
			calls = append(calls, openCodeLoginCall{args: append([]string(nil), args...), configContent: os.Getenv("OPENCODE_CONFIG_CONTENT")})
			if fail != nil {
				return fail(args)
			}
			return nil
		},
	}
	cmd := newOpenCodeLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/opencode", "--major", major})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	return calls, cmd.Execute()
}

func TestOpenCodeLoginProviderChooserIsUnchanged(t *testing.T) {
	for _, major := range []string{"1", "2"} {
		t.Run("v"+major, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_CONTENT", "")
			calls, err := runOpenCodeLoginForTest(t, major, "1\n", nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if len(calls) != 1 || !slices.Equal(calls[0].args, []string{"auth", "login"}) || calls[0].configContent != "" {
				t.Fatalf("calls = %#v, want one plain `auth login` without an AO plugin", calls)
			}
		})
	}
}

func TestOpenCodeLoginClaudePlanLoadsPinnedPluginForEachVersion(t *testing.T) {
	tests := []struct {
		major, key, plugin string
		wantLogin          []string
	}{
		{"1", "plugin", opencode.ClaudePlanPluginV1, []string{"auth", "login", "-p", "anthropic"}},
		{"2", "plugins", opencode.ClaudePlanPluginV2, []string{"auth", "login", "anthropic", "--standalone", "--method", opencode.ClaudePlanMethodV2}},
	}
	for _, tt := range tests {
		t.Run("v"+tt.major, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_CONTENT", `{"`+tt.key+`":["user-plugin"],"model":"x/y"}`)
			calls, err := runOpenCodeLoginForTest(t, tt.major, "2\n", nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			login := calls[len(calls)-1]
			if !slices.Equal(login.args, tt.wantLogin) {
				t.Fatalf("login args = %q, want %q", login.args, tt.wantLogin)
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(login.configContent), &config); err != nil {
				t.Fatalf("config content %q: %v", login.configContent, err)
			}
			plugins, _ := config[tt.key].([]any)
			if len(plugins) != 2 || plugins[0] != "user-plugin" || plugins[1] != tt.plugin || config["model"] != "x/y" {
				t.Fatalf("config = %#v, want the user's settings kept and %q added under %q", config, tt.plugin, tt.key)
			}
		})
	}
}

func TestOpenCodeLoginClaudePlanRetriesV2PluginPreload(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	preloads := 0
	calls, err := runOpenCodeLoginForTest(t, "2", "2\n", func(args []string) error {
		if slices.Equal(args[:2], []string{"auth", "list"}) {
			preloads++
			if preloads == 1 {
				return errors.New("plugins did not finish activating")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if preloads != 2 || len(calls) != 3 {
		t.Fatalf("preloads = %d, calls = %d; want a retried preload before login", preloads, len(calls))
	}
}

func TestOpenCodeLoginRejectsUnknownSelectionAndMajor(t *testing.T) {
	if _, err := runOpenCodeLoginForTest(t, "1", "3\n", nil); err == nil {
		t.Fatal("selection 3 succeeded, want an error")
	}
	if _, err := runOpenCodeLoginForTest(t, "3", "1\n", nil); err == nil {
		t.Fatal("major 3 succeeded, want an error")
	}
}

func TestOpenCodeLoginMenuOffersClaudePlan(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeOpenCodeLoginMenu(&stdout, codexLoginStyle{}, 2); err != nil {
		t.Fatalf("writeOpenCodeLoginMenu: %v", err)
	}
	for _, fragment := range []string{"Sign in to OpenCode 2", "Choose a provider", "Claude Pro/Max plan", "Selection [1-2]"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("menu missing %q:\n%s", fragment, stdout.String())
		}
	}
}
