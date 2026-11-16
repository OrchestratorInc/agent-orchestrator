package pr

import "strings"

// markdownCodeRanges finds a Markdown text's code, as [start, end) byte
// offsets: fenced blocks and inline code spans. An attachment link inside code
// is an example to show, not a file to store.
//
// A fence is a line that starts, after any indent, with three or more
// backticks or tildes (a backtick fence's line has no other backtick); it runs
// to the next line of at least as many of the same character, or to the end.
// A code span opens with a run of backticks and closes at the next run of
// exactly as many, within its paragraph; a run with no such partner is plain
// text. A backslash before a backtick makes that backtick plain text.
//
// The app's markdownCodeRanges (frontend/src/renderer/lib/markdown-code.ts)
// mirrors this, so the app and the daemon agree on which files a comment links.
func markdownCodeRanges(text string) [][2]int {
	var ranges [][2]int
	lineStart := true
	for i := 0; i < len(text); {
		if lineStart {
			if end, ok := fencedBlockEnd(text, i); ok {
				ranges = append(ranges, [2]int{i, end})
				i = end
				continue
			}
		}
		switch c := text[i]; {
		case c == '\n':
			lineStart = true
			i++
		case c == '\\' && i+1 < len(text) && text[i+1] == '`':
			lineStart = false
			i += 2
		case c == '`':
			lineStart = false
			run := runLength(text, i, '`')
			if end, ok := codeSpanEnd(text, i+run, run); ok {
				ranges = append(ranges, [2]int{i, end})
				i = end
			} else {
				i += run
			}
		default:
			lineStart = false
			i++
		}
	}
	return ranges
}

// fencedBlockEnd reports whether the line at start opens a fenced block, and
// where the block ends: after its closing line, or at the end of the text.
func fencedBlockEnd(text string, start int) (int, bool) {
	line, next := lineAt(text, start)
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	if indent == len(line) || (line[indent] != '`' && line[indent] != '~') {
		return 0, false
	}
	marker := line[indent]
	size := runLength(line, indent, marker)
	if size < 3 || (marker == '`' && strings.IndexByte(line[indent+size:], '`') >= 0) {
		return 0, false
	}
	for at := next; at < len(text); {
		line, after := lineAt(text, at)
		body := strings.TrimLeft(line, " \t")
		if closing := runLength(body, 0, marker); closing >= size && strings.TrimSpace(body[closing:]) == "" {
			return after, true
		}
		at = after
	}
	return len(text), true
}

// codeSpanEnd finds where a code span opened by a run of size backticks ends:
// after the next run of exactly size backticks before a blank line.
func codeSpanEnd(text string, from, size int) (int, bool) {
	for i := from; i < len(text); {
		switch text[i] {
		case '`':
			run := runLength(text, i, '`')
			if run == size {
				return i + run, true
			}
			i += run
		case '\n':
			line, _ := lineAt(text, i+1)
			if strings.TrimSpace(line) == "" {
				return 0, false
			}
			i++
		default:
			i++
		}
	}
	return 0, false
}

// lineAt is the line starting at start, without its newline, and where the
// next line starts.
func lineAt(text string, start int) (string, int) {
	if end := strings.IndexByte(text[start:], '\n'); end >= 0 {
		return text[start : start+end], start + end + 1
	}
	return text[start:], len(text)
}

func runLength(text string, start int, c byte) int {
	n := 0
	for start+n < len(text) && text[start+n] == c {
		n++
	}
	return n
}
