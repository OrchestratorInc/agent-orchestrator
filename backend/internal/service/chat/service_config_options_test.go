package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSettingsFromConfigOptionsKeepsClaudeModelAndEffortAcrossRestart(t *testing.T) {
	settings, changed := settingsFromConfigOptions(domain.ConversationSettings{
		ApprovalMode: domain.PermissionModeBypassPermissions,
	}, []ports.ChatConfigOption{
		{ID: "model", Category: "model", Current: ports.ChatConfigOptionValue{Select: "sonnet"}},
		{ID: "effort", Category: "thought_level", Current: ports.ChatConfigOptionValue{Select: "high"}},
	})
	if !changed {
		t.Fatal("settings should change")
	}
	if settings.Model != "sonnet" || settings.ReasoningEffort != "high" || settings.ApprovalMode != domain.PermissionModeBypassPermissions {
		t.Fatalf("settings = %+v, want model and effort while preserving approval", settings)
	}
}

func TestPermissionConfigOptions(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  domain.PermissionMode
	}{{"manual", domain.PermissionModeDefault}, {"default", domain.PermissionModeDefault}, {"acceptEdits", domain.PermissionModeAcceptEdits}, {"auto", domain.PermissionModeAuto}, {"bypassPermissions", domain.PermissionModeBypassPermissions}, {"dontAsk", ""}, {"plan", ""}, {"custom", ""}} {
		input := []ports.ChatConfigOption{{ID: "mode", Current: ports.ChatConfigOptionValue{Select: tc.value}, Choices: []ports.ChatConfigOptionChoice{{Value: tc.value}}}}
		got := permissionConfigOptions(domain.HarnessClaudeCode, input)
		if got[0].Choices[0].PermissionMode != tc.want {
			t.Fatalf("%s mapping = %q", tc.value, got[0].Choices[0].PermissionMode)
		}
		settings, _ := settingsFromConfigOptions(domain.ConversationSettings{ApprovalMode: domain.PermissionModeAuto}, got)
		want := tc.want
		if want == "" {
			want = domain.PermissionModeAuto
		}
		if settings.ApprovalMode != want {
			t.Fatalf("%s settings=%q", tc.value, settings.ApprovalMode)
		}
		if input[0].Choices[0].PermissionMode != "" {
			t.Fatal("mutated provider catalog")
		}
		if other := permissionConfigOptions(domain.HarnessOpenCode, input); other[0].Choices[0].PermissionMode != "" {
			t.Fatal("mapped unknown provider")
		}
		input[0].ID = "model"
		if model := permissionConfigOptions(domain.HarnessClaudeCode, input); model[0].Choices[0].PermissionMode != "" {
			t.Fatal("mapped model choice")
		}
	}
}

func TestPermissionConfigOptionsMapsCommandCodeModes(t *testing.T) {
	input := []ports.ChatConfigOption{{
		ID:      "mode",
		Current: ports.ChatConfigOptionValue{Select: "default"},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: "default", Name: "Default"},
			{Value: "auto-accept", Name: "Auto Accept"},
			{Value: "plan", Name: "Plan"},
			{Value: "dont-ask", Name: "Don't Ask"},
			{Value: "bypass", Name: "Bypass"},
		},
	}}

	got := permissionConfigOptions(domain.HarnessCommandCode, input)
	want := []struct {
		value string
		mode  domain.PermissionMode
	}{
		{"default", domain.PermissionModeDefault},
		{"auto-accept", domain.PermissionModeAuto},
		{"bypass", domain.PermissionModeBypassPermissions},
	}
	if len(got) != 1 || len(got[0].Choices) != len(want) {
		t.Fatalf("choices = %+v, want only AO-compatible Command Code modes", got)
	}
	for i, choice := range got[0].Choices {
		if choice.Value != want[i].value || choice.PermissionMode != want[i].mode {
			t.Errorf("choice[%d] = (%q, %q), want (%q, %q)",
				i, choice.Value, choice.PermissionMode, want[i].value, want[i].mode)
		}
	}
	settings, _ := settingsFromConfigOptions(
		domain.ConversationSettings{ApprovalMode: domain.PermissionModeBypassPermissions}, got)
	if settings.ApprovalMode != domain.PermissionModeDefault {
		t.Fatalf("selecting native default kept approval = %q, want %q",
			settings.ApprovalMode, domain.PermissionModeDefault)
	}
	if len(input[0].Choices) != 5 {
		t.Fatal("mutated provider catalog")
	}
}

func TestPermissionConfigOptionsLabelsOpenCodeTiers(t *testing.T) {
	// AO injects these as OpenCode agents; build and plan are OpenCode's own
	// execution modes and must stay out of the approvals vocabulary.
	for _, harness := range []domain.AgentHarness{domain.HarnessOpenCode, domain.HarnessOpenCodeV2} {
		for _, tc := range []struct {
			value string
			mode  domain.PermissionMode
			label string
		}{
			{"ao-default", domain.PermissionModeDefault, "Use agent permissions"},
			{"ao-accept-edits", domain.PermissionModeAcceptEdits, "Accept edits"},
			{"ao-auto", domain.PermissionModeAuto, "Auto-approve"},
			{"ao-bypass", domain.PermissionModeBypassPermissions, "Bypass permissions"},
			{"build", "", "build"},
			{"plan", "", "plan"},
		} {
			t.Run(string(harness)+"/"+tc.value, func(t *testing.T) {
				input := []ports.ChatConfigOption{{
					ID:      "mode",
					Current: ports.ChatConfigOptionValue{Select: tc.value},
					Choices: []ports.ChatConfigOptionChoice{{Value: tc.value, Name: tc.value}},
				}}
				got := permissionConfigOptions(harness, input)
				if got[0].Choices[0].PermissionMode != tc.mode || got[0].Choices[0].Name != tc.label {
					t.Fatalf("%s -> (%q, %q), want (%q, %q)",
						tc.value, got[0].Choices[0].PermissionMode, got[0].Choices[0].Name, tc.mode, tc.label)
				}
				settings, _ := settingsFromConfigOptions(
					domain.ConversationSettings{ApprovalMode: domain.PermissionModeDefault}, got)
				want := tc.mode
				if want == "" {
					want = domain.PermissionModeDefault
				}
				if settings.ApprovalMode != want {
					t.Fatalf("%s settings = %q, want %q", tc.value, settings.ApprovalMode, want)
				}
				if input[0].Choices[0].Name != tc.value {
					t.Fatal("mutated provider catalog")
				}
			})
		}
	}
}

// Copilot's catalog exactly as copilot --acp 1.0.93 advertises it.
func copilotCatalog() []ports.ChatConfigOption {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	return []ports.ChatConfigOption{
		{ID: "mode", Name: "Mode", Category: "mode", Type: ports.ChatConfigOptionSelect,
			Current: ports.ChatConfigOptionValue{Select: uri + "agent"},
			Choices: []ports.ChatConfigOptionChoice{
				{Value: uri + "agent", Name: "Agent"}, {Value: uri + "plan", Name: "Plan"},
				{Value: uri + "autopilot", Name: "Autopilot"},
			}},
		{ID: "agent", Name: "Agent", Type: ports.ChatConfigOptionSelect,
			Choices: []ports.ChatConfigOptionChoice{{Value: ""}, {Value: "ao-s1"}}},
		{ID: "allow_all", Name: "Allow all", Type: ports.ChatConfigOptionSelect,
			Current: ports.ChatConfigOptionValue{Select: "off"},
			Choices: []ports.ChatConfigOptionChoice{{Value: "on"}, {Value: "off"}}},
	}
}

func TestPermissionConfigOptionsHidesCopilotApprovalBypasses(t *testing.T) {
	got := permissionConfigOptions(domain.HarnessCopilot, copilotCatalog())
	ids := map[string]ports.ChatConfigOption{}
	for _, option := range got {
		ids[option.ID] = option
	}
	if _, ok := ids["allow_all"]; ok {
		t.Error("allow_all is driven by AO's approval mode and must not be a composer control")
	}
	mode, ok := ids["mode"]
	if !ok {
		t.Fatal("mode option dropped; Agent and Plan are execution modes")
	}
	var names []string
	for _, choice := range mode.Choices {
		names = append(names, choice.Name)
		if choice.PermissionMode != "" {
			t.Errorf("%s annotated as approval mode %q", choice.Name, choice.PermissionMode)
		}
	}
	if len(names) != 2 || names[0] != "Agent" || names[1] != "Plan" {
		t.Errorf("mode choices = %v, want [Agent Plan]", names)
	}
	if _, ok := ids["agent"]; !ok {
		t.Error("agent option dropped from the catalog")
	}
	if len(permissionConfigOptions(domain.HarnessCodex, copilotCatalog())) != 3 {
		t.Error("another harness lost options to the Copilot filter")
	}
}

func TestCopilotConfigOptionAllowed(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	for _, tc := range []struct {
		id, value string
		ok        bool
	}{
		{"allow_all", "on", false},
		{"allow_all", "off", false},
		{"mode", uri + "autopilot", false},
		{"mode", uri + "plan", true},
		{"mode", uri + "agent", true},
		{"model", "gpt-5.5", true},
	} {
		err := copilotConfigOptionAllowed(tc.id, ports.ChatConfigOptionValue{Select: tc.value})
		if tc.ok && err != nil {
			t.Errorf("%s=%s: %v", tc.id, tc.value, err)
		}
		if !tc.ok && !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Errorf("%s=%s: err = %v, want ErrChatConfigOptionInvalid", tc.id, tc.value, err)
		}
	}
}

type copilotTestConversation struct {
	ports.ChatConversation
	options           []ports.ChatConfigOption
	setErr            error
	retainMode        bool
	omitSetterCatalog bool
}

func (c *copilotTestConversation) ListConfigOptions(context.Context) ([]ports.ChatConfigOption, error) {
	return append([]ports.ChatConfigOption(nil), c.options...), nil
}

func (c *copilotTestConversation) SetConfigOption(_ context.Context, id string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error) {
	if c.setErr != nil {
		return nil, c.setErr
	}
	if id == "mode" && c.retainMode {
		return append([]ports.ChatConfigOption(nil), c.options...), nil
	}
	for i := range c.options {
		if c.options[i].ID == id {
			c.options[i].Current = value
		}
	}
	if c.omitSetterCatalog {
		return nil, nil
	}
	return append([]ports.ChatConfigOption(nil), c.options...), nil
}

func TestSanitizeCopilotLoadedModeResetsAutopilotToAgent(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	catalog := copilotCatalog()
	for i := range catalog {
		if catalog[i].ID == "mode" {
			catalog[i].Current = ports.ChatConfigOptionValue{Select: uri + "autopilot"}
		}
	}
	conv := &copilotTestConversation{options: catalog}
	if err := sanitizeCopilotLoadedMode(context.Background(), conv); err != nil {
		t.Fatalf("sanitizeCopilotLoadedMode failed: %v", err)
	}
	for _, opt := range conv.options {
		if opt.ID == "mode" && opt.Current.Select != uri+"agent" {
			t.Fatalf("provider mode = %q, want %q", opt.Current.Select, uri+"agent")
		}
	}
}

func TestSanitizeCopilotLoadedModePreservesPlanMode(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	catalog := copilotCatalog()
	for i := range catalog {
		if catalog[i].ID == "mode" {
			catalog[i].Current = ports.ChatConfigOptionValue{Select: uri + "plan"}
		}
	}
	conv := &copilotTestConversation{options: catalog}
	if err := sanitizeCopilotLoadedMode(context.Background(), conv); err != nil {
		t.Fatalf("sanitizeCopilotLoadedMode failed: %v", err)
	}
	for _, opt := range conv.options {
		if opt.ID == "mode" && opt.Current.Select != uri+"plan" {
			t.Fatalf("provider mode = %q, want %q", opt.Current.Select, uri+"plan")
		}
	}
}

func TestSanitizeCopilotLoadedModeFailsWhenModeOptionIsMissingOnResume(t *testing.T) {
	conv := &copilotTestConversation{options: []ports.ChatConfigOption{{ID: "model"}}}
	if err := sanitizeCopilotLoadedMode(context.Background(), conv); err == nil {
		t.Fatal("sanitizeCopilotLoadedMode succeeded without a mode option on resume")
	}
}

func TestSanitizeCopilotLoadedModeFailsWhenNoSafeFallbackExists(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	catalog := copilotCatalog()
	for i := range catalog {
		if catalog[i].ID == "mode" {
			catalog[i].Current = ports.ChatConfigOptionValue{Select: uri + "autopilot"}
			catalog[i].Choices = []ports.ChatConfigOptionChoice{{Value: "unknown", Name: "Unknown"}}
		}
	}
	conv := &copilotTestConversation{options: catalog}
	if err := sanitizeCopilotLoadedMode(context.Background(), conv); err == nil {
		t.Fatal("sanitizeCopilotLoadedMode succeeded without a known safe mode")
	}
}

func TestSanitizeCopilotLoadedModeFailsWhenProviderRetainsAutopilot(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	catalog := copilotCatalog()
	for i := range catalog {
		if catalog[i].ID == "mode" {
			catalog[i].Current = ports.ChatConfigOptionValue{Select: uri + "autopilot"}
		}
	}
	conv := &copilotTestConversation{options: catalog, retainMode: true}
	if err := sanitizeCopilotLoadedMode(context.Background(), conv); err == nil {
		t.Fatal("sanitizeCopilotLoadedMode succeeded when provider retained Autopilot; want error")
	}
}

func TestSanitizeCopilotLoadedModeFailsWhenResetRejected(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	catalog := copilotCatalog()
	for i := range catalog {
		if catalog[i].ID == "mode" {
			catalog[i].Current = ports.ChatConfigOptionValue{Select: uri + "autopilot"}
		}
	}
	conv := &copilotTestConversation{
		options: catalog,
		setErr:  errors.New("provider rejected mode change"),
	}
	if err := sanitizeCopilotLoadedMode(context.Background(), conv); err == nil {
		t.Fatal("sanitizeCopilotLoadedMode succeeded when provider rejected reset; want error")
	}
}

func TestSanitizeCopilotLoadedModeFailsWhenSetterOmitsCatalog(t *testing.T) {
	const uri = "https://agentclientprotocol.com/protocol/session-modes#"
	catalog := copilotCatalog()
	for i := range catalog {
		if catalog[i].ID == "mode" {
			catalog[i].Current = ports.ChatConfigOptionValue{Select: uri + "autopilot"}
		}
	}
	// When the setter returns no catalog, SetConfigOption returns nil options.
	// Even though the accepted value is preserved in the local cache, the safety
	// reset must fail closed because the provider never reported the mode.
	conv := &copilotTestConversation{options: catalog, omitSetterCatalog: true}
	err := sanitizeCopilotLoadedMode(context.Background(), conv)
	if err == nil {
		t.Fatal("sanitizeCopilotLoadedMode succeeded when setter omitted catalog; want error")
	}
	if !strings.Contains(err.Error(), "provider did not report the resulting mode") {
		t.Fatalf("err = %v, want 'provider did not report the resulting mode'", err)
	}
}
func TestPermissionConfigOptionsMapsAgyModesOntoApprovalTiers(t *testing.T) {
	input := []ports.ChatConfigOption{{
		ID:      "mode",
		Current: ports.ChatConfigOptionValue{Select: "yolo"},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: "default", Name: "Default"},
			{Value: "auto_edit", Name: "Auto Edit"},
			{Value: "yolo", Name: "YOLO"},
		},
	}}
	got := permissionConfigOptions(domain.HarnessAgy, input)
	want := []struct {
		mode domain.PermissionMode
		name string
	}{
		{domain.PermissionModeDefault, "Use agent permissions"},
		{domain.PermissionModeAcceptEdits, "Accept edits"},
		{domain.PermissionModeAuto, "Auto-approve"},
	}
	for i, choice := range got[0].Choices {
		if choice.PermissionMode != want[i].mode || choice.Name != want[i].name {
			t.Fatalf("choice %q = (%q, %q), want (%q, %q)", choice.Value, choice.PermissionMode, choice.Name, want[i].mode, want[i].name)
		}
	}
	settings, _ := settingsFromConfigOptions(domain.ConversationSettings{}, got)
	if settings.ApprovalMode != domain.PermissionModeAuto {
		t.Fatalf("settings approval = %q, want auto for yolo", settings.ApprovalMode)
	}
	if input[0].Choices[2].Name != "YOLO" {
		t.Fatal("mutated provider catalog")
	}
}
