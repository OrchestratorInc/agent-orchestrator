// Windows npm shim resolution: AO resolves the dsh binary to the `.cmd` batch
// shim npm installs into its global bin directory. Windows cannot start a batch
// file directly — CreateProcess hands it to cmd.exe, whose `/c` input line is
// capped near 8,191 characters — so the standing-instructions prepend (which
// puts the whole role prompt in argv) dies with "The command line is too long."
// before the harness reads any instruction.
//
// resolveWindowsShimArgv parses that shim and rewrites the argv to start the
// program the shim would have run: a Node runtime plus the package entry
// point. That launch goes through CreateProcess directly, which allows 32,767
// characters, and keeps prompts containing `<`, `>`, `&`, `|`, and newlines out
// of cmd.exe's parser.
//
// The rewrite is conservative and mirrors the semantics of the generic
// agentlaunch shim resolver (see PR #6239): argv is returned unchanged unless
// the shim parses to a Node-backed npm invocation whose entry point exists and
// a Node runtime can be located. An unrecognized or broken install therefore
// still fails through the original cmd.exe path with its own error. Native
// `.exe` binaries are never touched, and once the generic resolver ships at the
// session-manager boundary this package-local rewrite stays a no-op there —
// the resolver will find no batch file in an already-rewritten argv.
package deepseekharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// npmShimProgram is the real program an npm command shim ends up running.
type npmShimProgram struct {
	// UsesInterpreter reports that the shim launches its target through a
	// runtime selected from the entry point's shebang. When false the shim
	// invokes a native program directly.
	UsesInterpreter bool
	// Target is the program the interpreter runs: the package entry point when
	// UsesInterpreter is true, or a native binary otherwise.
	Target string
}

// resolveWindowsShimArgv rewrites a launch argv whose program is a Windows npm
// command shim so the shim's own target is started directly. It returns argv
// unchanged when it is not a batch shim, when the shim cannot be parsed, or
// when its target or a Node runtime is missing.
func resolveWindowsShimArgv(argv []string, lookPath func(string) (string, error)) []string {
	if len(argv) == 0 || !isWindowsBatchFile(argv[0]) {
		return argv
	}
	shim := argv[0]
	content, err := os.ReadFile(shim) //nolint:gosec // path is the resolved harness binary
	if err != nil {
		return argv
	}
	program, ok := npmShimInvocation(filepath.Dir(shim), string(content))
	if !ok {
		return argv
	}

	target := ""
	var executable string
	if program.UsesInterpreter {
		// Both halves must exist: a Node runtime that is missing would fall
		// back to PATH, but a shim whose entry point was removed or moved must
		// stay untouched so the original launch still reports its own failure.
		if !isRegularFile(program.Target) {
			return argv
		}
		target = program.Target
		executable = resolveNodeRuntime(filepath.Dir(shim), lookPath)
	} else {
		executable = program.Target
	}
	if executable == "" || !isRegularFile(executable) {
		return argv
	}

	rewritten := make([]string, 0, len(argv)+1)
	rewritten = append(rewritten, executable)
	if target != "" {
		rewritten = append(rewritten, target)
	}
	rewritten = append(rewritten, argv[1:]...)
	return rewritten
}

// isWindowsBatchFile reports whether path names a Windows batch script — the
// extensions Windows hands to cmd.exe rather than starting directly.
func isWindowsBatchFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}

// npmShimInvocation parses an npm shim body and reports the program it runs.
// cmd-shim emits a single invocation line ending in `%*`; a package shipping a
// native payload invokes that binary instead, through a line of its own.
func npmShimInvocation(dir, content string) (npmShimProgram, bool) {
	var found npmShimProgram
	ok := false
	interpreter := npmShimInterpreter(content)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.Contains(line, "%*") {
			continue
		}
		tokens := quotedTokens(line)
		if len(tokens) == 0 {
			continue
		}
		program, parsed := npmShimProgramFromTokens(dir, tokens, interpreter)
		if !parsed {
			continue
		}
		found, ok = program, true
	}
	return found, ok
}

// npmShimInterpreter recovers the interpreter name cmd-shim will run the entry
// point with. cmd-shim writes two candidates — a `.exe` beside the shim and the
// bare shebang interpreter — and picks the first that exists at run time; the
// ELSE branch holds the interpreter name itself. `%_prog%` is not Node-specific:
// a `#!/usr/bin/env bun` entry produces the same shape with "bun", so this is
// read from the shim rather than assumed.
func npmShimInterpreter(content string) string {
	const elseMarker = ") ELSE ("
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(strings.ToUpper(line), strings.ToUpper(elseMarker)) {
			continue
		}
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(strings.TrimSuffix(next, "\r"))
			name, ok := strings.CutPrefix(next, `SET "_prog=`)
			if !ok {
				continue
			}
			name = strings.Trim(strings.TrimSuffix(name, `"`), `"`)
			if isSupportedInterpreter(name) {
				return name
			}
			return ""
		}
	}
	return ""
}

// isSupportedInterpreter reports whether AO can start name directly. AO knows
// how to locate a Node runtime, so only Node-backed shims are rewritten; any
// other interpreter is left to the original cmd.exe path rather than launched
// under the wrong runtime.
func isSupportedInterpreter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "node", "nodejs":
		return true
	default:
		return false
	}
}

// npmShimProgramFromTokens classifies one shim invocation line.
//
// `"%_prog%"` is cmd-shim's placeholder for the runtime it selected. cmd-shim
// emits `"%_prog%" <shebang args...> "<target>" %*`, so the target is the last
// quoted token and any unquoted shebang flags precede it. An unrecognized
// interpreter is reported as unsupported rather than as a native payload: the
// target is an extensionless script that only runs under its own runtime, so
// the caller must leave the shim alone. A shim whose first token is a plain
// path (no placeholder) invokes a native payload directly.
func npmShimProgramFromTokens(dir string, tokens []string, interpreter string) (npmShimProgram, bool) {
	if strings.Contains(tokens[0], "%_prog%") {
		if len(tokens) < 2 {
			return npmShimProgram{}, false
		}
		target := expandShimPath(tokens[len(tokens)-1], dir)
		if target == "" || interpreter == "" {
			return npmShimProgram{}, false
		}
		return npmShimProgram{UsesInterpreter: true, Target: target}, true
	}
	target := expandShimPath(tokens[0], dir)
	if target == "" {
		return npmShimProgram{}, false
	}
	return npmShimProgram{Target: target}, true
}

// quotedTokens splits a shim invocation line into tokens. A quoted run is one
// token and unquoted whitespace separates tokens, because cmd-shim emits the
// shebang arguments unquoted (`"%_prog%" --require ./bootstrap.js "target" %*`).
// Shim lines do not escape embedded quotes, so a quoted token ends at the next
// quote. A quoted run that is immediately followed by more text starts a new
// token, which is what makes `"%_prog%" --require` two tokens rather than one.
// A trailing `%*` is the caller's argument placeholder and is dropped: AO
// appends the caller's own arguments itself.
func quotedTokens(line string) []string {
	// The invocation is chained as `endLocal & goto ... || title %COMSPEC% &
	// <program> <args> %*`; only the segment after the final `&` is the program.
	// The separator is the last `&` outside quoted tokens: a quoted npm entry
	// path may itself contain `&` (e.g. `R&D.js`), and slicing at it would
	// leave a half-understood shim on the cmd.exe path.
	line = invocationSegment(line)
	var tokens []string
	var current strings.Builder
	inQuotes := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
		}
		current.Reset()
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			if inQuotes {
				// Closing quote: emit the quoted token and let any following
				// unquoted text begin the next one.
				flush()
				inQuotes = false
			} else {
				inQuotes = true
			}
		case (c == ' ' || c == '\t') && !inQuotes:
			flush()
		default:
			current.WriteByte(c)
		}
	}
	flush()
	if n := len(tokens); n > 0 && strings.EqualFold(tokens[n-1], "%*") {
		tokens = tokens[:n-1]
	}
	return tokens
}

// invocationSegment returns the shim invocation after the final `&` command
// separator that sits outside quoted tokens. Separators inside a quoted npm
// entry path (e.g. `"%dp0%\node_modules\pkg\bin\R&D.js"`) are part of the path,
// not the `endLocal & goto ... & <program>` chain, so they must not split.
func invocationSegment(line string) string {
	inQuotes := false
	lastSep := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuotes = !inQuotes
		case '&':
			if !inQuotes {
				lastSep = i
			}
		}
	}
	if lastSep >= 0 {
		return line[lastSep+1:]
	}
	return line
}

// expandShimPath substitutes the shim's own directory for `%dp0%` and returns a
// cleaned absolute path. A token still holding a shell variable yields "" so a
// caller never rewrites argv from a half-understood shim.
func expandShimPath(token, dir string) string {
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	expanded := strings.ReplaceAll(token, "%~dp0", prefix)
	expanded = strings.ReplaceAll(expanded, "%dp0%", prefix)
	if expanded == "" || strings.Contains(expanded, "%") {
		return ""
	}
	return filepath.Clean(expanded)
}

// resolveNodeRuntime prefers the Node runtime installed beside the shim, which
// is how nvm-style installs pin the interpreter, before falling back to PATH.
func resolveNodeRuntime(shimDir string, lookPath func(string) (string, error)) string {
	if local := filepath.Join(shimDir, "node.exe"); isRegularFile(local) {
		return local
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, name := range []string{"node.exe", "node"} {
		if path, err := lookPath(name); err == nil && isRegularFile(path) {
			return path
		}
	}
	return ""
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
