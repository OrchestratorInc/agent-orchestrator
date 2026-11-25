package nativetransfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var windowsPath = regexp.MustCompile(`^(?:[A-Za-z]:|\\\\[^\\/]+)[\\/]`)

// isAbsPath accepts an absolute path of any platform: the machine that
// recorded it may run another one.
func isAbsPath(p string) bool {
	return strings.HasPrefix(p, "/") || windowsPath.MatchString(p)
}

// pathMap rewrites absolute path prefixes from the source machine to this one
// inside JSON Lines records.
type pathMap []prefixPair

type prefixPair struct {
	old, new string
	oldJSON  []byte
}

func newPathMap(pairs map[string]string) (pathMap, error) {
	var m pathMap
	for old, new := range pairs {
		if !isAbsPath(old) || strings.Trim(windowsPath.ReplaceAllString(old, ""), `/\`) == "" {
			return nil, fmt.Errorf("nativetransfer: refusing to rewrite path prefix %q", old)
		}
		if old == new {
			continue
		}
		m = append(m, prefixPair{old: old, new: new, oldJSON: jsonText(old)})
		// Tools on Windows also write paths with forward slashes.
		if separator(old) == '\\' {
			slashed := strings.ReplaceAll(old, `\`, "/")
			m = append(m, prefixPair{old: slashed, new: strings.ReplaceAll(new, `\`, "/"), oldJSON: jsonText(slashed)})
		}
	}
	// Longest first, so a prefix nested inside another is replaced by the more specific pair.
	sort.Slice(m, func(i, j int) bool { return len(m[i].old) > len(m[j].old) })
	return m, nil
}

// jsonText is s as it appears inside a JSON string, without the quotes.
func jsonText(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimSuffix(bytes.TrimSuffix(buf.Bytes(), []byte("\n")), []byte(`"`))[1:]
}

// rewriteLine returns line with every mapped path prefix replaced in string
// values, except inside thinking blocks: their text is covered by a signature
// the provider checks on resume. A line with nothing to replace is returned
// unchanged byte for byte. A rewritten line is re-encoded, so its object keys
// come out sorted.
func (m pathMap) rewriteLine(line []byte) ([]byte, bool, error) {
	if !m.mentionedIn(line) {
		return line, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false, fmt.Errorf("nativetransfer: decode record: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("nativetransfer: record has trailing data")
	}
	v, changed := m.rewriteValue(v)
	if !changed {
		return line, false, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), true, nil
}

func (m pathMap) mentionedIn(line []byte) bool {
	for _, p := range m {
		if bytes.Contains(line, p.oldJSON) {
			return true
		}
	}
	return false
}

func (m pathMap) rewriteValue(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		return m.rewriteString(t)
	case []any:
		changed := false
		for i := range t {
			if nv, c := m.rewriteValue(t[i]); c {
				t[i], changed = nv, true
			}
		}
		return t, changed
	case map[string]any:
		if kind, _ := t["type"].(string); kind == "thinking" || kind == "redacted_thinking" {
			return t, false
		}
		changed := false
		for k, val := range t {
			if nv, c := m.rewriteValue(val); c {
				t[k], changed = nv, true
			}
		}
		return t, changed
	}
	return v, false
}

func (m pathMap) rewriteString(s string) (string, bool) {
	changed := false
	for _, p := range m {
		if out, c := replacePathPrefix(s, p.old, p.new); c {
			s, changed = out, true
		}
	}
	return s, changed
}

// replacePathPrefix replaces from where it stands as a whole path, so
// "/w/web-3" matches "/w/web-3/a.go" but not "/w/web-30" or "/x/w/web-3". The
// rest of a Windows path takes the separators of a POSIX to, and the reverse.
func replacePathPrefix(s, from, to string) (string, bool) {
	fromSep, toSep := separator(from), separator(to)
	var b strings.Builder
	changed := false
	i := 0
	for {
		j := strings.Index(s[i:], from)
		if j < 0 {
			break
		}
		j += i
		end := j + len(from)
		b.WriteString(s[i:j])
		if (j == 0 || !isPathNameByte(s[j-1])) && (end == len(s) || !isPathNameByte(s[end])) {
			b.WriteString(to)
			changed = true
			for ; fromSep != toSep && end < len(s) && (s[end] == fromSep || isPathNameByte(s[end])); end++ {
				if s[end] == fromSep {
					b.WriteByte(toSep)
				} else {
					b.WriteByte(s[end])
				}
			}
		} else {
			b.WriteString(from)
		}
		i = end
	}
	if !changed {
		return s, false
	}
	b.WriteString(s[i:])
	return b.String(), true
}

func separator(p string) byte {
	if windowsPath.MatchString(p) && strings.Contains(p, `\`) {
		return '\\'
	}
	return '/'
}

func isPathNameByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
