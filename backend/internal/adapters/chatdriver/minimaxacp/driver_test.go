package minimaxacp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type stubPlugin struct {
	profile string
	calls   *int
}

func (stubPlugin) ResolveBinary(context.Context) (string, error) { return "mcode", nil }
func (stubPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusConfigured, nil
}
func (p stubPlugin) PrepareChatProfile(context.Context, string, string, map[string]string) (string, error) {
	if p.calls != nil {
		*p.calls++
	}
	return p.profile, nil
}

func TestConfigureUsesPrivateAdditiveContextOnEveryLaunch(t *testing.T) {
	profile := t.TempDir()
	var previous string
	for _, contextText := range []string{"--leading-dash\nprivate first", "--fresh restore instructions"} {
		args, env, err := configure(stubPlugin{profile: profile})(context.Background(), acpdriver.LaunchConfig{
			SystemPrompt: contextText, Permissions: ports.PermissionModeDefault,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 3 || args[0] != "acp" || args[1] != "--append-system-prompt-file" {
			t.Fatalf("unexpected argv: %v", args)
		}
		if filepath.Dir(args[2]) != profile || (previous != "" && args[2] != previous) {
			t.Fatal("context file must be private and reuse one bounded path")
		}
		previous = args[2]
		data, err := os.ReadFile(args[2])
		if err != nil || string(data) != contextText {
			t.Fatalf("context round trip: %v", err)
		}
		info, err := os.Stat(args[2])
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("context mode: %v", err)
		}
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			if _, overridden := env[key]; overridden {
				t.Fatalf("Chat must preserve platform temp path: %s", key)
			}
		}
		if env["MINIMAX_DATA_DIR"] != profile || env["MAVIS_DATA_DIR"] != profile {
			t.Fatalf("private environment mismatch: %v", env)
		}
	}
	entries, err := os.ReadDir(profile)
	if err != nil || len(entries) != 1 {
		t.Fatalf("repeated launches retained extra context files: %v, %v", entries, err)
	}
}

func TestConfigurePrunesLegacyContextFiles(t *testing.T) {
	for _, prompt := range []string{"replacement-context", ""} {
		t.Run(map[bool]string{true: "with-context", false: "without-context"}[prompt != ""], func(t *testing.T) {
			profile := t.TempDir()
			for _, name := range []string{".ao-chat-context-one.md", ".ao-chat-context-two.md", ".ao-chat-context.md", "user-notes.md"} {
				if err := os.WriteFile(filepath.Join(profile, name), []byte("previous-content"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			legacyDirectory := filepath.Join(profile, ".ao-chat-context-resource.md")
			if err := os.Mkdir(legacyDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			args, _, err := configure(stubPlugin{profile: profile})(context.Background(), acpdriver.LaunchConfig{SystemPrompt: prompt})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".ao-chat-context-one.md", ".ao-chat-context-two.md"} {
				if _, err := os.Lstat(filepath.Join(profile, name)); !os.IsNotExist(err) {
					t.Fatalf("legacy context %s retained: %v", name, err)
				}
			}
			if prompt == "" {
				if !reflect.DeepEqual(args, []string{"acp"}) {
					t.Fatalf("empty context added a prompt-file argument: %v", args)
				}
				if _, err := os.Lstat(filepath.Join(profile, ".ao-chat-context.md")); !os.IsNotExist(err) {
					t.Fatalf("empty context left the previous prompt file behind: %v", err)
				}
			} else {
				data, err := os.ReadFile(filepath.Join(profile, ".ao-chat-context.md"))
				if err != nil || string(data) != prompt {
					t.Fatalf("replacement context missing: %q, %v", data, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(profile, "user-notes.md"))
			if err != nil || string(data) != "previous-content" {
				t.Fatalf("context cleanup changed unrelated data: %q, %v", data, err)
			}
			if info, err := os.Stat(legacyDirectory); err != nil || !info.IsDir() {
				t.Fatalf("context cleanup removed a directory: %v", err)
			}
		})
	}
}

func TestNativePermissionPolicyUsesConfigNotPlanMode(t *testing.T) {
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		cfg := acpdriver.LaunchConfig{Permissions: mode}
		if err := validateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode}); err != nil {
			t.Fatal(err)
		}
		want := []acpdriver.SessionOption{{ID: "permissionMode", Value: string(ports.NormalizePermissionMode(mode))}}
		if got := launchOptions(cfg); !reflect.DeepEqual(got, want) {
			t.Fatalf("launch options = %v, want %v", got, want)
		}
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, "unknown"} {
		if err := validateTurnSettings(mode, ports.ChatTurnSettings{}); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Fatalf("unsupported mode %q: %v", mode, err)
		}
	}
}

func TestModelMappingPreservesNativeChoicesAndEscapesCatalogIDs(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"custom_provider:work/vendor/model", "m:custom_provider%3Awork:vendor%2Fmodel:u"},
		{"provider+ /m?é!*'()", "m:provider%2B%20:m%3F%C3%A9!*'():u"},
		{"m:custom_provider%3Awork:vendor%2Fmodel:v:fast%2B", "m:custom_provider%3Awork:vendor%2Fmodel:v:fast%2B"},
	} {
		got, err := modelValue(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("modelValue(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, invalid := range []string{"bare-model", "/model", "provider/", "m:p::u", "m:p:m:u:extra", "m:p:%broken:u"} {
		if _, err := modelValue(invalid); err == nil {
			t.Fatalf("accepted invalid model %q", invalid)
		}
	}
	got := sessionOptions(ports.ChatTurnSettings{Model: "p/m", Effort: "high", Approval: ports.PermissionModeAuto})
	want := []acpdriver.SessionOption{{ID: "model", Value: "p/m"}, {ID: "thinkingEffort", Value: "high"}, {ID: "permissionMode", Value: "auto"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %v, want %v", got, want)
	}
}

func TestInitializeRejectsUnqualifiedDistributionAndMissingLoad(t *testing.T) {
	valid := acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentInfo:         &acpsdk.Implementation{Name: "minimax-code", Version: "0.6.5"},
		AgentCapabilities: acpsdk.AgentCapabilities{LoadSession: true},
	}
	if err := validateInitialize(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*acpsdk.InitializeResponse){
		func(r *acpsdk.InitializeResponse) { r.AgentInfo = nil },
		func(r *acpsdk.InitializeResponse) {
			r.AgentInfo = &acpsdk.Implementation{Name: "minimax-code", Version: "0.6.9"}
		},
		func(r *acpsdk.InitializeResponse) { r.AgentCapabilities.LoadSession = false },
	} {
		candidate := valid
		mutate(&candidate)
		if err := validateInitialize(candidate); err == nil {
			t.Fatal("accepted incompatible initialize")
		}
	}
	driver := New(stubPlugin{}, nil)
	if driver.Harness() != domain.HarnessMiniMaxCode {
		t.Fatal("wrong harness identity")
	}
}

func TestContextValidationFailsBeforeLaunch(t *testing.T) {
	for _, text := range []string{" \n\t", strings.Repeat("x", (512<<10)+1)} {
		calls := 0
		if _, _, err := configure(stubPlugin{profile: t.TempDir(), calls: &calls})(context.Background(), acpdriver.LaunchConfig{SystemPrompt: text}); err == nil {
			t.Fatal("accepted invalid standing context")
		}
		if calls != 0 {
			t.Fatal("invalid context copied credential resources before validation")
		}
	}
}

func TestRestoreIdentityCannotSelectLatestOrAnotherMode(t *testing.T) {
	for _, invalid := range []string{"", "latest", "ao-session-id", "mvs_short"} {
		if _, err := decodeNativeID(invalid); err == nil {
			t.Fatalf("accepted non-native identity %q", invalid)
		}
	}
}

func TestCanonicalModelResolvesOnlyAgainstNativeVariantCatalog(t *testing.T) {
	const base = "m:custom_provider%3Aao:native-test:"
	catalog := func(current string, values ...string) []ports.ChatConfigOption {
		choices := make([]ports.ChatConfigOptionChoice, 0, len(values))
		for _, value := range values {
			choices = append(choices, ports.ChatConfigOptionChoice{Value: value})
		}
		return []ports.ChatConfigOption{{ID: "model", Current: ports.ChatConfigOptionValue{Select: current}, Choices: choices}}
	}
	for _, tc := range []struct {
		name, input, current string
		choices              []string
		want                 string
		invalid              bool
	}{
		{"preserve empty default", "custom_provider:ao/native-test", base + "v:", []string{base + "v:", base + "v:thinking"}, base + "v:", false},
		{"preserve thinking default", "custom_provider:ao/native-test", base + "v:thinking", []string{base + "v:", base + "v:thinking"}, base + "v:thinking", false},
		{"unique undefined variant", "custom_provider:ao/native-test", "m:other:model:u", []string{base + "u"}, base + "u", false},
		{"ambiguous variant refused", "custom_provider:ao/native-test", "m:other:model:u", []string{base + "v:", base + "v:thinking"}, "", true},
		{"unknown model refused", "custom_provider:ao/other", base + "v:", []string{base + "v:"}, "", true},
		{"explicit native unchanged", base + "v:thinking", base + "v:", []string{base + "v:", base + "v:thinking"}, base + "v:thinking", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSessionOption(acpdriver.SessionOption{ID: "model", Value: tc.input}, catalog(tc.current, tc.choices...))
			if tc.invalid {
				if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
					t.Fatalf("unqualified model accepted: %v", err)
				}
			} else if err != nil || got.Value != tc.want {
				t.Fatalf("resolved = %q, %v; want %q", got.Value, err, tc.want)
			}
		})
	}
}

// Accepting an empty, wrong-session or partial receipt would admit a prompt
// while MiniMax's root turn or a delegated operation could still be running.
func TestInterruptRequiresCompleteExactNativeStopReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, receipt string
		requestErr    error
		valid         bool
	}{
		{"released", `{"receipt":{"schemaVersion":1,"rootSessionId":"native-session","rootStopped":true,"stoppedSessionIds":[],"activeSessionIds":[],"failedSessionIds":[]}}`, nil, true},
		{"missing receipt", `{}`, nil, false},
		{"wrong session", `{"receipt":{"schemaVersion":1,"rootSessionId":"another-session","rootStopped":true,"activeSessionIds":[],"failedSessionIds":[]}}`, nil, false},
		{"unreleased root", `{"receipt":{"schemaVersion":1,"rootSessionId":"native-session","rootStopped":false,"activeSessionIds":[],"failedSessionIds":[]}}`, nil, false},
		{"active delegate", `{"receipt":{"schemaVersion":1,"rootSessionId":"native-session","rootStopped":true,"activeSessionIds":["child"],"failedSessionIds":[]}}`, nil, false},
		{"failed stop", `{"receipt":{"schemaVersion":1,"rootSessionId":"native-session","rootStopped":true,"activeSessionIds":[],"failedSessionIds":["native-session"]}}`, nil, false},
		{"missing lists", `{"receipt":{"schemaVersion":1,"rootSessionId":"native-session","rootStopped":true}}`, nil, false},
		{"malformed", `{`, nil, false},
		{"transport failure", "", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := settleInterrupt(context.Background(), func(_ context.Context, method string, params any) (json.RawMessage, error) {
				if method != "mcode/session/delegation/stop" || !reflect.DeepEqual(params, map[string]string{"sessionId": "native-session"}) {
					t.Fatalf("native stop request = %s, %#v", method, params)
				}
				return json.RawMessage(tc.receipt), tc.requestErr
			}, "native-session")
			if (err == nil) != tc.valid {
				t.Fatalf("settlement error = %v, valid=%v", err, tc.valid)
			}
			if tc.requestErr != nil && !errors.Is(err, tc.requestErr) {
				t.Fatalf("lost native request failure: %v", err)
			}
		})
	}
}
