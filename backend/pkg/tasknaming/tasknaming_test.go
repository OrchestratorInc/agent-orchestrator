package tasknaming

import (
	"context"
	"strings"
	"testing"
)

func TestWaitKeepsTimelyAnswerAfterPreparationOutlastsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Name, 1)
	done <- Name{Slug: "generated-name"}
	cancel()
	if got := Wait(ctx, done, Name{Slug: "fallback"}); got.Slug != "generated-name" {
		t.Fatalf("name = %+v", got)
	}
}

func TestWaitDoesNotBlockOnLateProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Wait(ctx, make(chan Name), Name{Slug: "fallback"}); got.Slug != "fallback" {
		t.Fatalf("name = %+v", got)
	}
}

func TestParseTaskName(t *testing.T) {
	fallback := Name{Slug: "fallback"}
	for _, tt := range []struct{ raw, slug string }{
		{`{"title":"Fix redirect","slug":"Fix / Login..Redirect"}`, "fix-login-redirect"},
		{"```json\n{\"title\":\"Fix redirect\",\"slug\":\"fix-login\"}\n```", "fix-login"},
		{`{"slug":"../../"}`, "fallback"},
		{`{"slug":"one two three four five six seven"}`, "one-two-three"},
		{`{"slug":"` + strings.Repeat("a", 100) + `"}`, strings.Repeat("a", 24)},
		{`{"slug":"vaporwave-site-redesign"}`, "vaporwave-site-redesign"},
		{`broken`, "fallback"},
	} {
		if got := Parse(tt.raw, fallback); got.Slug != tt.slug {
			t.Errorf("parse %q = %+v, want %q", tt.raw, got, tt.slug)
		}
	}
}

func TestParseTaskNameTitles(t *testing.T) {
	fallback := Name{Title: "Fallback title", Slug: "fallback"}
	for _, tt := range []struct {
		name, raw string
		want      Name
	}{
		{"whitespace and controls", `{"title":"  Fix\n\tlogin\u0000\u001b redirect  ","slug":"LOGIN / Redirect"}`, Name{Title: "Fix login redirect", Slug: "login-redirect"}},
		{"unicode title", `{"title":"修复登录","slug":"login"}`, Name{Title: "修复登录", Slug: "login"}},
		{"rune limit", `{"title":"` + strings.Repeat("界", 101) + `","slug":"login"}`, Name{Title: strings.Repeat("界", 100), Slug: "login"}},
		{"punctuation title and unusable slug", `{"title":"...","slug":"你好"}`, Name{Slug: "fallback"}},
		{"missing title", `{"slug":"login"}`, Name{Slug: "login"}},
		{"malformed JSON", `broken`, fallback},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.raw, fallback); got != tt.want {
				t.Fatalf("name = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func BenchmarkTaskNameParsing(b *testing.B) {
	fallback := Name{Slug: "fix-login-redirect"}
	for b.Loop() {
		Parse(`{"title":"Preserve destination after login","slug":"fix-login-redirect"}`, fallback)
	}
}

func TestFallback(t *testing.T) {
	for _, tt := range []struct{ prompt, id, slug string }{
		{"Fix login redirect after expiry", "task-1", "fix-login-redirect"},
		{"你好", "TASK_123", "task-123"},
		{"", "task-123", "task-123"},
		{"...", "...", ""},
	} {
		if got := Fallback(tt.prompt, tt.id); got != (Name{Slug: tt.slug}) {
			t.Errorf("Fallback(%q, %q) = %+v, want slug %q", tt.prompt, tt.id, got, tt.slug)
		}
	}
}
