package chat

import (
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// extractAOSummary returns the summary an `ao summary ...` shell command sets,
// or "" when the command is something else or its text is not usable. It reads
// the arguments the way a shell would, so quotes are removed and anything after
// the first control operator (`&&`, `;`, `|`, a redirect) is not part of the
// summary. The text then goes through the same normalizer as the CLI and the
// HTTP endpoint.
func extractAOSummary(commandInput string) string {
	cmd := strings.TrimSpace(commandInput)
	rest, ok := strings.CutPrefix(cmd, "ao summary")
	if !ok || rest == "" || !unicode.IsSpace(rune(rest[0])) {
		return ""
	}
	words, ok := shellWords(rest)
	if !ok {
		return ""
	}
	return domain.NormalizeCardSummary(strings.Join(words, " "))
}

// shellWords splits the arguments of one simple command. It stops at the first
// unquoted control operator and reports false for an unterminated quote.
func shellWords(s string) ([]string, bool) {
	var (
		words   []string
		word    strings.Builder
		inWord  bool
		digitsO = true // the word so far is only digits, so it may be a redirect fd
	)
	flush := func() {
		if inWord {
			words = append(words, word.String())
		}
		word.Reset()
		inWord, digitsO = false, true
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\n':
			flush()
			return words, true
		case unicode.IsSpace(r):
			flush()
		case strings.ContainsRune("&|;<>()", r):
			// `2>&1` is a redirect, not text: drop its file-descriptor prefix.
			if inWord && digitsO && (r == '>' || r == '<') {
				word.Reset()
				inWord = false
			}
			flush()
			return words, true
		case r == '\'':
			end := indexRune(runes, i+1, '\'')
			if end < 0 {
				return nil, false
			}
			word.WriteString(string(runes[i+1 : end]))
			inWord, digitsO = true, false
			i = end
		case r == '"':
			closed := false
			for i++; i < len(runes); i++ {
				c := runes[i]
				if c == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[i+1]) {
					i++
					word.WriteRune(runes[i])
					continue
				}
				if c == '"' {
					closed = true
					break
				}
				word.WriteRune(c)
			}
			if !closed {
				return nil, false
			}
			inWord, digitsO = true, false
		case r == '\\' && i+1 < len(runes):
			i++
			word.WriteRune(runes[i])
			inWord, digitsO = true, false
		default:
			word.WriteRune(r)
			inWord = true
			if !unicode.IsDigit(r) {
				digitsO = false
			}
		}
	}
	flush()
	return words, true
}

func indexRune(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
