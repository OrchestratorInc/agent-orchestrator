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
		if signed(t) {
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

// signed reports a block whose text a model provider signs and checks when
// the conversation resumes, so not a byte of it may change: Claude Code's
// thinking and redacted_thinking blocks, OpenCode's reasoning parts (whose
// signature is in metadata.<provider>.signature), and any object that
// carries a signature itself or in its metadata.
func signed(t map[string]any) bool {
	switch kind, _ := t["type"].(string); kind {
	case "thinking", "redacted_thinking", "reasoning":
		return true
	}
	if _, ok := t["signature"]; ok {
		return true
	}
	if meta, ok := t["metadata"].(map[string]any); ok {
		for _, provider := range meta {
			if fields, ok := provider.(map[string]any); ok {
				if _, ok := fields["signature"]; ok {
					return true
				}
			}
		}
	}
	return false
}

// rewriteString replaces every mapped prefix in s in one pass over the
// original text, so a replacement is never matched again by a later pair
// (when one pair's new path is another pair's old one). At each position the
// longest matching prefix wins (m is sorted longest first).
//
// Each prefix's next match is cached, absence included, and searched again
// only once the scan has passed it, so every prefix's search moves forward:
// the text is scanned about once per prefix, not once per replacement, which
// keeps a long string listing many paths (up to a 64 MiB transcript) linear.
func (m pathMap) rewriteString(s string) (string, bool) {
	var b strings.Builder
	changed := false
	i := 0
	// next[k] is prefix k's next whole-path match at or after i; -1 when it
	// has none left, which stays true for the rest of s.
	next := make([]int, len(m))
	for k, p := range m {
		next[k] = indexWholePath(s, 0, p.old)
	}
	for i < len(s) {
		best, at := -1, len(s)
		for k, p := range m {
			if next[k] >= 0 && next[k] < i {
				// Passed (inside an earlier replacement): look again from i.
				next[k] = indexWholePath(s, i, p.old)
			}
			if j := next[k]; j >= 0 && j < at {
				best, at = k, j
			}
		}
		if best < 0 {
			break
		}
		p := m[best]
		b.WriteString(s[i:at])
		b.WriteString(p.new)
		i = at + len(p.old)
		changed = true
		// The rest of a Windows path takes the separators of a POSIX new, and the reverse.
		if fromSep, toSep := separator(p.old), separator(p.new); fromSep != toSep {
			for ; i < len(s) && (s[i] == fromSep || isPathNameByte(s[i])); i++ {
				if s[i] == fromSep {
					b.WriteByte(toSep)
				} else {
					b.WriteByte(s[i])
				}
			}
		}
	}
	if !changed {
		return s, false
	}
	b.WriteString(s[i:])
	return b.String(), true
}

// replacePathPrefix replaces from wherever it stands as a whole path, so
// "/w/web-3" matches "/w/web-3/a.go" but not "/w/web-30" or "/x/w/web-3".
func replacePathPrefix(s, from, to string) (string, bool) {
	return pathMap{{old: from, new: to}}.rewriteString(s)
}

// indexWholePath is the first index at or after i where from stands as a whole
// path, or -1.
func indexWholePath(s string, i int, from string) int {
	for {
		j := strings.Index(s[i:], from)
		if j < 0 {
			return -1
		}
		j += i
		end := j + len(from)
		if (j == 0 || !isPathNameByte(s[j-1])) && (end == len(s) || !isPathNameByte(s[end])) {
			return j
		}
		i = j + 1
	}
}

func isPathNameByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// separator is the path separator p uses.
func separator(p string) byte {
	if windowsPath.MatchString(p) && strings.Contains(p, `\`) {
		return '\\'
	}
	return '/'
}
