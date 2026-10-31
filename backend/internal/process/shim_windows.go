//go:build windows

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxShimSize bounds how much of a batch file is read while looking for its
// launch line. npm, pnpm and yarn shims are a few hundred bytes; anything far
// larger is not a shim worth parsing.
const maxShimSize = 64 << 10

// resolveShimArgv rewrites a command whose program resolves to a Windows batch
// shim so the shim's own target is started directly.
//
// Windows cannot start a .cmd/.bat itself: CreateProcess rejects it, so every
// runtime that "runs" one (Go included) hands the whole line to cmd.exe. That
// shell has no way to carry a double quote inside an argument — it treats `"` as
// a delimiter — and a batch shim's `%*` re-expands the line for one more parse.
// An argument like Codex's hook override
//
//	-c hooks.SessionStart=[{hooks=[{type="command",command="& \"C:\...\ao.exe\" hooks codex session-start",timeout=5}]}]
//
// is therefore split at the `&`, whichever way the quotes are escaped before
// cmd.exe sees them: doubling them (`""`), prefixing a backslash (`\"`) and
// caret-escaping the metacharacters all still deliver a split or altered argv
// (issue #7212). Starting the target directly removes cmd.exe from the path, so
// argv reaches the harness byte for byte and no quoting is involved at all.
//
// The rewrite is deliberately conservative: the command is returned unchanged
// unless the shim matches a shape this package understands and every path it
// names exists, so an unfamiliar or broken shim keeps its previous behaviour.
func resolveShimArgv(name string, args []string) (string, []string) {
	resolved, err := exec.LookPath(name)
	if err != nil || !isBatchFile(resolved) {
		return name, args
	}
	argv, ok := shimTargetArgv(resolved, args)
	if !ok {
		return name, args
	}
	return argv[0], argv[1:]
}

func isBatchFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}

// shimTargetArgv returns the argv that runs shim's own target, or false when the
// shim has no launch line this package can follow.
func shimTargetArgv(shim string, args []string) ([]string, bool) {
	info, err := os.Stat(shim)
	if err != nil || info.IsDir() || info.Size() > maxShimSize {
		return nil, false
	}
	content, err := os.ReadFile(shim)
	if err != nil {
		return nil, false
	}
	dir := filepath.Dir(shim)
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.Contains(line, "%*") {
			continue
		}
		if argv, ok := shimLineArgv(line, dir, args); ok {
			return argv, true
		}
	}
	return nil, false
}

// shimLineArgv parses one shim line that forwards `%*`. npm chains its setup and
// the real launch onto a single line with `&`; yarn and pnpm put the launch on
// its own line, which the same parse handles because there is nothing to split.
func shimLineArgv(line, dir string, args []string) ([]string, bool) {
	line = strings.TrimPrefix(strings.TrimSpace(line), "@")
	if i := strings.LastIndex(line, "&"); i >= 0 {
		line = line[i+1:]
	}
	line = strings.TrimSpace(line)
	// npm sets dp0 from `%~dp0`; yarn uses `%~dp0` inline. Both are the shim's
	// own directory, which ends in a separator already.
	line = strings.ReplaceAll(line, "%~dp0", dir+`\`)
	line = strings.ReplaceAll(line, "%dp0%", dir+`\`)
	tokens, ok := splitShimTokens(line)
	if !ok || len(tokens) < 2 {
		return nil, false
	}
	// AO's arguments take the place of `%*`; a line that uses them anywhere else
	// would need an escaping rule this package does not implement.
	if tokens[len(tokens)-1] != "%*" {
		return nil, false
	}
	argv := tokens[:len(tokens)-1]
	program, ok := shimProgram(argv[0], dir)
	if !ok {
		return nil, false
	}
	argv[0] = program
	// `%dp0%` carries a trailing separator and the shim adds its own, so path
	// tokens can hold doubled separators; clean them before validating.
	for i, token := range argv[1:] {
		if strings.ContainsAny(token, `\/`) {
			argv[i+1] = filepath.Clean(token)
		}
	}
	for _, token := range argv[1:] {
		if !shimPathToken(token) {
			return nil, false
		}
	}
	return append(argv, args...), true
}

// shimProgram resolves the shim's launcher. npm picks the Node bundled next to
// the shim when it exists and falls back to `node` on PATH; both are tried.
func shimProgram(token, dir string) (string, bool) {
	if token == "%_prog%" {
		if bundled := filepath.Join(dir, "node.exe"); fileExists(bundled) {
			return bundled, true
		}
		node, err := exec.LookPath("node")
		if err != nil {
			return "", false
		}
		return node, true
	}
	if strings.Contains(token, "%") {
		return "", false
	}
	if !strings.ContainsAny(token, `\/`) {
		resolved, err := exec.LookPath(token)
		if err != nil {
			return "", false
		}
		return resolved, true
	}
	cleaned := filepath.Clean(token)
	if !fileExists(cleaned) {
		return "", false
	}
	return cleaned, true
}

// shimPathToken accepts the remaining tokens of a launch line: bare flags pass
// through, while anything that names a file has to exist. That check is what
// keeps a half-understood line from launching the wrong thing.
func shimPathToken(token string) bool {
	if token == "" || strings.Contains(token, "%") || strings.ContainsAny(token, `<>|`) {
		return false
	}
	if !strings.ContainsAny(token, `\/`) {
		return true
	}
	return fileExists(token)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// splitShimTokens splits a batch launch line on spaces, honouring double quotes.
// Shims do not use escapes inside those quotes, so a quote simply opens or
// closes a token; an unbalanced line is rejected.
func splitShimTokens(line string) ([]string, bool) {
	var (
		tokens  []string
		current strings.Builder
		quoted  bool
		open    bool
	)
	flush := func() {
		if open {
			tokens = append(tokens, current.String())
			current.Reset()
			open = false
		}
	}
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			open = true
		case (r == ' ' || r == '\t' || r == '\r') && !quoted:
			flush()
		default:
			current.WriteRune(r)
			open = true
		}
	}
	if quoted {
		return nil, false
	}
	flush()
	return tokens, true
}
