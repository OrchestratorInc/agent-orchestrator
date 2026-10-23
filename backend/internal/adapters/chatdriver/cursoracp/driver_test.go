package cursoracp

import (
	"context"
	"encoding/json"
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

type fakeExtensionBridge struct {
	input         ports.ChatInputRequest
	inputResponse ports.ChatInputResponse
	approval      acpdriver.ClientApprovalRequest
	selected      string
	plan          *domain.ConversationPlan
}

func (f *fakeExtensionBridge) RequestInput(
	_ context.Context,
	request ports.ChatInputRequest,
) (ports.ChatInputResponse, error) {
	f.input = request
	return f.inputResponse, nil
}

func (f *fakeExtensionBridge) RequestApproval(
	_ context.Context,
	request acpdriver.ClientApprovalRequest,
) (string, error) {
	f.approval = request
	return f.selected, nil
}

func (f *fakeExtensionBridge) UpdatePlan(plan *domain.ConversationPlan) { f.plan = plan }

func TestStandingInstructionsLeadTheFirstPromptAsText(t *testing.T) {
	if got := sessionPreamble(acpdriver.LaunchConfig{}); got != nil {
		t.Fatalf("preamble without instructions = %#v, want none", got)
	}
	cfg := acpdriver.LaunchConfig{SystemPrompt: "AO standing instructions\n<ao-handoff-request>note</ao-handoff-request>"}
	args, _, err := configure(context.Background(), cfg)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if len(args) != 1 || args[0] != "acp" {
		t.Fatalf("args = %#v, want only acp: Cursor's chat ignores --plugin-dir rules", args)
	}
	got := sessionPreamble(cfg)
	if len(got) != 1 || got[0].Text == nil || !strings.Contains(got[0].Text.Text, cfg.SystemPrompt) ||
		!strings.HasPrefix(got[0].Text.Text, "<ao-standing-instructions>") {
		t.Fatalf("preamble = %#v, want the instructions as one wrapped text block", got)
	}
}

func TestConfigureMapsEveryCursorPermissionMode(t *testing.T) {
	tests := []struct {
		name string
		mode ports.PermissionMode
		want []string
	}{
		{name: "default", mode: ports.PermissionModeDefault, want: []string{"acp"}},
		{name: "accept edits", mode: ports.PermissionModeAcceptEdits, want: []string{"acp"}},
		{name: "auto review", mode: ports.PermissionModeAuto, want: []string{"--auto-review", "acp"}},
		{name: "bypass", mode: ports.PermissionModeBypassPermissions, want: []string{"--force", "acp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, env, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: tt.mode})
			if err != nil {
				t.Fatalf("configure: %v", err)
			}
			if !reflect.DeepEqual(args, tt.want) {
				t.Fatalf("args = %#v, want %#v", args, tt.want)
			}
			if env != nil {
				t.Fatalf("env = %#v, want nil", env)
			}
		})
	}
}

func TestCursorPermissionPolicyImplementsAdvertisedModes(t *testing.T) {
	edit := acpsdk.ToolKindEdit
	execute := acpsdk.ToolKindExecute
	options := []acpsdk.PermissionOption{
		{OptionId: "allow-once", Kind: acpsdk.PermissionOptionKindAllowOnce},
		{OptionId: "allow-always", Kind: acpsdk.PermissionOptionKindAllowAlways},
		{OptionId: "reject-once", Kind: acpsdk.PermissionOptionKindRejectOnce},
	}
	tests := []struct {
		name    string
		mode    ports.PermissionMode
		kind    *acpsdk.ToolKind
		wantID  acpsdk.PermissionOptionId
		handled bool
	}{
		{name: "default parks", mode: ports.PermissionModeDefault, kind: &edit},
		{name: "accept edits allows edit once", mode: ports.PermissionModeAcceptEdits, kind: &edit, wantID: "allow-once", handled: true},
		{name: "accept edits parks execute", mode: ports.PermissionModeAcceptEdits, kind: &execute},
		{name: "auto parks classifier escalation", mode: ports.PermissionModeAuto, kind: &execute},
		{name: "bypass prefers persistent allow", mode: ports.PermissionModeBypassPermissions, kind: &execute, wantID: "allow-always", handled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotHandled := permissionPolicy(tt.mode, acpsdk.RequestPermissionRequest{
				ToolCall: acpsdk.ToolCallUpdate{Kind: tt.kind}, Options: options,
			})
			if gotID != tt.wantID || gotHandled != tt.handled {
				t.Fatalf("selection = (%q, %v), want (%q, %v)", gotID, gotHandled, tt.wantID, tt.handled)
			}
		})
	}
}

func TestValidateTurnSettingsRejectsCursorProcessModeTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial ports.PermissionMode
		turn    ports.PermissionMode
		wantErr bool
	}{
		{name: "empty preserves auto launch", initial: ports.PermissionModeAuto},
		{name: "same auto is valid", initial: ports.PermissionModeAuto, turn: ports.PermissionModeAuto},
		{name: "default to accept edits is dynamic", initial: ports.PermissionModeDefault, turn: ports.PermissionModeAcceptEdits},
		{name: "accept edits to default is dynamic", initial: ports.PermissionModeAcceptEdits, turn: ports.PermissionModeDefault},
		{name: "default to auto needs restart", initial: ports.PermissionModeDefault, turn: ports.PermissionModeAuto, wantErr: true},
		{name: "auto to default needs restart", initial: ports.PermissionModeAuto, turn: ports.PermissionModeDefault, wantErr: true},
		{name: "default to bypass needs restart", initial: ports.PermissionModeDefault, turn: ports.PermissionModeBypassPermissions, wantErr: true},
		{name: "bypass to accept edits needs restart", initial: ports.PermissionModeBypassPermissions, turn: ports.PermissionModeAcceptEdits, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTurnSettings(test.initial, ports.ChatTurnSettings{Approval: test.turn})
			if (err != nil) != test.wantErr {
				t.Fatalf("validateTurnSettings(%q, %q) error = %v, wantErr %v",
					test.initial, test.turn, err, test.wantErr)
			}
		})
	}
}

func TestHandleAskQuestionUsesDurableInputFlow(t *testing.T) {
	bridge := &fakeExtensionBridge{inputResponse: ports.ChatInputResponse{
		Action: ports.ChatInputActionAccept,
		Content: map[string]any{
			"mode":    "agent",
			"targets": []any{"backend", "frontend"},
		},
	}}
	raw := json.RawMessage(`{
		"toolCallId":"call-1",
		"title":"Need input",
		"questions":[
			{"id":"mode","prompt":"Which mode?","options":[{"id":"agent","label":"Agent"},{"id":"plan","label":"Plan"}]},
			{"id":"targets","prompt":"Which targets?","allowMultiple":true,"options":[{"id":"backend","label":"Backend"},{"id":"frontend","label":"Frontend"}]}
		]
	}`)

	result, handled, err := handleExtension(context.Background(), bridge, "cursor/ask_question", raw)
	if err != nil {
		t.Fatalf("handleExtension: %v", err)
	}
	if !handled {
		t.Fatal("cursor/ask_question was not handled")
	}
	properties, _ := bridge.input.Schema["properties"].(map[string]any)
	mode, _ := properties["mode"].(map[string]any)
	targets, _ := properties["targets"].(map[string]any)
	if bridge.input.Mode != ports.ChatInputModeForm || bridge.input.Message != "Need input" ||
		mode["type"] != "string" || targets["type"] != "array" {
		t.Fatalf("input request = %#v", bridge.input)
	}
	encoded, _ := json.Marshal(result)
	var response struct {
		Outcome struct {
			Outcome string `json:"outcome"`
			Answers []struct {
				QuestionID        string   `json:"questionId"`
				SelectedOptionIDs []string `json:"selectedOptionIds"`
			} `json:"answers"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Outcome.Outcome != "answered" || len(response.Outcome.Answers) != 2 ||
		!reflect.DeepEqual(response.Outcome.Answers[0].SelectedOptionIDs, []string{"agent"}) ||
		!reflect.DeepEqual(response.Outcome.Answers[1].SelectedOptionIDs, []string{"backend", "frontend"}) {
		t.Fatalf("ask response = %s", encoded)
	}
}

func TestHandleCreatePlanPublishesPlanAndUsesDurableApprovalFlow(t *testing.T) {
	bridge := &fakeExtensionBridge{selected: "accept"}
	raw := json.RawMessage(`{
		"toolCallId":"call-2",
		"name":"Repair ACP",
		"overview":"Preserve lifecycle safety.",
		"plan":"1. Fix replay. 2. Verify.",
		"todos":[
			{"id":"one","content":"Fix replay","status":"completed"},
			{"id":"two","content":"Verify","status":"in_progress"}
		]
	}`)

	result, handled, err := handleExtension(context.Background(), bridge, "cursor/create_plan", raw)
	if err != nil {
		t.Fatalf("handleExtension: %v", err)
	}
	if !handled {
		t.Fatal("cursor/create_plan was not handled")
	}
	if bridge.plan == nil || len(bridge.plan.Steps) != 2 || bridge.plan.Steps[0].Status != domain.PlanStepCompleted ||
		bridge.plan.Steps[1].Status != domain.PlanStepInProgress {
		t.Fatalf("published plan = %#v", bridge.plan)
	}
	if bridge.approval.Summary != "Approve Cursor plan: Repair ACP" || len(bridge.approval.Decisions) != 2 {
		t.Fatalf("approval request = %#v", bridge.approval)
	}
	encoded, _ := json.Marshal(result)
	if string(encoded) != `{"outcome":{"outcome":"accepted"}}` {
		t.Fatalf("plan response = %s", encoded)
	}
}

func TestConfigureUsesNativeCursorACPWithoutStandingRule(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{
		Model: "gpt-5.5", Permissions: ports.PermissionModeDefault,
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if want := []string{"acp"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if env != nil {
		t.Fatalf("env = %#v, want nil", env)
	}
}

func TestSessionOptionsSplitAModelIDIntoCursorSettings(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Fatalf("empty settings = %#v", got)
	}
	opt := func(id, value string) acpdriver.SessionOption {
		return acpdriver.SessionOption{ID: id, Value: value, Optional: id != "model"}
	}
	effort := func(value, reasoning string) []acpdriver.SessionOption {
		return []acpdriver.SessionOption{opt("effort", value), opt("reasoning_effort", value), opt("reasoning", reasoning)}
	}
	tests := []struct {
		name     string
		settings ports.ChatTurnSettings
		want     []acpdriver.SessionOption
	}{
		{"plain id keeps Cursor's saved settings", ports.ChatTurnSettings{Model: "claude-opus-4-8"},
			[]acpdriver.SessionOption{opt("model", "claude-opus-4-8")}},
		{"auto is Cursor's default", ports.ChatTurnSettings{Model: "auto"},
			[]acpdriver.SessionOption{opt("model", "default")}},
		{"command-line id with every setting", ports.ChatTurnSettings{Model: "claude-opus-4-8-thinking-high-fast"},
			append([]acpdriver.SessionOption{opt("model", "claude-opus-4-8"), opt("thinking", "true"), opt("fast", "true")}, effort("high", "high")...)},
		{"a setting the id leaves out is off", ports.ChatTurnSettings{Model: "claude-opus-4-8-low"},
			append([]acpdriver.SessionOption{opt("model", "claude-opus-4-8"), opt("thinking", "false"), opt("fast", "false")}, effort("low", "low")...)},
		{"effort before thinking", ports.ChatTurnSettings{Model: "claude-4.6-opus-high-thinking"},
			append([]acpdriver.SessionOption{opt("model", "claude-opus-4-6"), opt("thinking", "true"), opt("fast", "false")}, effort("high", "high")...)},
		{"an older Claude is named family first", ports.ChatTurnSettings{Model: "claude-4.6-opus-max-thinking"},
			append([]acpdriver.SessionOption{opt("model", "claude-opus-4-6"), opt("thinking", "true"), opt("fast", "false")}, effort("max", "max")...)},
		{"an older Claude with no settings in its id", ports.ChatTurnSettings{Model: "claude-4-sonnet-thinking"},
			[]acpdriver.SessionOption{opt("model", "claude-sonnet-4"), opt("thinking", "true"), opt("fast", "false")}},
		{"extra high is an effort", ports.ChatTurnSettings{Model: "gpt-5.5-extra-high-fast"},
			append([]acpdriver.SessionOption{opt("model", "gpt-5.5"), opt("thinking", "false"), opt("fast", "true")}, effort("xhigh", "extra-high")...)},
		{"grok drops the command-line prefix", ports.ChatTurnSettings{Model: "cursor-grok-4.6-high"},
			append([]acpdriver.SessionOption{opt("model", "grok-4.6"), opt("thinking", "false"), opt("fast", "false")}, effort("high", "high")...)},
		{"an older session's bracket id", ports.ChatTurnSettings{Model: "gpt-5.5[context=272k,reasoning=medium,fast=false]"},
			append([]acpdriver.SessionOption{opt("model", "gpt-5.5"), opt("fast", "false")}, effort("medium", "medium")...)},
		{"a chosen effort wins", ports.ChatTurnSettings{Model: "gpt-5.5[reasoning=medium]", Effort: "high"},
			append([]acpdriver.SessionOption{opt("model", "gpt-5.5")}, effort("high", "high")...)},
		{"a model whose name ends in a number", ports.ChatTurnSettings{Model: "gpt-5-mini"},
			[]acpdriver.SessionOption{opt("model", "gpt-5-mini")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionOptions(tt.settings); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("options = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func assertNetPreload(t *testing.T, dataDir, nodeOptions string) {
	t.Helper()
	const prefix = `--require "`
	start := strings.LastIndex(nodeOptions, prefix)
	if start < 0 || !strings.HasSuffix(nodeOptions, `"`) {
		t.Fatalf("NODE_OPTIONS = %q, want it to require the Cursor preload", nodeOptions)
	}
	path := filepath.FromSlash(nodeOptions[start+len(prefix) : len(nodeOptions)-1])
	if filepath.Dir(path) != filepath.Join(dataDir, "cursor") {
		t.Fatalf("preload path = %q, want it under %q", path, filepath.Join(dataDir, "cursor"))
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preload: %v", err)
	}
	if string(body) != string(netPreload) {
		t.Fatal("preload on disk differs from the embedded preload")
	}
}

func TestConfigureKeepsInheritedNodeOptions(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "")
	dataDir := t.TempDir()
	_, env, err := configure(context.Background(), acpdriver.LaunchConfig{
		DataDir: dataDir,
		Env:     map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096"},
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	got := env["NODE_OPTIONS"]
	if !strings.HasPrefix(got, "--max-old-space-size=4096 --require ") {
		t.Fatalf("NODE_OPTIONS = %q, want the inherited options kept ahead of the preload", got)
	}
	assertNetPreload(t, dataDir, got)

	// A second launch reuses the same content-named file.
	_, again, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: dataDir})
	if err != nil {
		t.Fatalf("configure again: %v", err)
	}
	if !strings.HasSuffix(got, again["NODE_OPTIONS"]) {
		t.Fatalf("second NODE_OPTIONS = %q, want the same preload as %q", again["NODE_OPTIONS"], got)
	}
}

func TestConfigureRemovesTheOldPluginFolders(t *testing.T) {
	data := t.TempDir()
	old := filepath.Join(data, "cursor", "acp-plugins", "abc", "rules")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: data, SessionID: "s1"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "cursor", "acp-plugins")); !os.IsNotExist(err) {
		t.Fatalf("old plugin folders still there: %v", err)
	}
}
