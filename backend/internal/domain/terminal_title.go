package domain

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxTerminalTitleRunes bounds a stored terminal title, matching the session
// display name limit so a title fits everywhere a name does.
const MaxTerminalTitleRunes = 100

// NormalizeTerminalTitle turns the payload of an OSC 0/2 sequence into the
// title AO retains. The payload is program-controlled, so anything that could
// steer a later reader is removed: invalid UTF-8, control characters, and
// bidirectional overrides.
//
// Leading symbols are dropped because agent TUIs animate a status glyph in
// front of a stable title ("◐ Fix login", "⠏ repo", "✳ Claude Code"). Without
// this every spinner frame would read as a new title. The empty result means
// the program reset its title.
func NormalizeTerminalTitle(raw string) string {
	title := strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r) || isBidiControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(raw, ""))
	title = strings.TrimLeftFunc(title, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.So, r) || isSpinnerDot(r) || r == '\ufe0e' || r == '\ufe0f'
	})
	title = strings.Join(strings.Fields(title), " ")
	if runes := []rune(title); len(runes) > MaxTerminalTitleRunes {
		title = strings.TrimSpace(string(runes[:MaxTerminalTitleRunes]))
	}
	return title
}

// isSpinnerDot covers spinner frames that are punctuation or math symbols
// rather than So symbols, such as the dot in Claude Code's "·✢✳✶✻✽" cycle.
func isSpinnerDot(r rune) bool {
	return r == '·' || r == '•' || r == '∙' || r == '⋅'
}

func isBidiControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' ||
		(r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}

// genericTerminalTitles are the names programs emit when they have nothing to
// say about the work: an agent's product name or a bare shell. AO already shows
// better labels for these, so they are never worth displaying.
var genericTerminalTitles = map[string]struct{}{
	"claude": {}, "claude code": {}, "codex": {}, "codex cli": {}, "openai codex": {},
	"terminal": {}, "shell": {}, "sh": {}, "bash": {}, "zsh": {}, "fish": {}, "nu": {},
	"pwsh": {}, "powershell": {}, "cmd": {}, "cmd.exe": {}, "tmux": {},
}

var (
	userAtHostTitle = regexp.MustCompile(`^[^\s@]+@[^\s:]+(:.*)?$`)
	windowsPath     = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// MeaningfulTerminalTitle returns the part of a stored title worth displaying
// for a terminal started in workingDir, or "" when the title only restates what
// AO's own fallback already shows: a directory, a user@host prompt, a shell or
// agent product name.
//
// It runs at read time so a stored fact never depends on this policy.
func MeaningfulTerminalTitle(title, workingDir string) string {
	title = strings.TrimSpace(title)
	if base := workingDirBase(workingDir); base != "" {
		// Codex reports "<task> | <directory>" and the bare directory when idle.
		title = strings.TrimSpace(strings.TrimSuffix(title, " | "+base))
		if title == base {
			return ""
		}
	}
	if title == "" || !strings.ContainsFunc(title, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return ""
	}
	if isPathLike(title) || userAtHostTitle.MatchString(title) || isFishTitle(title) {
		return ""
	}
	if _, generic := genericTerminalTitles[strings.ToLower(strings.TrimPrefix(title, "-"))]; generic {
		return ""
	}
	return title
}

// fishTitleCommandRunes is how much of the command line fish's default
// fish_title keeps (string sub -l 20).
const fishTitleCommandRunes = 20

// isFishTitle reports fish's default title, "[host] <command> <directory>":
// an optional ssh host, up to 20 characters of the running command line, and
// the working directory from prompt_pwd. Fish emits it at every prompt and
// around every command, so it would relabel the tab for each `ls`.
func isFishTitle(title string) bool {
	if strings.HasPrefix(title, "[") {
		if end := strings.Index(title, "] "); end > 0 {
			title = title[end+2:]
		}
	}
	if isPathLike(title) {
		return true
	}
	// The directory can contain spaces, and so can the command, so try each
	// split whose command part fits in fish's limit.
	for i, r := range title {
		if r != ' ' {
			continue
		}
		if utf8.RuneCountInString(title[:i]) > fishTitleCommandRunes {
			return false
		}
		if isPathLike(title[i+1:]) {
			return true
		}
	}
	return false
}

func isPathLike(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || windowsPath.MatchString(s)
}

func workingDirBase(workingDir string) string {
	workingDir = strings.TrimRight(strings.TrimSpace(workingDir), `/\`)
	if workingDir == "" {
		return ""
	}
	if i := strings.LastIndexAny(workingDir, `/\`); i >= 0 {
		return workingDir[i+1:]
	}
	return filepath.Base(workingDir)
}
