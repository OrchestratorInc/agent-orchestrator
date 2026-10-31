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
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim := writeShimFixture(t, dir, "tool.cmd", tc.content)
			if _, ok := shimTargetArgv(shim, []string{"arg"}); ok {
				t.Fatalf("shimTargetArgv(%q) unwrapped a shim it cannot follow", shim)
			}
		})
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
func TestCommandContextStartsShimTargetWithExactArgv(t *testing.T) {
	dir := t.TempDir()
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

// TestCommandContextKeepsUnfamiliarShim verifies the fallback: a shim this
// package cannot parse keeps its previous cmd.exe path, and plain arguments
// (no quotes) survive it.
func TestCommandContextKeepsUnfamiliarShim(t *testing.T) {
	dir := t.TempDir()
	shim := writeShimFixture(t, dir, "tool.cmd", "@echo off\r\necho %~1\r\n")

	cmd := CommandContext(context.Background(), shim, "argument with spaces")
	if cmd.Path != shim {
		t.Fatalf("cmd.Path = %q, want the shim kept for an unfamiliar launch line", cmd.Path)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v\n%s", err, output)
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
