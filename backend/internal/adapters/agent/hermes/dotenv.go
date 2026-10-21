package hermes

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Python's Unicode whitespace (str.isspace, and \s in a str pattern), which
// both Hermes's .env sanitizer and python-dotenv use. pyBlank excludes the
// line breaks, as python-dotenv's [^\S\r\n] does.
const (
	pyBlank      = `\t\x{0B}\f\x{1C}-\x{1F} \x{85}\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`
	pyWhitespace = `\n\r` + pyBlank
)

// The grammar of the python-dotenv release Hermes bundles to load
// <HERMES_HOME>/.env (dotenv/parser.py), so AO sees exactly the assignments
// Hermes applies: quoted keys, `export` followed by any blank, and quoted
// values that span lines. Each pattern is anchored at the reader position.
var (
	dotenvMultilineSpace    = regexp.MustCompile(`^[` + pyWhitespace + `]*`)
	dotenvExport            = regexp.MustCompile(`^(?:export[` + pyBlank + `]+)?`)
	dotenvSingleQuotedKey   = regexp.MustCompile(`^'([^']+)'`)
	dotenvUnquotedKey       = regexp.MustCompile(`^([^=#` + pyWhitespace + `]+)`)
	dotenvWhitespace        = regexp.MustCompile(`^[` + pyBlank + `]*`)
	dotenvEqualSign         = regexp.MustCompile(`^(=[` + pyBlank + `]*)`)
	dotenvSingleQuotedValue = regexp.MustCompile(`(?s)^'((?:\\.|[^'\\])*)'`)
	dotenvDoubleQuotedValue = regexp.MustCompile(`(?s)^"((?:\\.|[^"\\])*)"`)
	dotenvUnquotedValue     = regexp.MustCompile(`^([^\r\n]*)`)
	dotenvComment           = regexp.MustCompile(`^(?:[` + pyBlank + `]*#[^\r\n]*)?`)
	dotenvEndOfLine         = regexp.MustCompile(`^[` + pyBlank + `]*(?:\r\n|\n|\r|$)`)
	dotenvRestOfLine        = regexp.MustCompile(`^[^\r\n]*(?:\r|\n|\r\n)?`)
	dotenvInlineComment     = regexp.MustCompile(`[` + pyWhitespace + `]+#.*`)
)

type dotenvBinding struct {
	key   string
	value string
}

// dotenvBindings parses a dotenv file the way Hermes loads it: normalized by
// its sanitizer first, then parsed by python-dotenv. A missing file has no
// bindings; an unreadable or ambiguously encoded one is an error, because
// Hermes may still load assignments from it. Values are returned only so
// callers can test emptiness, never to log.
func dotenvBindings(path string) ([]dotenvBinding, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text, ok := hermesDotenvText(raw)
	if !ok {
		return nil, fmt.Errorf("hermes: %s is not valid UTF-8 or BOM-marked UTF-16; re-save it as UTF-8", path)
	}
	var bindings []dotenvBinding
	for text != "" {
		var binding dotenvBinding
		text, binding, ok = parseDotenvBinding(text)
		if ok && binding.key != "" {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

// hermesDotenvText mirrors _sanitize_env_file_if_needed followed by the
// loader's decode for files that decode cleanly: UTF-8 (optionally with a
// BOM) and UTF-16 through its BOM (Notepad "Unicode"). The sanitizer strips
// NULs and trims every non-blank, non-comment line. Anything else (UTF-32,
// malformed UTF-8 or UTF-16) is ambiguous: Hermes's replacement-character
// and latin-1 fallbacks can still apply assignments AO cannot predict, so it
// is reported as undecodable rather than as having no assignments.
func hermesDotenvText(raw []byte) (string, bool) {
	var text string
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE, 0x00, 0x00}), bytes.HasPrefix(raw, []byte{0x00, 0x00, 0xFE, 0xFF}):
		return "", false
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		if len(raw)%2 != 0 {
			return "", false
		}
		units := make([]uint16, 0, len(raw)/2-1)
		for i := 2; i+1 < len(raw); i += 2 {
			if raw[0] == 0xFF {
				units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
			} else {
				units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
			}
		}
		for i := 0; i < len(units); i++ {
			// A lone surrogate fails Python's strict UTF-16 decode.
			if utf16.IsSurrogate(rune(units[i])) {
				if i+1 >= len(units) || utf16.DecodeRune(rune(units[i]), rune(units[i+1])) == utf8.RuneError {
					return "", false
				}
				i++
			}
		}
		// Hermes rewrites the decoded text as UTF-8 and its loader then reads
		// it as utf-8-sig, which drops a U+FEFF that survived the decode.
		text = strings.TrimPrefix(string(utf16.Decode(units)), "\ufeff")
	default:
		raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
		if !utf8.Valid(raw) {
			return "", false
		}
		text = string(raw)
	}
	text = strings.ReplaceAll(text, "\x00", "")
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	if last := len(lines) - 1; lines[last] == "" {
		lines = lines[:last]
	}
	var b strings.Builder
	for _, line := range lines {
		if stripped := strings.TrimFunc(line, pyIsSpace); stripped != "" && !strings.HasPrefix(stripped, "#") {
			line = stripped
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), true
}

// dotenvEnvironment is the environment the bindings leave behind: the last
// assignment of each name wins, as when Hermes applies them with override.
// Names are keyed by nativeEnvName.
func dotenvEnvironment(bindings []dotenvBinding) map[string]string {
	env := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		env[nativeEnvName(binding.key)] = binding.value
	}
	return env
}

// nativeEnvName is the variable a dotenv key sets in Hermes's process. A
// leading U+FEFF can be dropped by a later utf-8-sig read once Hermes rewrites
// the file, and Windows os.environ upper-cases names.
func nativeEnvName(key string) string {
	key = strings.TrimLeft(key, "\ufeff")
	if runtime.GOOS == "windows" {
		key = strings.ToUpper(key)
	}
	return key
}

// pyIsSpace is Python's str.isspace: Unicode White_Space plus the ASCII
// separators U+001C..U+001F.
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || r >= 0x1C && r <= 0x1F
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
			binding.value = strings.TrimRightFunc(dotenvInlineComment.ReplaceAllString(m[1], ""), pyIsSpace)
		}
	}
	read(dotenvComment)
	if _, ok := read(dotenvEndOfLine); !ok {
		return fail()
	}
	return text, binding, true
}
