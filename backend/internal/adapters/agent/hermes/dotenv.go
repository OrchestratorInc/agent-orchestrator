package hermes

import (
	"bytes"
	"os"
	"regexp"
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
// its sanitizer first, then parsed by python-dotenv. A missing or unreadable
// file has no bindings. Values are returned only so callers can test
// emptiness, never to log.
func dotenvBindings(path string) []dotenvBinding {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text, ok := hermesDotenvText(raw)
	if !ok {
		return nil
	}
	var bindings []dotenvBinding
	for text != "" {
		var binding dotenvBinding
		text, binding, ok = parseDotenvBinding(text)
		if ok && binding.key != "" {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// hermesDotenvText mirrors _sanitize_env_file_if_needed followed by the
// loader's decode. UTF-16 (Notepad "Unicode") is decoded through its BOM;
// UTF-32 is left untouched by Hermes and then yields no usable keys. The
// sanitizer strips NULs and trims every non-blank, non-comment line.
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
		text = string(utf16.Decode(units))
	default:
		raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
		if !utf8.Valid(raw) {
			// Hermes's sanitizer leaves the file untouched when the first line
			// fails to decode, and its loader then falls back to latin-1.
			if first, _, _ := bytes.Cut(raw, []byte("\n")); !utf8.Valid(first) {
				runes := make([]rune, len(raw))
				for i, b := range raw {
					runes[i] = rune(b)
				}
				return string(runes), true
			}
		}
		text = strings.ToValidUTF8(string(raw), "�")
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
