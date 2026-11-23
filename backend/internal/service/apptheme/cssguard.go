package apptheme

import "strings"

// cssLoadsFile names the first way theme CSS would fetch a file, or "" for
// none. It reads the stylesheet the way a browser's tokenizer does (comments,
// strings, escapes, url tokens) rather than searching the text, so an escaped
// function name (`u\72l(`), a comment marker inside a string, or a url hidden
// in a value the browser parses late cannot slip past. The renderer runs the
// same scan (themeCssLoadsFile in frontend/src/renderer/lib/theme-css-guard.ts)
// and both are held to testdata/css_guard_vectors.json.
//
// A theme may only reference what it carries: url() must hold a data: URL or
// a #fragment. @import, image-set(), src() and image() are refused outright,
// since each can name a file through a bare string.
func cssLoadsFile(css string) string {
	n := len(css)
	for i := 0; i < n; {
		c := css[i]
		switch {
		case c == '/' && i+1 < n && css[i+1] == '*':
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return ""
			}
			i += 2 + end + 2
		case c == '"' || c == '\'':
			_, i = cssString(css, i)
		case c == '#':
			// A hash token (#fff, #id): its name is not a function name.
			_, i = cssName(css, i+1)
		case c == '@':
			var name string
			name, i = cssName(css, i+1)
			if strings.EqualFold(name, "import") {
				return "cannot @import; put the rules in the CSS and embed fonts as data: URLs"
			}
		case cssNameStart(css, i):
			var name string
			name, i = cssName(css, i)
			if i >= n || css[i] != '(' {
				continue
			}
			switch strings.ToLower(name) {
			case "url":
				var target string
				target, i = cssURLTarget(css, i+1)
				target = strings.ToLower(strings.TrimSpace(target))
				if !strings.HasPrefix(target, "data:") && !strings.HasPrefix(target, "#") {
					return "cannot load " + quoteClipped(target) + "; embed fonts and images as data: URLs"
				}
			case "image-set", "-webkit-image-set", "src", "image":
				return "cannot use " + strings.ToLower(name) + "(); use one url(data:...) image"
			}
		default:
			i++
		}
	}
	return ""
}

func quoteClipped(text string) string {
	return `"` + clip(text) + `"`
}

func cssHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func cssSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func cssNewline(c byte) bool {
	return c == '\n' || c == '\r' || c == '\f'
}

// cssNameChar reports a byte that can be part of an identifier. Bytes of a
// non-ASCII character count, as they do in CSS.
func cssNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c >= 0x80
}

// cssValidEscape reports a backslash that starts an escape: any backslash not
// followed by a newline or the end of the text.
func cssValidEscape(css string, i int) bool {
	return i < len(css) && css[i] == '\\' && i+1 < len(css) && !cssNewline(css[i+1])
}

func cssNameStart(css string, i int) bool {
	c := css[i]
	if c == '-' {
		if i+1 >= len(css) {
			return false
		}
		next := css[i+1]
		return next == '-' || cssNameChar(next) && !(next >= '0' && next <= '9') || cssValidEscape(css, i+1)
	}
	return cssNameChar(c) && !(c >= '0' && c <= '9') && c != '-' || cssValidEscape(css, i)
}

// cssEscape decodes the escape at css[i] (a backslash) and returns its text
// and the index after it.
func cssEscape(css string, i int) (string, int) {
	i++
	if i >= len(css) {
		return "", i
	}
	if !cssHex(css[i]) {
		return css[i : i+1], i + 1
	}
	value, digits := 0, 0
	for i < len(css) && digits < 6 && cssHex(css[i]) {
		c := css[i]
		switch {
		case c <= '9':
			value = value*16 + int(c-'0')
		case c >= 'a':
			value = value*16 + int(c-'a') + 10
		default:
			value = value*16 + int(c-'A') + 10
		}
		i++
		digits++
	}
	// One whitespace after a hex escape belongs to it.
	if i < len(css) && cssSpace(css[i]) {
		if css[i] == '\r' && i+1 < len(css) && css[i+1] == '\n' {
			i++
		}
		i++
	}
	if value == 0 || value > 0x10ffff {
		value = 0xfffd
	}
	return string(rune(value)), i
}

// cssName reads an identifier starting at i, with its escapes decoded.
func cssName(css string, i int) (string, int) {
	var b strings.Builder
	for i < len(css) {
		switch {
		case cssNameChar(css[i]):
			b.WriteByte(css[i])
			i++
		case cssValidEscape(css, i):
			var text string
			text, i = cssEscape(css, i)
			b.WriteString(text)
		default:
			return b.String(), i
		}
	}
	return b.String(), i
}

// cssString reads the string whose opening quote is at i and returns its
// decoded content and the index after it. As in CSS, an unescaped newline
// ends the string, so the text after it is read as code again.
func cssString(css string, i int) (string, int) {
	quote := css[i]
	i++
	var b strings.Builder
	for i < len(css) {
		c := css[i]
		switch {
		case c == quote:
			return b.String(), i + 1
		case cssNewline(c):
			return b.String(), i
		case c == '\\':
			if i+1 >= len(css) {
				return b.String(), i + 1
			}
			if cssNewline(css[i+1]) {
				// A line continuation.
				if css[i+1] == '\r' && i+2 < len(css) && css[i+2] == '\n' {
					i++
				}
				i += 2
				continue
			}
			var text string
			text, i = cssEscape(css, i)
			b.WriteString(text)
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), i
}

// cssURLTarget reads what a url( names, starting after the parenthesis: a
// quoted string, or raw text up to the closing parenthesis.
func cssURLTarget(css string, i int) (string, int) {
	for i < len(css) && cssSpace(css[i]) {
		i++
	}
	if i < len(css) && (css[i] == '"' || css[i] == '\'') {
		return cssString(css, i)
	}
	var b strings.Builder
	for i < len(css) && css[i] != ')' {
		if cssValidEscape(css, i) {
			var text string
			text, i = cssEscape(css, i)
			b.WriteString(text)
			continue
		}
		b.WriteByte(css[i])
		i++
	}
	return b.String(), i
}
