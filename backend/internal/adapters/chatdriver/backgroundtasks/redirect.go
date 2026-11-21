package backgroundtasks

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A log last written this long before its task started still counts as the
// task's own: a provider can stamp the start after the shell opened the file.
const logClockSlack = 5 * time.Second

// RedirectedOutput reads the log file a background shell command writes its
// output to. A command like `npm run dev > logs/dev.log 2>&1` prints nothing
// of its own, so the log is the only output there is.
//
// The path comes from the command line, never from a request. Relative paths
// start at dir, after any plain `cd` the command runs first. Only a log inside
// dir, the session's workspace, is read: an absolute path elsewhere, or a
// symlink anywhere on the way that leaves dir, is not followed, and the result
// says so with LogOutsideWorkspace. The file is read only when it is a regular
// file, not a symlink, written no earlier than the task started: a stale log
// from an earlier run, or an existing file the command opened without writing
// to, is not shown.
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
		return ports.ChatBackgroundTaskOutput{LogOutsideWorkspace: true}, false
	}
	raw, dropped, ok := readLogTail(dir, rel, startedAt.Add(-logClockSlack))
	if !ok {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	return TailOutput(raw, dropped), true
}

// RedirectTarget returns the log file a command writes to, as RedirectedOutput
// resolves it from dir, or "" when it names none that can be followed. Two
// runs with the same target share a log.
func RedirectTarget(command, dir string) string {
	if !filepath.IsAbs(dir) {
		return ""
	}
	home, _ := os.UserHomeDir()
	path, _ := stdoutFile(command, dir, home)
	return path
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

// stdoutFile finds the log file a shell command line sends its standard
// output to. Within one simple command the last redirect of descriptor 1
// (`>`, `>>`, `1>`, `&>`, `>|`) decides, as in the shell. Across commands, the
// one put in the background with `&` decides, since that is the long-running
// one (`npm run dev > dev.log 2>&1 & echo $! > dev.pid`); otherwise the first
// command that redirects its output does (`npm run dev > dev.log; echo ok >
// status.txt`). It answers only for a path the shell would not expand
// further, and never for a device such as /dev/null.
//
// The line is read as a POSIX shell (sh, bash, zsh, Git Bash) reads it. On
// Windows a line with nothing only a POSIX shell reads ($, backquotes, nohup)
// is read as cmd.exe reads it instead, which is how Codex reports commands
// there: see cmdLine.
func stdoutFile(command, dir, home string) (string, bool) {
	return stdoutFileAs(command, dir, home, hostWindows && cmdLine(command))
}

// cmdLine reports a Windows command line that reads as cmd.exe, not a POSIX
// shell: it has none of $, backquotes or nohup, which only a POSIX shell uses.
func cmdLine(command string) bool {
	return !strings.ContainsAny(command, "$`") && !strings.Contains(command, "nohup")
}

// stdoutFileAs reads command as a POSIX shell, or as cmd.exe when cmd is set.
// cmd.exe runs the commands either side of `&` one after the other, so no
// command is in the background, and the last one that redirects its output
// decides (`echo start > status.txt & npm run dev > dev.log`). A backslash
// there is a path separator (logs\dev.log), not an escape. A POSIX line on
// Windows follows only forward-slash paths: drive-absolute (C:/logs/dev.log),
// rooted without a drive (/tmp/dev.log, outside dir), or relative to dir.
func stdoutFileAs(command, dir, home string, cmd bool) (string, bool) {
	tokens, ok := lexShell(command, cmd)
	if !ok {
		return "", false
	}
	type place struct {
		dir   string
		known bool
	}
	// redirect is where one command, or a parenthesized group, sends its output.
	type redirect struct {
		target     string
		found, set bool
	}
	cwd := place{dir, filepath.IsAbs(dir)}
	var subshells []place
	var groups []int         // per open `(`, the index in commands where it began
	var words []shellToken   // the current simple command's words
	var current redirect     // the current simple command's redirect
	var commands []redirect  // every finished command's redirect, in order
	var background *redirect // the redirect of the command run with `&`
	lastGroup := -1          // after `)`: where that group began in commands
	finish := func() {
		commands = append(commands, current)
		current = redirect{}
	}
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
			ran := current
			hadGroup := lastGroup
			finish()
			lastGroup = -1
			switch token.text {
			case "(":
				subshells = append(subshells, cwd)
				groups = append(groups, len(commands))
			case ")":
				// A `cd` inside parentheses ends with them.
				if len(subshells) > 0 {
					cwd = subshells[len(subshells)-1]
					subshells = subshells[:len(subshells)-1]
				}
				if len(groups) > 0 {
					lastGroup = groups[len(groups)-1]
					groups = groups[:len(groups)-1]
				}
			case "&":
				switch {
				case cmd:
					// cmd.exe's `&` means "then": nothing runs in the background.
				case ran.set:
					background = &ran
				case hadGroup >= 0:
					// `( cd app && npm run dev > dev.log ) &`: the group's first redirect.
					for _, inner := range commands[hadGroup:] {
						if inner.set {
							background = &inner
							break
						}
					}
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
			// The command's last redirect decides, even one that can't be followed.
			current = redirect{set: true}
			if isDevice(to.text) || (hostWindows && to.escaped) || (cmd && strings.EqualFold(to.text, "NUL")) {
				continue
			}
			current.target, current.found = resolveShellPath(to, cwd.dir, cwd.known, home)
		}
	}
	finish()
	chosen := background
	if chosen == nil && cmd {
		for i := len(commands) - 1; i >= 0; i-- {
			if commands[i].set {
				chosen = &commands[i]
				break
			}
		}
	}
	if chosen == nil {
		for i := range commands {
			if commands[i].set {
				chosen = &commands[i]
				break
			}
		}
	}
	if chosen == nil || !chosen.found || isDevice(chosen.target) {
		return "", false
	}
	return chosen.target, true
}

// hostWindows: paths follow Windows rules, and a backslash on a command line
// is more likely a path separator than the escape a POSIX shell reads.
var hostWindows = runtime.GOOS == "windows"

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
	// On Windows a path rooted without a drive, as Git Bash writes /tmp/dev.log,
	// is not under any workspace; it must not be joined onto one.
	if filepath.IsAbs(text) || (hostWindows && strings.HasPrefix(text, "/")) {
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
	// escaped: a backslash outside quotes escaped part of a word.
	escaped bool
	// fd is a redirect's descriptor; -1 for &>, which redirects 1 and 2.
	fd    int
	dup   bool
	input bool
}

// lexShell splits a command line into words, separators and redirects, as a
// POSIX shell does for the parts that decide where output goes. ok is false
// for a line it can't follow, such as one with a here-document or an
// unterminated quote. With cmd set it reads the line as cmd.exe does: a
// backslash and a single quote are plain characters, ^ escapes, %VAR%
// expands, and there are no comments or ~ for home.
func lexShell(line string, cmd bool) ([]shellToken, bool) {
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
		if cmd {
			switch c {
			case '\\', '\'', '#', '~':
				word.WriteByte(c)
				inWord = true
				continue
			case '%':
				current.literal, inWord = false, true
				word.WriteByte(c)
				continue
			case '^':
				if i+1 < len(line) {
					i++
					word.WriteByte(line[i])
					inWord, current.quoted, current.escaped = true, true, true
				}
				continue
			}
		}
		switch c {
		case '\\':
			if i+1 < len(line) {
				i++
				if line[i] != '\n' {
					word.WriteByte(line[i])
					inWord, current.quoted, current.escaped = true, true, true
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
