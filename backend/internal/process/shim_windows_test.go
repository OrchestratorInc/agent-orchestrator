//go:build windows

package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// npmCmdShim is the shape npm writes for every global CLI, verbatim down to the
// `endLocal & goto` line: the launch is the last `&` segment.
const npmCmdShim = `@ECHO off
GOTO start
:find_dp0
SET dp0=%~dp0
EXIT /b
:start
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
  SET PATHEXT=%PATHEXT:;.JS;=;%
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\@openai\codex\bin\codex.js" %*
`

// yarnCmdShim is the shape yarn writes: the bundled Node launch and a bare
// `node` fallback, each on their own line.
const yarnCmdShim = `@IF EXIST "%~dp0\node.exe" (
  "%~dp0\node.exe"  "%~dp0\..\yarn\bin\yarn.js" %*
) ELSE (
  @SETLOCAL
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  node  "%~dp0\..\yarn\bin\yarn.js" %*
)
`

// bunCmdShim is the shape cmd-shim writes for an entry whose shebang picks a
// non-Node interpreter: the `%_prog%` machinery is identical to npm's, but the
// ELSE branch assigns `bun` (or `sh`, `deno`, ...). Starting the entry under
// Node would run it with the wrong runtime, so the shim must be declined.
const bunCmdShim = `@ECHO off
GOTO start
:find_dp0
SET dp0=%~dp0
EXIT /b
:start
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\bun.exe" (
  SET "_prog=%dp0%\bun.exe"
) ELSE (
  SET "_prog=bun"
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\pkg\cli.ts" %*
`

// pnpmCmdShim carries the NODE_PATH assignment pnpm writes before its launch
// line. Starting the target directly would drop that value, so the shim must
// be declined even though its launch line itself parses cleanly.
const pnpmCmdShim = `@ECHO off
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
  SET PATHEXT=%PATHEXT:;.JS;=;%
)

@SET "NODE_PATH=%dp0%\store\node_modules"
endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\pkg\bin\cli.js" %*
`

// nodeCmdShim is Node's own bundled npm.cmd, trimmed to the load-bearing lines:
// it assigns NODE_EXE/NPM_CLI_JS and forwards through them. This package can
// neither follow the unexpanded variables nor carry the assignments into a
// direct launch, so the shim keeps the ComSpec /d /s /c launch instead.
const nodeCmdShim = `@ECHO off
SETLOCAL

SET "NODE_EXE=%~dp0\node.exe"
IF NOT EXIST "%NODE_EXE%" (
  SET "NODE_EXE=node"
)
SET "NPM_CLI_JS=%~dp0\node_modules\npm\bin\npm-cli.js"
"%NODE_EXE%" "%NPM_CLI_JS%" %*
`

// codexHooksFlag is the argument that started #7212: a quote followed by `&`
// inside one -c value, which cmd.exe splits no matter how it is escaped.
const codexHooksFlag = `hooks.SessionStart=[{hooks=[{type="command",command="& \"C:\\Program Files\\AO\\ao.exe\" hooks codex session-start",timeout=5}]}]`

func writeShimFixture(t *testing.T, dir, shimName, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, shimName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, shimName)
}

// writeNodeFixture copies the running test binary as `node.exe` so shim fixtures
// resolve a bundled runtime without depending on one being installed.
func writeNodeFixture(t *testing.T, dir string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(dir, "node.exe")
	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, content, 0o700); err != nil {
		t.Fatal(err)
	}
	return node
}

func TestShimTargetArgvUnwrapsNPMLikeShims(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "node_modules", "@openai", "codex", "bin")
	if err := os.MkdirAll(entry, 0o700); err != nil {
		t.Fatal(err)
	}
	codexJS := filepath.Join(entry, "codex.js")
	if err := os.WriteFile(codexJS, []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "yarn", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	yarnJS := filepath.Join(dir, "yarn", "bin", "yarn.js")
	if err := os.WriteFile(yarnJS, []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		shimDir string
		shim    string
		content string
		script  string
	}{
		// The npm shim sits beside its bundled node.exe and its entry script.
		{"npm with bundled node", dir, "codex.cmd", npmCmdShim, codexJS},
		// The yarn shim uses its real `%~dp0\..\yarn` shape, which lands on the
		// sibling yarn tree once `..` is cleaned.
		{"yarn with bundled node", filepath.Join(dir, "bin"), "yarn.cmd", yarnCmdShim, yarnJS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(tc.shimDir, 0o700); err != nil {
				t.Fatal(err)
			}
			node := writeNodeFixture(t, tc.shimDir)
			shim := writeShimFixture(t, tc.shimDir, tc.shim, tc.content)
			got, ok := shimTargetArgv(shim, []string{"-c", codexHooksFlag})
			if !ok {
				t.Fatalf("shimTargetArgv(%q) = not ok, want the shim unwrapped", shim)
			}
			want := []string{node, tc.script, "-c", codexHooksFlag}
			if len(got) != len(want) {
				t.Fatalf("shimTargetArgv = %#v, want %#v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("shimTargetArgv[%d] = %q, want %q", i, got[i], want[i])
				}
			}
		})
	}
}

func TestShimTargetArgvRejectsShimsItCannotFollow(t *testing.T) {
	dir := t.TempDir()
	writeNodeFixture(t, dir)

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"no percent star", "@echo off\r\necho %~1\r\n"},
		{"percent star not last", `"%dp0%\run.js" %* --done`},
		{"unexpanded variable", `"%dp0%\node.exe" "%CONFIG_DIR%\run.js" %*`},
		{"missing entry script", `"%dp0%\node.exe" "%dp0%\missing.js" %*`},
		{"redirection", `"%dp0%\node.exe" "%dp0%\run.js" > log.txt %*`},
		{"unbalanced quotes", `"%dp0%\node.exe "%dp0%\run.js" %*`},
		// cmd-shim writes the entry point's shebang interpreter into _prog; a
		// bun entry must keep its shim launch rather than run under Node.
		{"non-node interpreter", bunCmdShim},
		// pnpm assigns NODE_PATH before the launch line; the unwrap would drop it.
		{"unknown SET variable", pnpmCmdShim},
		// Node's bundled npm.cmd forwards through NODE_EXE/NPM_CLI_JS variables
		// this rewrite does not carry; it keeps its ComSpec launch.
		{"node's bundled npm.cmd", nodeCmdShim},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim := writeShimFixture(t, dir, "tool.cmd", tc.content)
			if _, ok := shimTargetArgv(shim, []string{"arg"}); ok {
				t.Fatalf("shimTargetArgv(%q) unwrapped a shim it cannot follow", shim)
			}
		})
	}
}

// TestShimTargetArgvRejectsBatchLauncher covers shims whose launch line runs
// another batch file (yarn v1 global bins chain this way): starting that target
// would put a .cmd back in argv[0], and the line would be handed to cmd.exe
// again, reintroducing the #7212 quoting bug.
func TestShimTargetArgvRejectsBatchLauncher(t *testing.T) {
	dir := t.TempDir()
	inner := writeShimFixture(t, dir, "inner.cmd", "@echo off\r\n")
	shim := writeShimFixture(t, dir, "outer.cmd", "@ECHO off\r\n\""+inner+"\" %*\r\n")

	if _, ok := shimTargetArgv(shim, []string{"arg"}); ok {
		t.Fatalf("shimTargetArgv(%q) unwrapped a launch line that runs another batch file", shim)
	}
}

// TestShimTargetArgvRejectsNodeShimOnPath covers version managers that install
// a node.cmd: with no bundled node.exe the shim's `node` fallback resolves to a
// batch file, and unwrapping to it would start a batch file again.
func TestShimTargetArgvRejectsNodeShimOnPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entry.js"), []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeShimFixture(t, dir, "node.cmd", "@echo off\r\n")
	shim := writeShimFixture(t, dir, "tool.cmd", `@ECHO off
IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\entry.js" %*
`)
	t.Setenv("PATH", dir+`;`+os.Getenv("PATH"))

	if _, ok := shimTargetArgv(shim, []string{"arg"}); ok {
		t.Fatalf("shimTargetArgv(%q) unwrapped to a node.cmd batch file", shim)
	}
}

// TestShimArgvEchoHelper is the child for the end-to-end test below: the shim
// fixture points at a copy of the test binary, so this helper reports exactly
// what argv the child received.
func TestShimArgvEchoHelper(t *testing.T) {
	if os.Getenv("AO_SHIM_ARGV_ECHO") != "1" {
		t.Skip("helper for TestCommandContextStartsShimTargetWithExactArgv")
	}
	seen := false
	for _, arg := range os.Args[1:] {
		if arg == "--" {
			seen = true
			continue
		}
		if seen {
			fmt.Printf("%q\n", arg)
		}
	}
}

// TestCommandContextStartsShimTargetWithExactArgv is the #7212 regression: the
// hook override with its embedded quote, `&` and spaces must reach the child
// byte for byte, and the child must be the shim's target rather than cmd.exe.
// The shim directory contains a space, which the unwrapped CreateProcess launch
// handles without any quoting.
func TestCommandContextStartsShimTargetWithExactArgv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ao tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	node := writeNodeFixture(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "entry.js"), []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := writeShimFixture(t, dir, "tool.cmd", `@ECHO off
"%~dp0node.exe" -test.run=TestShimArgvEchoHelper -- "%~dp0entry.js" %*
`)

	cmd := CommandContext(context.Background(), shim, "-c", codexHooksFlag, `C:\repo with space\`)
	cmd.Env = append(os.Environ(), "AO_SHIM_ARGV_ECHO=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v\n%s", err, output)
	}
	if cmd.Path != node {
		t.Fatalf("cmd.Path = %q, want the shim target %q (cmd.exe would re-parse the arguments)", cmd.Path, node)
	}

	var got []string
	for _, line := range strings.Split(strings.TrimRight(string(output), "\r\n"), "\n") {
		// The test harness prints its own PASS bookkeeping around the helper.
		unquoted, unquoteErr := strconv.Unquote(line)
		if unquoteErr != nil {
			continue
		}
		got = append(got, unquoted)
	}
	want := []string{filepath.Join(dir, "entry.js"), "-c", codexHooksFlag, `C:\repo with space\`}
	if len(got) != len(want) {
		t.Fatalf("child argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("child argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCommandContextRunsDeclinedShimThroughCmdExe verifies the fallback: a shim
// this package cannot parse still runs, through cmd.exe /d /s /c, and a quoted
// argument survives it. Go's default batch launch would fail this exact case —
// a quoted shim path together with a quoted argument — with
// `'...\ao' is not recognized`, so both the shim directory and the shim name
// contain spaces.
func TestCommandContextRunsDeclinedShimThroughCmdExe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ao tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	shim := writeShimFixture(t, dir, "echo tool.cmd", "@echo off\r\necho %~1\r\n")

	cmd := CommandContext(context.Background(), shim, "argument with spaces")
	if cmd.SysProcAttr == nil || !strings.HasPrefix(cmd.SysProcAttr.CmdLine, `/d /s /c "`) {
		got := ""
		if cmd.SysProcAttr != nil {
			got = cmd.SysProcAttr.CmdLine
		}
		t.Fatalf("SysProcAttr.CmdLine = %q, want the /d /s /c launch", got)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "argument with spaces" {
		t.Fatalf("output = %q, want the argument preserved", got)
	}
}

// TestCommandContextRunsDeclinedShimWithoutComSpec covers the fallback's own
// fallback: with no ComSpec, the shell still has to be named by absolute path.
// Cmd.Start resolves a bare `cmd.exe` against cmd.Dir, which callers set to a
// workspace after construction, so a cmd.exe checked into a cloned project would
// be the one that runs. systemexec and previewserver used to hand the bare name
// to exec.Command, which resolved it in the daemon's environment; the moved
// fallback has to do that resolution itself.
func TestCommandContextRunsDeclinedShimWithoutComSpec(t *testing.T) {
	t.Setenv("ComSpec", "")
	dir := filepath.Join(t.TempDir(), "ao tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	shim := writeShimFixture(t, dir, "echo tool.cmd", "@echo off\r\necho %~1\r\n")

	cmd := CommandContext(context.Background(), shim, "argument with spaces")
	cmd.Dir = t.TempDir()
	if !filepath.IsAbs(cmd.Path) {
		t.Fatalf("cmd.Path = %q, want an absolute path to the system cmd.exe", cmd.Path)
	}
	if !strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") {
		t.Fatalf("cmd.Path = %q, want the system cmd.exe", cmd.Path)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shim without ComSpec: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "argument with spaces" {
		t.Fatalf("output = %q, want the argument preserved", got)
	}
}

// TestCommandResolvesShimNamedThroughPATH mirrors real callers, which name the
// tool and let PATH answer with the shim file.
func TestCommandResolvesShimNamedThroughPATH(t *testing.T) {
	dir := t.TempDir()
	node := writeNodeFixture(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "entry.js"), []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := writeShimFixture(t, dir, "aotool.cmd", `@ECHO off
"%~dp0node.exe" "%~dp0entry.js" %*
`)
	t.Setenv("PATH", dir+`;`+os.Getenv("PATH"))

	resolved, err := exec.LookPath("aotool")
	if err != nil {
		t.Fatalf("lookpath aotool: %v", err)
	}
	if resolved != shim {
		t.Fatalf("lookpath aotool = %q, want the shim %q", resolved, shim)
	}
	cmd := Command("aotool", "-c", codexHooksFlag)
	if cmd.Path != node {
		t.Fatalf("cmd.Path = %q, want the shim target %q, not cmd.exe", cmd.Path, node)
	}
}
