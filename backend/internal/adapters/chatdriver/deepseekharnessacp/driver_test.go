package deepseekharnessacp

import (
	"context"
	"reflect"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureBootsTheACPProfile(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{"--profile", "acp"}) {
		t.Fatalf("args = %#v", args)
	}
	// The profile owns the model route and credentials, so AO adds no overlay.
	if len(env) != 0 {
		t.Fatalf("env = %#v, want none", env)
	}
}

func TestPromptFailureCodeOnlyRecognizesDeepSeekMissingRouteKey(t *testing.T) {
	const detail = `turn failed: llm-deepseek: no API key for provider route "deepseek-official"; store DEEPSEEK_API_KEY through the credentials service`
	for _, tc := range []struct {
		name string
		err  error
		want domain.ConversationFailureCode
	}{
		{"observed missing key", &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: detail}, domain.ConversationFailureProviderKeyMissing},
		{"message-only diagnostic", &acpsdk.RequestError{Code: -32603, Message: "Internal error: " + detail}, domain.ConversationFailureProviderKeyMissing},
		{"other route", &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: `turn failed: llm-deepseek: no API key for provider route "other";`}, ""},
		{"unrelated internal error", &acpsdk.RequestError{Code: -32603, Message: "Internal error", Data: "model service unavailable"}, ""},
		{"auth code", &acpsdk.RequestError{Code: -32000, Message: "Authentication required", Data: detail}, ""},
		{"unstructured error", &acpsdk.RequestError{Code: -32603, Message: detail}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptFailureCode(tc.err); got != tc.want {
				t.Fatalf("promptFailureCode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSessionOptionsCarryModelAndNativeEffort 防止供应商原生档位在生成 ACP 请求前被旧映射改写。
func TestSessionOptionsCarryModelAndNativeEffort(t *testing.T) {
	got := sessionOptions(ports.ChatTurnSettings{
		Model:  `["codegpt","cn:deepseek-v4.1-flash"]`,
		Effort: "xhigh",
	})
	want := []acpdriver.SessionOption{
		{ID: "model", Value: `["codegpt","cn:deepseek-v4.1-flash"]`},
		{ID: "reasoning_effort", Value: "xhigh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSessionOptionsOmitUnsetSettings(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Fatalf("options = %#v, want nil", got)
	}
}

// TestSessionOptionsRetainNativeLevels 只兼容 none 别名，其余档位原样交由会话校验。
func TestSessionOptionsRetainNativeLevels(t *testing.T) {
	cases := map[string]string{
		"none":    "off",
		"minimal": "minimal",
		"off":     "off",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"max":     "max",
		// 原生值由实时目录校验，不在此改变大小写。
		"HIGH": "HIGH",
	}
	for input, want := range cases {
		options := sessionOptions(ports.ChatTurnSettings{Effort: input})
		if len(options) != 1 || options[0].ID != "reasoning_effort" {
			t.Fatalf("effort %q produced %#v", input, options)
		}
		if options[0].Value != want {
			t.Fatalf("effort %q mapped to %q, want %q", input, options[0].Value, want)
		}
	}
}

func TestSessionOptionsPassThroughUnknownEffort(t *testing.T) {
	options := sessionOptions(ports.ChatTurnSettings{Effort: "turbo"})
	if len(options) != 1 || options[0].Value != "turbo" {
		t.Fatalf("options = %#v, want the value passed through", options)
	}
}

func TestPermissionPolicyAnswersOnceForAutoAndEdits(t *testing.T) {
	allow := acpsdk.PermissionOptionKindAllowOnce
	edit := acpsdk.ToolKindEdit
	params := func(kind *acpsdk.ToolKind) acpsdk.RequestPermissionRequest {
		return acpsdk.RequestPermissionRequest{
			ToolCall: acpsdk.ToolCallUpdate{Kind: kind},
			Options: []acpsdk.PermissionOption{
				{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
				{OptionId: "allow", Kind: allow},
			},
		}
	}

	// auto answers any request, once.
	if id, ok := permissionPolicy(ports.PermissionModeAuto, params(nil)); !ok || id != "allow" {
		t.Fatalf("auto = (%q, %v), want (allow, true)", id, ok)
	}
	// accept-edits answers edit-like tools only.
	if id, ok := permissionPolicy(ports.PermissionModeAcceptEdits, params(&edit)); !ok || id != "allow" {
		t.Fatalf("accept-edits on edit = (%q, %v), want (allow, true)", id, ok)
	}
	if _, ok := permissionPolicy(ports.PermissionModeAcceptEdits, params(nil)); ok {
		t.Fatal("accept-edits answered a non-edit tool")
	}
	// default stays out of the way entirely.
	if _, ok := permissionPolicy(ports.PermissionModeDefault, params(&edit)); ok {
		t.Fatal("default mode answered a permission request")
	}
}

func TestSessionPreambleCarriesStandingInstructions(t *testing.T) {
	blocks := SessionPreamble(acpdriver.LaunchConfig{SystemPrompt: "follow AO rules"})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want one text block", blocks)
	}
}

func TestSessionPreambleOmitWhenEmpty(t *testing.T) {
	if got := SessionPreamble(acpdriver.LaunchConfig{}); got != nil {
		t.Fatalf("blocks = %#v, want nil", got)
	}
}

func TestHarnessConstantMatchesTheRegistryKey(t *testing.T) {
	if domain.HarnessDeepSeek != "deepseek-harness" {
		t.Fatalf("harness = %q", domain.HarnessDeepSeek)
	}
}
