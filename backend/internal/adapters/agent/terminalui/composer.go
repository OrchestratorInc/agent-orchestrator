package terminalui

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const composerLookbackLines = 8

type styledRune struct {
	value rune
	dim   bool
	bold  bool
}

func PlainTerminalText(output string) string {
	lines := styledTerminalLines(output)
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = styledString(line)
	}
	return strings.Join(plain, "\n")
}

func PlainTerminalLines(output string) []string {
	return strings.Split(PlainTerminalText(output), "\n")
}

type ComposerState uint8

const (
	ComposerUnknown ComposerState = iota
	ComposerEmpty
	ComposerDraft
)

func LastPromptIsEmptyOrDimPlaceholder(output, marker string, chromeLabels ...string) bool {
	return LastPromptComposerState(output, marker, chromeLabels...) == ComposerEmpty
}

func isPromptChromeLabel(text string, chromeLabels []string) bool {
	if len(chromeLabels) == 0 || text == "" {
		return false
	}
	for _, label := range chromeLabels {
		if text == strings.TrimSpace(label) {
			return true
		}
	}
	return false
}

func LastPromptComposerState(output, marker string, chromeLabels ...string) ComposerState {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return ComposerUnknown
	}
	lines := styledTerminalLines(output)
	start := len(lines) - composerLookbackLines
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		line := trimLeftStyledSpace(lines[i])
		markerRunes := []rune(marker)
		if len(line) < len(markerRunes) || styledString(line[:len(markerRunes)]) != marker {
			continue
		}
		if !isPromptChromeLabel(strings.TrimSpace(styledString(line[len(markerRunes):])), chromeLabels) {
			for _, r := range line[len(markerRunes):] {
				if unicode.IsSpace(r.value) {
					continue
				}
				if !r.dim {
					return ComposerDraft
				}
			}
		}
		for j := i + 1; j < len(lines); j++ {
			continuation := lines[j]
			if horizontalRuleWidth(continuation) > 0 {
				return ComposerEmpty
			}
			if isPromptChromeLabel(strings.TrimSpace(styledString(continuation)), chromeLabels) {
				continue
			}
			for _, r := range continuation {
				if unicode.IsSpace(r.value) {
					continue
				}
				if !r.dim {
					return ComposerDraft
				}
			}
		}
		return ComposerEmpty
	}
	return ComposerUnknown
}

func LastPromptHasBoldMarker(output, marker string) bool {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return false
	}
	markerRunes := []rune(marker)
	lines := styledTerminalLines(output)
	start := len(lines) - composerLookbackLines
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		line := trimLeftStyledSpace(lines[i])
		if len(line) < len(markerRunes) || styledString(line[:len(markerRunes)]) != marker {
			continue
		}
		for _, r := range line[:len(markerRunes)] {
			if !r.bold || r.dim {
				return false
			}
		}
		return true
	}
	return false
}

func LastBorderedPromptIsEmptyOrDimPlaceholder(output, marker string, chromeLabels ...string) bool {
	return LastBorderedPromptComposerState(output, marker, chromeLabels...) == ComposerEmpty
}

func LastBorderedPromptComposerState(output, marker string, chromeLabels ...string) ComposerState {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return ComposerUnknown
	}
	lines := styledTerminalLines(output)
	markerRunes := []rune(marker)
	start := len(lines) - composerLookbackLines
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		line := trimLeftStyledSpace(lines[i])
		if len(line) < len(markerRunes) || styledString(line[:len(markerRunes)]) != marker {
			continue
		}
		upperWidth := 0
		for j := i - 1; j >= 0 && upperWidth == 0; j-- {
			upperWidth = horizontalRuleWidth(lines[j])
		}
		lowerIndex, lowerWidth := -1, 0
		for j := len(lines) - 1; j > i; j-- {
			if lowerWidth = horizontalRuleWidth(lines[j]); lowerWidth > 0 {
				lowerIndex = j
				break
			}
		}
		if upperWidth == 0 || lowerIndex < 0 || upperWidth != lowerWidth {
			return ComposerUnknown
		}
		if !isPromptChromeLabel(strings.TrimSpace(styledString(line[len(markerRunes):])), chromeLabels) {
			for _, r := range line[len(markerRunes):] {
				if !unicode.IsSpace(r.value) && !r.dim {
					return ComposerDraft
				}
			}
		}
		for _, continuation := range lines[i+1 : lowerIndex] {
			if isPromptChromeLabel(strings.TrimSpace(styledString(continuation)), chromeLabels) {
				continue
			}
			for _, r := range continuation {
				if !unicode.IsSpace(r.value) && !r.dim {
					return ComposerDraft
				}
			}
		}
		return ComposerEmpty
	}
	return ComposerUnknown
}

func horizontalRuleWidth(line []styledRune) int {
	for len(line) > 0 && unicode.IsSpace(line[0].value) {
		line = line[1:]
	}
	for len(line) > 0 && unicode.IsSpace(line[len(line)-1].value) {
		line = line[:len(line)-1]
	}
	if len(line) < 16 {
		return 0
	}
	for _, r := range line {
		if r.value != '─' {
			return 0
		}
	}
	return len(line)
}

func styledTerminalLines(output string) [][]styledRune {
	output = strings.ReplaceAll(output, "\r", "\n")
	lines := make([][]styledRune, 0, 1)
	lines = append(lines, nil)
	dim := false
	bold := false
	for i := 0; i < len(output); {
		if output[i] == '\x1b' {
			if next, params, sgr := consumeEscape(output, i); next > i {
				if sgr {
					dim, bold = applySGR(dim, bold, params)
				}
				i = next
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(output[i:])
		i += size
		if r == '\n' {
			lines = append(lines, nil)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], styledRune{value: r, dim: dim, bold: bold})
	}
	return lines
}

func consumeEscape(output string, start int) (next int, params string, sgr bool) {
	if start+1 >= len(output) {
		return start + 1, "", false
	}
	switch output[start+1] {
	case '[':
		for i := start + 2; i < len(output); i++ {
			b := output[i]
			if b < 0x40 || b > 0x7e {
				continue
			}
			return i + 1, output[start+2 : i], b == 'm'
		}
		return len(output), "", false
	case ']':
		for i := start + 2; i < len(output); i++ {
			if output[i] == '\a' {
				return i + 1, "", false
			}
			if output[i] == '\x1b' && i+1 < len(output) && output[i+1] == '\\' {
				return i + 2, "", false
			}
		}
		return len(output), "", false
	default:
		return min(start+2, len(output)), "", false
	}
}

func applySGR(dim, bold bool, params string) (bool, bool) {
	if params == "" {
		return false, false
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		raw := fields[i]
		if colon := strings.IndexByte(raw, ':'); colon >= 0 {
			raw = raw[:colon]
		}
		code, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		if code == 38 || code == 48 || code == 58 {
			if strings.Contains(fields[i], ":") || i+1 >= len(fields) {
				continue
			}
			switch fields[i+1] {
			case "5":
				i = min(i+2, len(fields)-1)
			case "2":
				i = min(i+4, len(fields)-1)
			}
			continue
		}
		switch code {
		case 0, 22:
			dim, bold = false, false
		case 1:
			bold = true
		case 2:
			dim = true
		}
	}
	return dim, bold
}

func trimLeftStyledSpace(line []styledRune) []styledRune {
	for len(line) > 0 && unicode.IsSpace(line[0].value) {
		line = line[1:]
	}
	return line
}

func styledString(line []styledRune) string {
	var b strings.Builder
	for _, r := range line {
		b.WriteRune(r.value)
	}
	return b.String()
}
