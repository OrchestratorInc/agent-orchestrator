package sessionsearch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A tool call is remembered by one argument, picked in toolArgumentKeys order.
func TestToolArgumentPicksTheRememberedField(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"description":"list files","command":"ls -la"}`, "ls -la"},
		{`{"file_path":"src/a.go","old_string":"x","new_string":"y"}`, "src/a.go"},
		{`{"pattern":"TODO","path":"src"}`, "src"},
		{`{"cmd":["bash","-lc","make test"]}`, "make test"},
		{`"{\"query\":\"flaky login\"}"`, "flaky login"},
		{`"*** Begin Patch"`, "*** Begin Patch"},
		{`{"url":"https://example.com"}`, ""},
		{`{"command":"   "}`, ""},
		{``, ""},
	}
	for _, c := range cases {
		if got := toolArgument(json.RawMessage(c.input)); got != c.want {
			t.Errorf("toolArgument(%s) = %q, want %q", c.input, got, c.want)
		}
	}
}

// The argument is cut to 2,000 characters and the output to 3,072, counted in
// characters so a multi-byte character is never split.
func TestToolTextKeepsItsCaps(t *testing.T) {
	long := strings.Repeat("é", 5000)
	call := toolCallText("Bash", long)
	if got := utf8.RuneCountInString(strings.TrimPrefix(call, "Bash: ")); got != toolArgumentChars {
		t.Fatalf("argument = %d characters, want %d", got, toolArgumentChars)
	}
	output := toolOutputText(long)
	if got := utf8.RuneCountInString(output); got != toolOutputChars || !utf8.ValidString(output) {
		t.Fatalf("output = %d characters (valid %v), want %d", got, utf8.ValidString(output), toolOutputChars)
	}
	if got := toolCallText("", "ls"); got != "ls" {
		t.Fatalf("call without a name = %q", got)
	}
	if got := toolText("Bash", "ls", "a.go"); got != "Bash: ls\n\na.go" {
		t.Fatalf("tool text = %q", got)
	}
}

// A result is read by its text parts, not every string in its JSON.
func TestToolResultTextReadsTextParts(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`"plain output"`, "plain output"},
		{`[{"type":"text","text":"first"},{"type":"image","source":{}},"second"]`, "first\nsecond"},
		{`{"content":[{"type":"text","text":"mcp result"}],"isError":false}`, "mcp result"},
		{`{"text":"direct"}`, "direct"},
		{`null`, ""},
	}
	for _, c := range cases {
		if got := toolResultText(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("toolResultText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// Sources are prepared in parallel but written one at a time in their order,
// and an apply that asks to stop stops the rest.
func TestForEachNativePlanAppliesInOrder(t *testing.T) {
	svc := New(Options{})
	targets := make([]domain.SessionSearchTarget, 40)
	for index := range targets {
		targets[index] = domain.SessionSearchTarget{
			ID: domain.SessionID("sess-" + strings.Repeat("x", index)), Harness: domain.AgentHarness("no-reader"),
		}
	}
	var seen []string
	svc.forEachNativePlan(context.Background(), targets, func(plan nativePlan) bool {
		if plan.outcome != nativeUnsupported {
			t.Fatalf("outcome = %v, want unsupported", plan.outcome)
		}
		seen = append(seen, string(plan.target.ID))
		return true
	})
	if len(seen) != len(targets) {
		t.Fatalf("applied %d plans, want %d", len(seen), len(targets))
	}
	for index, id := range seen {
		if id != string(targets[index].ID) {
			t.Fatalf("plan %d was %s, want %s", index, id, targets[index].ID)
		}
	}
	applied := 0
	svc.forEachNativePlan(context.Background(), targets, func(nativePlan) bool {
		applied++
		return applied < 5
	})
	if applied != 5 {
		t.Fatalf("applied %d plans after stopping at 5", applied)
	}
}
