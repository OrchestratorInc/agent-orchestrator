//go:build windows

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// maxShimSize bounds how much of a batch file is read while looking for its
// launch line. npm, pnpm and yarn shims are a few hundred bytes; anything far
// larger is not a shim worth parsing.
const maxShimSize = 64 << 10

// ResolveBatchShim rewrites a launch argv whose program is a Windows batch shim
// so the shim's own target is started directly.
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
// The shim path must already be resolved by the caller (exec.LookPath or
// exec.Command have both). The rewrite is deliberately conservative: false is
// returned unless the shim matches a shape this package understands and every
// path it names exists. Command and CommandContext run a shim this function
// declines through cmd.exe /d /s /c instead; a caller that wants to decide for
// itself branches on the result.
func ResolveBatchShim(shim string, args []string) ([]string, bool) {
	if !isBatchFile(shim) {
		return nil, false
	}
	return shimTargetArgv(shim, args)
}

// rewriteBatchShim launches a command whose program resolved to a Windows batch
// shim: as the shim's own target when the shim can be followed (see
// ResolveBatchShim), or through cmd.exe /d /s /c with quoted tokens otherwise.
// Go's default batch launch, which a plain exec.Command would use, cannot carry
// a quoted shim path together with a quoted argument (`C:\Program
// Files\nodejs\npm.cmd` would fail as `'C:\Program' is not recognized`), and it
// drops /d, so cmd AutoRun scripts would run again.
//
// exec.Command has already resolved cmd.Path, so no extra PATH scan is spent on
// launches that are not shims. The cmd.exe launch carries the whole command
// line in SysProcAttr.CmdLine: callers that edit cmd.Args after construction
// get the rewritten argv for an unwrapped shim, but not for this fallback.
func rewriteBatchShim(cmd *exec.Cmd) {
	if !isBatchFile(cmd.Path) {
		return
	}
	shim := cmd.Path
	if argv, ok := shimTargetArgv(shim, cmd.Args[1:]); ok {
		cmd.Path = argv[0]
		cmd.Args = argv
		return
	}
	args := cmd.Args[1:]
	shell := strings.TrimSpace(os.Getenv("ComSpec"))
	if shell == "" {
		shell = systemCommandShell()
	}
	cmd.Path = shell
	cmd.Args = nil
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = `/d /s /c "` + windowsBatchCommandLine(shim, args) + `"`
}

// systemCommandShell names the batch interpreter for the rare environment that
// has no ComSpec. It returns an absolute path for the same reason ComSpec
// normally is one: Cmd.Start resolves a bare name against cmd.Dir, which
// callers point at a workspace after construction, so a cmd.exe checked into a
// cloned project would be the one that runs.
func systemCommandShell() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", "cmd.exe")
	}
	if resolved, err := exec.LookPath("cmd.exe"); err == nil {
		return resolved
	}
	return "cmd.exe"
}

func isBatchFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}

// cmd.exe uses different quoting rules from native Windows executables. The
// outer quotes are consumed by /s /c; each token remains quoted for paths and
// arguments containing spaces. Doubling embedded quotes preserves them for the
// batch shim instead of allowing them to terminate the command early.
func windowsBatchCommandLine(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWindowsBatchArg(executable))
	for _, arg := range args {
		parts = append(parts, quoteWindowsBatchArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsBatchArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
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
	// A shim that assigns variables this rewrite does not carry into the child
	// (pnpm's NODE_PATH, for example) would lose them when its target is
	// started directly, so it keeps the shim launch that applies them.
	text := string(content)
	if setsUnknownVariables(text) {
		return nil, false
	}
	interpreter := shimInterpreter(text)
	dir := filepath.Dir(shim)
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "%*") {
			continue
		}
		if argv, ok := shimLineArgv(line, dir, interpreter, args); ok {
			return argv, true
		}
	}
	return nil, false
}

// shimLineArgv parses one shim line that forwards `%*`. npm chains its setup and
// the real launch onto a single line with `&`; yarn and pnpm put the launch on
// its own line, which the same parse handles because there is nothing to split.
func shimLineArgv(line, dir, interpreter string, args []string) ([]string, bool) {
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
	program, ok := shimProgram(argv[0], dir, interpreter)
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
//
// `%_prog%` is only followed when the shim's ELSE branch assigns Node to it:
// cmd-shim writes the entry point's shebang interpreter there (`bun`, `sh`,
// `deno`, ...), and starting such an entry under Node would run it with the
// wrong runtime. A launcher that is itself a batch file is declined too —
// chaining would hand the line back to cmd.exe and reintroduce the #7212
// quoting bug for that layout.
func shimProgram(token, dir, interpreter string) (string, bool) {
	if token == "%_prog%" {
		if !isNodeInterpreter(interpreter) {
			return "", false
		}
		if bundled := filepath.Join(dir, "node.exe"); fileExists(bundled) {
			return bundled, true
		}
		node, err := exec.LookPath("node")
		if err != nil || isBatchFile(node) {
			return "", false
		}
		return node, true
	}
	if strings.Contains(token, "%") {
		return "", false
	}
	if !strings.ContainsAny(token, `\/`) {
		resolved, err := exec.LookPath(token)
		if err != nil || isBatchFile(resolved) {
			return "", false
		}
		return resolved, true
	}
	cleaned := filepath.Clean(token)
	if !fileExists(cleaned) || isBatchFile(cleaned) {
		return "", false
	}
	return cleaned, true
}

// isNodeInterpreter reports whether name is a Node runtime. Only Node-backed
// shims are unwrapped; any other interpreter keeps its shim launch.
func isNodeInterpreter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "node", "nodejs":
		return true
	default:
		return false
	}
}

// shimInterpreter recovers the interpreter cmd-shim assigns to `_prog` when the
// runtime bundled beside the shim is missing. cmd-shim writes the candidates in
// an IF/ELSE pair — a `.exe` beside the shim, then the bare interpreter name —
// and the ELSE branch holds that name (`node` for npm CLIs, the entry point's
// shebang interpreter otherwise).
func shimInterpreter(content string) string {
	const elseMarker = ") ELSE ("
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(strings.ToUpper(line), elseMarker) {
			continue
		}
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(strings.TrimSuffix(next, "\r"))
			name, ok := strings.CutPrefix(next, `SET "_prog=`)
			if !ok {
				continue
			}
			return strings.Trim(strings.TrimSuffix(name, `"`), `"`)
		}
	}
	return ""
}

// setsUnknownVariables reports whether the shim assigns an environment variable
// other than the ones the launch line itself depends on. pnpm's shims, for
// example, run `@SET "NODE_PATH=<store>\node_modules"` before the launch line;
// starting their target directly would drop that value and break CLIs that
// resolve dependencies through it. Only dp0, _prog and PATHEXT — assigned by
// every npm/yarn shape this package follows — are recognised.
func setsUnknownVariables(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		name, ok := assignedVariable(line)
		if !ok {
			continue
		}
		switch name {
		case "DP0", "_PROG", "PATHEXT":
		default:
			return true
		}
	}
	return false
}

// assignedVariable extracts the variable name a batch `SET` line assigns, with
// the same case-insensitivity cmd.exe applies. Lines that do not assign
// anything (SETLOCAL, echoes, control flow) are not SET lines.
func assignedVariable(line string) (string, bool) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	line = strings.TrimSpace(strings.TrimPrefix(line, "@"))
	upper := strings.ToUpper(line)
	var rest string
	switch {
	case strings.HasPrefix(upper, "SET "):
		rest = strings.TrimSpace(line[len("SET "):])
	case strings.HasPrefix(upper, `SET"`):
		rest = strings.TrimPrefix(line[len("SET"):], `"`)
	default:
		return "", false
	}
	rest = strings.TrimPrefix(rest, `"`)
	name, _, _ := strings.Cut(rest, "=")
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return "", false
	}
	return name, true
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
