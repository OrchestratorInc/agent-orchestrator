package agyacp

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// launchInput is an invoke_subagent call's input as agy 1.5.15 sends it.
func launchInput(typeName string) map[string]any {
	return map[string]any{
		"Model": "inherit", "Prompt": "Read a.txt and report its exact contents back to me.",
		"Role": "Codebase Researcher", "TypeName": typeName, "Workspace": "inherit",
	}
}

func TestSubagentLaunchRecognisesInvokeSubagent(t *testing.T) {
	launch, ok := subagentLaunch(acpdriver.ChildToolCall{
		Title: "Run invoke_subagent", Kind: acpsdk.ToolKindOther, RawInput: launchInput("research"),
	})
	want := acpdriver.SubagentLaunch{
		ToolName: "invoke_subagent", Title: "Codebase Researcher",
		Prompt: "Read a.txt and report its exact contents back to me.", AgentType: "research",
	}
	if !ok || launch != want {
		t.Fatalf("launch = %+v, %v", launch, ok)
	}
	// The default type inherits the parent's setup and is not worth naming.
	if launch, _ := subagentLaunch(acpdriver.ChildToolCall{Title: "Run invoke_subagent", RawInput: launchInput("self")}); launch.AgentType != "" {
		t.Fatalf("default type shown as %q", launch.AgentType)
	}
	for name, call := range map[string]acpdriver.ChildToolCall{
		"another tool": {Title: "File a.txt", Kind: acpsdk.ToolKindRead, RawInput: map[string]any{"Path": "a.txt"}},
		"no prompt":    {Title: "Run invoke_subagent", RawInput: map[string]any{"TypeName": "research"}},
		"prompt alone": {Title: "Run something", RawInput: map[string]any{"Prompt": "hi"}},
		"no raw input": {Title: "Run invoke_subagent"},
		"wrong type":   {Title: "Run invoke_subagent", RawInput: "Prompt"},
	} {
		if _, ok := subagentLaunch(call); ok {
			t.Errorf("%s read as a launch", name)
		}
	}
}

func TestSubagentReportIsASendMessageToTheLauncher(t *testing.T) {
	call := acpdriver.ChildToolCall{Title: "Run send_message", RawInput: map[string]any{"Message": "alpha", "Recipient": "parent-1"}}
	if report, ok := subagentReport(call, "parent-1"); !ok || report != "alpha" {
		t.Fatalf("report = %q, %v", report, ok)
	}
	if _, ok := subagentReport(call, "someone-else"); ok {
		t.Fatal("a message to another session read as the report")
	}
	if _, ok := subagentReport(call, ""); ok {
		t.Fatal("a child with no known launcher read as reporting")
	}
	if _, ok := subagentReport(acpdriver.ChildToolCall{Title: "File a.txt", RawInput: map[string]any{"Recipient": "parent-1"}}, "parent-1"); ok {
		t.Fatal("another tool read as the report")
	}
}

// writeBrain lays out agy's records for a parent that launched one child, as
// agy 1.5.15 writes them.
func writeBrain(t *testing.T, home, parent, launch, child string) {
	t.Helper()
	root := filepath.Join(home, "antigravity-acp", "brain")
	files := map[string]string{
		filepath.Join(root, parent, ".system_generated", "subagents", child+".json"): `{"conversationId":"` + child +
			`","subagentDescriptor":{"typeName":"research","role":"Codebase Researcher"},"state":"SUBAGENT_STATE_ALIVE","spawnStepIndex":4}`,
		filepath.Join(root, child, ".system_generated", "messages", "read.json"): `{"m1":true}`,
		// A later message, from another sender, sorts first and must not be taken.
		filepath.Join(root, child, ".system_generated", "messages", "0aaa.json"): `{"id":"0aaa","recipient":"` + child +
			`","sender":"someone","content":"hi","sourceMetadata":{"tool":{"conversationId":"someone","toolCall":{"id":"call-other","name":"send_message"}}}}`,
		filepath.Join(root, child, ".system_generated", "messages", "373811ca.json"): `{"id":"373811ca","recipient":"` + child +
			`","sender":"` + parent + `","priority":"MESSAGE_PRIORITY_HIGH","renderDetails":{"messageTitle":"Message from Parent Agent"},` +
			`"content":"Read a.txt","sourceMetadata":{"tool":{"conversationId":"` + parent + `","stepIndex":3,` +
			`"toolCall":{"id":"` + launch + `","name":"invoke_subagent","argumentsJson":"{}"}}}}`,
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBrainNamesEachChildAndItsLaunch(t *testing.T) {
	home := t.TempDir()
	writeBrain(t, home, "parent-1", "call_9043167176488376096", "bb5931af-659e-4455-be8c-dceb1c05d730")
	b := brain{home: func(env map[string]string) (string, error) { return env["GEMINI_HOME"], nil }}
	env := map[string]string{"GEMINI_HOME": home}

	if got := b.children(env, "parent-1"); !slices.Equal(got, []string{"bb5931af-659e-4455-be8c-dceb1c05d730"}) {
		t.Fatalf("children = %v", got)
	}
	parent, launch, ok := b.launchOf(env, "bb5931af-659e-4455-be8c-dceb1c05d730")
	if !ok || parent != "parent-1" || launch != "call_9043167176488376096" {
		t.Fatalf("launchOf = %q %q %v", parent, launch, ok)
	}
	if got := b.children(env, "bb5931af-659e-4455-be8c-dceb1c05d730"); len(got) != 0 {
		t.Fatalf("a child with no children of its own lists %v", got)
	}
	if _, _, ok := b.launchOf(env, "parent-1"); ok {
		t.Fatal("a top-level session has a launch")
	}
}

func TestBrainRefusesSessionIDsThatLeaveIt(t *testing.T) {
	home := t.TempDir()
	writeBrain(t, home, "parent-1", "call-1", "child-1")
	b := brain{home: func(map[string]string) (string, error) { return home, nil }}
	for _, session := range []string{"", "..", "../brain/parent-1", "child-1/..", "/etc", "a b"} {
		if got := b.children(nil, session); got != nil {
			t.Errorf("children(%q) = %v", session, got)
		}
		if _, _, ok := b.launchOf(nil, session); ok {
			t.Errorf("launchOf(%q) found a launch", session)
		}
	}
}

func TestBuildConfigFollowsAntigravitySubagents(t *testing.T) {
	cfg := buildConfig(nil, newResolver(nil), nil)
	if cfg.ChildSessions == nil || cfg.ChildSessions.Launch == nil || cfg.ChildSessions.Report == nil ||
		cfg.ChildSessions.Children == nil || cfg.ChildSessions.LaunchOf == nil {
		t.Fatalf("ChildSessions = %+v", cfg.ChildSessions)
	}
}
