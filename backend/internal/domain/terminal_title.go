package domain

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxTerminalTitleRunes = 100

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

func isSpinnerDot(r rune) bool {
	return r == '·' || r == '•' || r == '∙' || r == '⋅'
}

func isBidiControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' ||
		(r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}

var genericTerminalTitles = map[string]struct{}{
	"claude": {}, "claude code": {}, "codex": {}, "codex cli": {}, "openai codex": {},
	"terminal": {}, "shell": {}, "sh": {}, "bash": {}, "zsh": {}, "fish": {}, "nu": {},
	"pwsh": {}, "powershell": {}, "cmd": {}, "cmd.exe": {}, "tmux": {},
}

var (
	userAtHostTitle = regexp.MustCompile(`^[^\s@]+@[^\s:]+(:.*)?$`)
	windowsPath     = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

func MeaningfulTerminalTitle(title, workingDir string) string {
	title = strings.TrimSpace(title)
	if base := workingDirBase(workingDir); base != "" {
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

const fishTitleCommandRunes = 20

func isFishTitle(title string) bool {
	if strings.HasPrefix(title, "[") {
		if end := strings.Index(title, "] "); end > 0 {
			title = title[end+2:]
		}
	}
	if isPathLike(title) {
		return true
	}
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
