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

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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
	// assigned is false for a bare name without "=", which python-dotenv
	// parses with value None and load_dotenv never sets.
	assigned bool
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

// dotenvEnvironment is the environment the bindings leave behind when Hermes
// loads them with override: each value's ${VAR} and ${VAR:-default}
// references resolve against base overlaid with earlier assignments, and
// the last assignment of a name wins. Names are keyed by processEnvName.
func dotenvEnvironment(bindings []dotenvBinding, base envLookup) map[string]string {
	env := make(map[string]string, len(bindings))
	lookup := func(name string) (string, bool) {
		if value, ok := env[processEnvName(name)]; ok {
			return value, true
		}
		if base == nil {
			return "", false
		}
		return base(name)
	}
	for _, binding := range bindings {
		if binding.assigned {
			env[processEnvName(binding.key)] = interpolateDotenv(binding.value, lookup)
		}
	}
	return env
}

// dotenvVariable is python-dotenv's POSIX variable reference.
var dotenvVariable = regexp.MustCompile(`\$\{([^}:]*)(?::-([^}]*))?\}`)

// interpolateDotenv mirrors python-dotenv's Variable.resolve: a defined name
// keeps its value even when empty, else the default (or empty) applies.
func interpolateDotenv(value string, lookup envLookup) string {
	return dotenvVariable.ReplaceAllStringFunc(value, func(ref string) string {
		m := dotenvVariable.FindStringSubmatch(ref)
		if resolved, ok := lookup(m[1]); ok {
			return resolved
		}
		return m[2]
	})
}

// dotenvEscapes are the control characters python-dotenv's unicode-escape
// decoding produces for a double-quoted value.
var dotenvEscapes = map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v'}

// decodeDotenvEscapes applies python-dotenv's decode_escapes to a quoted
// value: a backslash followed by one of allowed is decoded.
func decodeDotenvEscapes(value, allowed string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) || strings.IndexByte(allowed, value[i+1]) < 0 {
			b.WriteByte(value[i])
			continue
		}
		i++
		if c, ok := dotenvEscapes[value[i]]; ok {
			b.WriteByte(c)
		} else {
			b.WriteByte(value[i])
		}
	}
	return b.String()
}

// processEnvName is the variable a dotenv key sets in Hermes's process:
// Windows os.environ applies Python's full str.upper to names, which also
// expands ligatures such as U+FB06 to "ST".
func processEnvName(key string) string {
	if runtime.GOOS == "windows" {
		return cases.Upper(language.Und).String(key)
	}
	return key
}

// guardedEnvName is processEnvName for a refusal check: a leading U+FEFF is
// dropped too, since a later utf-8-sig read can strip it once Hermes rewrites
// the file. Never use it as positive evidence.
func guardedEnvName(key string) string {
	return processEnvName(strings.TrimLeft(key, "\ufeff"))
}

// pyStrip is Python's str.strip().
func pyStrip(s string) string {
	return strings.TrimFunc(s, pyIsSpace)
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
		binding.assigned = true
		read(dotenvEqualSign)
		switch {
		case strings.HasPrefix(text, "'"):
			m, ok := read(dotenvSingleQuotedValue)
			if !ok {
				return fail()
			}
			binding.value = decodeDotenvEscapes(m[1], `\'`)
		case strings.HasPrefix(text, `"`):
			m, ok := read(dotenvDoubleQuotedValue)
			if !ok {
				return fail()
			}
			binding.value = decodeDotenvEscapes(m[1], `\'"abfnrtv`)
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
