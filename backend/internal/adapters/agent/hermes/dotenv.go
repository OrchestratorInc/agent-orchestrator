package hermes

import (
	"bytes"
	"os"
	"regexp"
	"strings"
)

// The grammar of the python-dotenv release Hermes bundles to load
// <HERMES_HOME>/.env (dotenv/parser.py), so AO sees exactly the assignments
// Hermes applies: quoted keys, `export` followed by any blank, and quoted
// values that span lines. Each pattern is anchored at the reader position.
var (
	dotenvMultilineSpace    = regexp.MustCompile(`^\s*`)
	dotenvExport            = regexp.MustCompile(`^(?:export[^\S\r\n]+)?`)
	dotenvSingleQuotedKey   = regexp.MustCompile(`^'([^']+)'`)
	dotenvUnquotedKey       = regexp.MustCompile(`^([^=#\s]+)`)
	dotenvWhitespace        = regexp.MustCompile(`^[^\S\r\n]*`)
	dotenvEqualSign         = regexp.MustCompile(`^(=[^\S\r\n]*)`)
	dotenvSingleQuotedValue = regexp.MustCompile(`(?s)^'((?:\\.|[^'\\])*)'`)
	dotenvDoubleQuotedValue = regexp.MustCompile(`(?s)^"((?:\\.|[^"\\])*)"`)
	dotenvUnquotedValue     = regexp.MustCompile(`^([^\r\n]*)`)
	dotenvComment           = regexp.MustCompile(`^(?:[^\S\r\n]*#[^\r\n]*)?`)
	dotenvEndOfLine         = regexp.MustCompile(`^[^\S\r\n]*(?:\r\n|\n|\r|$)`)
	dotenvRestOfLine        = regexp.MustCompile(`^[^\r\n]*(?:\r|\n|\r\n)?`)
	dotenvInlineComment     = regexp.MustCompile(`\s+#.*`)
)

type dotenvBinding struct {
	key   string
	value string
}

// dotenvBindings parses a dotenv file the way Hermes's loader does. Hermes
// strips a UTF-8 BOM before parsing; a missing or unreadable file has none.
// Values are returned only so callers can test emptiness, never to log.
func dotenvBindings(path string) []dotenvBinding {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")))
	var bindings []dotenvBinding
	for text != "" {
		var binding dotenvBinding
		var ok bool
		text, binding, ok = parseDotenvBinding(text)
		if ok && binding.key != "" {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// parseDotenvBinding mirrors parse_binding: on a syntax error it discards the
// rest of the current line, as python-dotenv does.
func parseDotenvBinding(text string) (string, dotenvBinding, bool) {
	read := func(re *regexp.Regexp) ([]string, bool) {
		m := re.FindStringSubmatch(text)
		if m == nil {
			return nil, false
		}
		text = text[len(m[0]):]
		return m, true
	}
	fail := func() (string, dotenvBinding, bool) {
		read(dotenvRestOfLine)
		return text, dotenvBinding{}, false
	}
	read(dotenvMultilineSpace)
	if text == "" {
		return "", dotenvBinding{}, false
	}
	read(dotenvExport)
	var binding dotenvBinding
	switch {
	case strings.HasPrefix(text, "#"):
	case strings.HasPrefix(text, "'"):
		m, ok := read(dotenvSingleQuotedKey)
		if !ok {
			return fail()
		}
		binding.key = m[1]
	default:
		m, ok := read(dotenvUnquotedKey)
		if !ok {
			return fail()
		}
		binding.key = m[1]
	}
	read(dotenvWhitespace)
	if strings.HasPrefix(text, "=") {
		read(dotenvEqualSign)
		switch {
		case strings.HasPrefix(text, "'"):
			m, ok := read(dotenvSingleQuotedValue)
			if !ok {
				return fail()
			}
			binding.value = m[1]
		case strings.HasPrefix(text, `"`):
			m, ok := read(dotenvDoubleQuotedValue)
			if !ok {
				return fail()
			}
			binding.value = m[1]
		case text == "" || text[0] == '\n' || text[0] == '\r':
		default:
			m, _ := read(dotenvUnquotedValue)
			binding.value = strings.TrimRight(dotenvInlineComment.ReplaceAllString(m[1], ""), " \t\r\n\f\v")
		}
	}
	read(dotenvComment)
	if _, ok := read(dotenvEndOfLine); !ok {
		return fail()
	}
	return text, binding, true
}
