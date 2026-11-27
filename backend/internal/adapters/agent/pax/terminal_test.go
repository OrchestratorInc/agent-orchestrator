package pax

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Frames follow pydantic_ai_pax/ui/prompt/live_prompt.py: title rule, the `❯`
// editor row, the bottom rule, and the toolbar.
const (
	rule    = "────────────────────────────────────────"
	toolbar = "openai-codex:gpt-6-astra · ~/repo · trusted · 3% context"
)

func frame(rows ...string) string { return strings.Join(rows, "\n") + "\n" }

func TestDetectTerminalActivity(t *testing.T) {
	transcript := "Pax: I updated the parser.\n"
	cases := []struct {
		name   string
		output string
		want   domain.ActivityState
		ok     bool
	}{
		{"idle", transcript + frame(rule, "❯ ", rule, toolbar), domain.ActivityIdle, true},
		{"idle with pinned row", transcript + frame(rule, "❯", rule, "pinned: deploy", toolbar), domain.ActivityIdle, true},
		{"working", transcript + frame("─ Working ⠋ 12s · Esc to interrupt ─────────", "❯ ", rule, toolbar), domain.ActivityActive, true},
		{"working with a queued draft", frame("─ Working ⠙ 3s · Esc to interrupt · Enter to queue · Alt+Enter to steer ─", "❯ next step", rule, toolbar), domain.ActivityActive, true},
		{"typed draft", frame(rule, "❯ fix the tests", rule, toolbar), "", false},
		{"boxed status", frame("┌──────────────────────┐", "│> Working ⠋ Ctrl+C to interrupt│", "└──────────────────────┘", toolbar), domain.ActivityActive, true},
		{"question", frame("Which database?", "  1. Postgres", "  2. SQLite", "↑/↓ move - number/Enter choose - Esc decline"), domain.ActivityWaitingInput, true},
		{"approval", frame("Approve?", "rm -rf build", "  1. Approve", "  2. Approve for this session", "  3. Deny", "↑/↓ move - number/Enter choose - Esc decline"), domain.ActivityBlocked, true},
		{"trust question", frame("Do you trust the files in this folder?", "  1. Yes, I trust this folder", "  2. No, keep it untrusted", "↑/↓ move - number/Enter choose - Esc decline"), domain.ActivityWaitingInput, true},
		{"chrome only in scrollback", frame(rule, "❯ ", rule, toolbar, "", "Pax printed a long answer here", "line 2", "line 3", "line 4"), "", false},
		{"shell prompt", frame("❯ pax --session-id 1234"), "", false},
	}
	p := New()
	for _, c := range cases {
		got, ok := p.DetectTerminalActivity(c.output)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: DetectTerminalActivity = %q %v, want %q %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestComposerIsEmpty(t *testing.T) {
	p := New()
	if !p.ComposerIsEmpty(frame(rule, "❯ ", rule, toolbar)) {
		t.Error("idle empty editor read as busy")
	}
	if p.ComposerIsEmpty(frame(rule, "❯ half-typed", rule, toolbar)) {
		t.Error("a draft read as empty")
	}
	if p.ComposerIsEmpty(frame("─ Working ⠋ 1s · Esc to interrupt ─", "❯ ", rule, toolbar)) {
		t.Error("a running turn read as an empty idle editor")
	}
}

func TestReadinessPatternNeedsTheEditorBox(t *testing.T) {
	hints, err := New().PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ready := frame(rule, "❯", rule, toolbar)
	if !containsAny(ready, hints.Patterns) {
		t.Errorf("patterns %q miss the drawn editor", hints.Patterns)
	}
	if containsAny(frame("~/repo on main", "❯ pax"), hints.Patterns) {
		t.Error("a shell prompt satisfied readiness")
	}
	if !containsAny(frame("Do you trust the files in this folder?"), hints.BlockingPatterns) {
		t.Error("the folder trust question does not hold delivery")
	}
}

func containsAny(output string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}
