package domain

import (
	"strings"
	"testing"
)

func TestNormalizeTerminalTitle(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"plain", "vim main.go", "vim main.go"},
		{"braille spinner", "⠏ Fix login", "Fix login"},
		{"claude glyph", "✳ Fix login", "Fix login"},
		{"claude dot frame", "· Fix login", "Fix login"},
		{"variation selector", "✳\ufe0f Fix login", "Fix login"},
		{"escape and controls", "a\x1b[31mb\x07c\x00", "a[31mbc"},
		{"bidi override", "safe\u202egnp.exe", "safegnp.exe"},
		{"whitespace runs", "  a \t\n b  ", "a b"},
		{"invalid utf-8", "ok\xffok", "okok"},
		{"reset", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeTerminalTitle(tc.raw); got != tc.want {
				t.Fatalf("NormalizeTerminalTitle(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNormalizeTerminalTitleCapsLength(t *testing.T) {
	got := NormalizeTerminalTitle(strings.Repeat("é", MaxTerminalTitleRunes+50))
	if n := len([]rune(got)); n != MaxTerminalTitleRunes {
		t.Fatalf("normalized title has %d runes, want %d", n, MaxTerminalTitleRunes)
	}
}

func TestMeaningfulTerminalTitle(t *testing.T) {
	const wd = "/Users/me/dev/agent-orchestrator"
	for _, tc := range []struct{ name, title, want string }{
		{"program title", "vim main.go", "vim main.go"},
		{"agent task", "Fix login", "Fix login"},
		{"codex task with directory", "Fix login | agent-orchestrator", "Fix login"},
		{"codex idle directory", "agent-orchestrator", ""},
		{"agent product name", "Claude Code", ""},
		{"bare shell", "-zsh", ""},
		{"path", "~/dev/agent-orchestrator", ""},
		{"user at host", "me@laptop:~/dev", ""},
		{"no letters", "...", ""},
		// fish_title: up to 20 characters of the command line, then prompt_pwd.
		{"fish prompt", "~/d/agent-orchestrator", ""},
		{"fish one-word command", "ls ~/d/agent-orchestrator", ""},
		{"fish multi-word command", "git status ~/d/agent-orchestrator", ""},
		{"fish command with a path", "cd ~/dev/foo ~/d/agent-orchestrator", ""},
		{"fish truncated command", "git commit -m fix th ~/d/agent-orchestrator", ""},
		{"fish directory with spaces", "make ~/d/My Project", ""},
		{"fish over ssh", "[devbox] npm run dev /srv/app", ""},
		{"fish idle over ssh", "[devbox] ~/app", ""},
		{"long text before a path is not fish", "Refactor the session store ~/notes", "Refactor the session store ~/notes"},
		{"bracketed program title", "[1] npm run dev", "[1] npm run dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MeaningfulTerminalTitle(tc.title, wd); got != tc.want {
				t.Fatalf("MeaningfulTerminalTitle(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}
