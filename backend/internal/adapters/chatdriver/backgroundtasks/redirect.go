package backgroundtasks

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A log last written this long before its task started still counts as the
// task's own: a provider can stamp the start after the shell opened the file.
const logClockSlack = 5 * time.Second

// RedirectedOutput reads the log file a background shell command writes its
// output to. A command like `npm run dev > /tmp/dev.log 2>&1` prints nothing
// of its own, so the log is the only output there is.
//
// The path comes from the command line, never from a request. Relative paths
// start at dir, after any plain `cd` the command runs first. Only a log inside
// dir, the session's workspace, is read: an absolute path elsewhere, or a
// symlink anywhere on the way that leaves dir, is not followed. The file is
// read only when it is a regular file, not a symlink, written no earlier than
// the task started: a stale log from an earlier run, or an existing file the
// command opened without writing to, is not shown.
func RedirectedOutput(command, dir string, startedAt *time.Time) (ports.ChatBackgroundTaskOutput, bool) {
	if startedAt == nil || !filepath.IsAbs(dir) {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	home, _ := os.UserHomeDir()
	path, ok := stdoutFile(command, dir, home)
	if !ok {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	raw, dropped, ok := readLogTail(dir, rel, startedAt.Add(-logClockSlack))
	if !ok {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	return TailOutput(raw, dropped), true
}

// readLogTail reads at most MaxOutputBytes from the end of the log at rel
// inside dir, starting on a whole line. dropped says older output was left out.
// os.Root keeps every step of rel, symlinked parents included, inside dir.
func readLogTail(dir, rel string, notBefore time.Time) (raw string, dropped, ok bool) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", false, false
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(notBefore) {
		return "", false, false
	}
	file, err := root.Open(rel)
	if err != nil {
		return "", false, false
	}
	defer func() { _ = file.Close() }()
	// The path must still name the file checked above, not one swapped in since.
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", false, false
	}
	start := max(0, opened.Size()-MaxOutputBytes)
	buffer := make([]byte, opened.Size()-start)
	n, err := file.ReadAt(buffer, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, false
	}
	raw = string(buffer[:n])
	if start > 0 {
		// The cut can land mid-line; that partial first line is not output.
		if at := strings.IndexByte(raw, '\n'); at >= 0 {
			raw = raw[at+1:]
		} else {
			raw = ""
		}
	}
	return raw, start > 0, true
}

// stdoutFile finds the file a shell command line sends its standard output
// to: the last redirect of descriptor 1 (`>`, `>>`, `1>`, `&>`, `>|`). It
// answers only for a path the shell would not expand further, and never for a
// device such as /dev/null.
//
// The line is read as a POSIX shell (sh, bash, zsh, Git Bash) reads it, on
// every platform. On Windows that means a backslash escapes, so only a path
// written with forward slashes is followed: drive-absolute (C:/logs/dev.log)
// or relative to dir.
func stdoutFile(command, dir, home string) (string, bool) {
	tokens, ok := lexShell(command)
	if !ok {
		return "", false
	}
	type place struct {
		dir   string
		known bool
	}
	cwd := place{dir, filepath.IsAbs(dir)}
	var subshells []place
	var words []shellToken // the current simple command's words
	target, found := "", false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token.kind {
		case tokenWord:
			words = append(words, token)
		case tokenSeparator:
			// A plain `cd` moves where the commands after it run.
			if len(words) > 0 && words[0].text == "cd" && !words[0].quoted {
				cwd.dir, cwd.known = changeDir(cwd.dir, cwd.known, words[1:], home)
			}
			words = nil
			switch token.text {
			case "(":
				subshells = append(subshells, cwd)
			case ")":
				// A `cd` inside parentheses ends with them.
				if len(subshells) > 0 {
					cwd = subshells[len(subshells)-1]
					subshells = subshells[:len(subshells)-1]
				}
			}
		case tokenRedirect:
			if i+1 >= len(tokens) || tokens[i+1].kind != tokenWord {
				continue
			}
			i++
			to := tokens[i]
			if token.input || (token.fd != 1 && token.fd != -1) {
				continue
			}
			// `>&2` copies a descriptor; only `>&file` names a file.
			if token.dup && (to.text == "-" || allDigits(to.text)) {
				continue
			}
			// The last redirect decides, even one that can't be followed.
			if isDevice(to.text) {
				target, found = "", false
				continue
			}
			target, found = resolveShellPath(to, cwd.dir, cwd.known, home)
		}
	}
	if !found || isDevice(target) {
		return "", false
	}
	return target, true
}

// isDevice reports a /dev path, checked as written too, since on Windows
// /dev/null is not absolute and would otherwise resolve under dir.
func isDevice(path string) bool {
	return path == "/dev" || strings.HasPrefix(path, "/dev/")
}

func changeDir(cwd string, known bool, args []shellToken, home string) (string, bool) {
	switch {
	case len(args) == 0:
		return home, home != ""
	case len(args) > 1 || args[0].text == "-":
		// Options, `cd -` and the like: where it lands isn't known.
		return "", false
	}
	return resolveShellPath(args[0], cwd, known, home)
}

func resolveShellPath(word shellToken, cwd string, known bool, home string) (string, bool) {
	text := word.text
	if !word.literal || text == "" {
		return "", false
	}
	if word.tilde {
		switch {
		case home == "":
			return "", false
		case text == "~":
			text = home
		case strings.HasPrefix(text, "~/"):
			text = home + text[1:]
		default: // ~user
			return "", false
		}
	}
	if filepath.IsAbs(text) {
		return filepath.Clean(text), true
	}
	if !known {
		return "", false
	}
	return filepath.Join(cwd, text), true
}

type tokenKind uint8

const (
	tokenWord tokenKind = iota
	// tokenSeparator ends a simple command: ; && || | & ( ) or a newline.
	tokenSeparator
	tokenRedirect
)

type shellToken struct {
	kind tokenKind
	text string
	// literal: a word has nothing left to expand ($, backquotes, globs).
	literal bool
	// quoted: part of a word was quoted or escaped.
	quoted bool
	// tilde: a word starts with an unquoted ~.
	tilde bool
	// fd is a redirect's descriptor; -1 for &>, which redirects 1 and 2.
	fd    int
	dup   bool
	input bool
}

// lexShell splits a command line into words, separators and redirects, as a
// POSIX shell does for the parts that decide where output goes. ok is false
// for a line it can't follow, such as one with a here-document or an
// unterminated quote.
func lexShell(line string) ([]shellToken, bool) {
	var tokens []shellToken
	var word strings.Builder
	current := shellToken{kind: tokenWord, literal: true}
	inWord := false
	flush := func() {
		if inWord {
			current.text = word.String()
			tokens = append(tokens, current)
		}
		word.Reset()
		current = shellToken{kind: tokenWord, literal: true}
		inWord = false
	}
	separator := func(text string) {
		flush()
		tokens = append(tokens, shellToken{kind: tokenSeparator, text: text})
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch c {
		case '\\':
			if i+1 < len(line) {
				i++
				if line[i] != '\n' {
					word.WriteByte(line[i])
					inWord, current.quoted = true, true
				}
			}
		case '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			word.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord, current.quoted = true, true
		case '"':
			j := i + 1
			for ; j < len(line) && line[j] != '"'; j++ {
				switch line[j] {
				case '\\':
					if j+1 < len(line) && strings.IndexByte("\"\\$`\n", line[j+1]) >= 0 {
						j++
						if line[j] != '\n' {
							word.WriteByte(line[j])
						}
						continue
					}
				case '$', '`':
					current.literal = false
				}
				word.WriteByte(line[j])
			}
			if j >= len(line) {
				return nil, false
			}
			i = j
			inWord, current.quoted = true, true
		case '$':
			current.literal, inWord = false, true
			word.WriteByte(c)
			if i+1 < len(line) && line[i+1] == '(' {
				end := closingParen(line, i+1)
				if end < 0 {
					return nil, false
				}
				word.WriteString(line[i+1 : end+1])
				i = end
			}
		case '`':
			end := strings.IndexByte(line[i+1:], '`')
			if end < 0 {
				return nil, false
			}
			word.WriteString(line[i : i+end+2])
			i += end + 1
			current.literal, inWord = false, true
		case '*', '?', '[':
			current.literal, inWord = false, true
			word.WriteByte(c)
		case '~':
			if !inWord {
				current.tilde = true
			}
			word.WriteByte(c)
			inWord = true
		case ' ', '\t', '\r':
			flush()
		case '\n', ';', '(', ')':
			separator(string(c))
		case '#':
			if inWord {
				word.WriteByte(c)
				break
			}
			// A comment runs to the end of the line.
			for i+1 < len(line) && line[i+1] != '\n' {
				i++
			}
		case '&':
			switch {
			case strings.HasPrefix(line[i:], "&&"):
				separator("&&")
				i++
			case strings.HasPrefix(line[i:], "&>"):
				flush()
				i++
				if strings.HasPrefix(line[i+1:], ">") {
					i++
				}
				tokens = append(tokens, shellToken{kind: tokenRedirect, fd: -1})
			default:
				separator("&")
			}
		case '|':
			if strings.HasPrefix(line[i:], "||") {
				separator("||")
				i++
				break
			}
			separator("|")
			if strings.HasPrefix(line[i+1:], "&") {
				i++
			}
		case '>', '<':
			redirect := shellToken{kind: tokenRedirect, fd: 1, input: c == '<'}
			if c == '<' {
				redirect.fd = 0
			}
			// Digits right before the operator name its descriptor: 2>.
			if digits := word.String(); inWord && !current.quoted && allDigits(digits) {
				redirect.fd, _ = strconv.Atoi(digits)
				word.Reset()
				current = shellToken{kind: tokenWord, literal: true}
				inWord = false
			} else {
				flush()
			}
			rest := line[i+1:]
			switch {
			case c == '<' && strings.HasPrefix(rest, "<<"): // here-string
				i += 2
			case c == '<' && strings.HasPrefix(rest, "<"): // here-document
				return nil, false
			case strings.HasPrefix(rest, string(c)), // >> appends
				c == '>' && strings.HasPrefix(rest, "|"),
				c == '<' && strings.HasPrefix(rest, ">"):
				i++
			case strings.HasPrefix(rest, "&"):
				redirect.dup = true
				i++
			}
			tokens = append(tokens, redirect)
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return tokens, true
}

// closingParen returns the index of the parenthesis closing the one at open.
func closingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
