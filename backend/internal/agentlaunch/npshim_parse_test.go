//go:build windows

package agentlaunch

import "testing"

func TestNpmShimInterpreterReadsElseBranch(t *testing.T) {
	tests := []struct {
		name        string
		interpreter string
		want        string
	}{
		{"node", "node", "node"},
		{"bun", "bun", ""},
		{"deno", "deno", ""},
		{"python3", "python3", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := shimBodyWithInterpreter(tc.interpreter, "")
			got := npmShimInterpreter(body)
			if got != tc.want {
				t.Fatalf("npmShimInterpreter() = %q, want %q", got, tc.want)
			}
		})
	}
}

// An interpreter-backed shim whose target is an extensionless script must never
// be mistaken for a native payload: that would execute the script directly,
// outside the runtime its shebang requires.
func TestNpmShimProgramRejectsUnknownInterpreter(t *testing.T) {
	for _, interpreter := range []string{"bun", "deno", "python3"} {
		t.Run(interpreter, func(t *testing.T) {
			program, ok := npmShimInvocation("C:\\shim", shimBodyWithInterpreter(interpreter, ""))
			if ok {
				t.Fatalf("npmShimInvocation() accepted a %s shim: %+v", interpreter, program)
			}
		})
	}
}

func TestNpmShimProgramAcceptsNodeInterpreter(t *testing.T) {
	program, ok := npmShimInvocation("C:\\shim", nodeShimBody)
	if !ok {
		t.Fatal("npmShimInvocation() rejected a node shim")
	}
	if !program.UsesInterpreter || program.Interpreter != "node" {
		t.Fatalf("program = %+v, want node interpreter", program)
	}
	if program.Target != `C:\shim\node_modules\pkg\bin\entry` {
		t.Fatalf("Target = %q", program.Target)
	}
}

// A package shipping a native binary has no interpreter at all: cmd-shim writes
// the target directly, and that must still be recognized.
func TestNpmShimProgramRecognizesNativePayload(t *testing.T) {
	body := "@ECHO off\r\n\"%dp0%\\node_modules\\pkg\\bin\\opencode.exe\"   %*\r\n"
	program, ok := npmShimInvocation("C:\\shim", body)
	if !ok {
		t.Fatal("npmShimInvocation() rejected a native payload shim")
	}
	if program.UsesInterpreter {
		t.Fatalf("native payload reported as interpreter-backed: %+v", program)
	}
	if program.Target != `C:\shim\node_modules\pkg\bin\opencode.exe` {
		t.Fatalf("Target = %q", program.Target)
	}
}

func TestQuotedTokensKeepsUnquotedArgsAndDropsStar(t *testing.T) {
	got := quotedTokens(`"%_prog%" --require ./bootstrap.js "%dp0%\a\b" %*`)
	want := []string{"%_prog%", "--require", "./bootstrap.js", `%dp0%\a\b`}
	if len(got) != len(want) {
		t.Fatalf("quotedTokens() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
}

// cmd-shim prefixes the invocation with `endLocal & goto ... || title %COMSPEC%`
// and chains another `&` before the program, so only the final segment is the
// invocation itself.
func TestQuotedTokensIgnoresCmdShimCommandChain(t *testing.T) {
	line := `endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & ` +
		`"%_prog%" "%dp0%\node_modules\pkg\bin\entry" %*`
	got := quotedTokens(line)
	want := []string{"%_prog%", `%dp0%\node_modules\pkg\bin\entry`}
	if !equalArgs(got, want) {
		t.Fatalf("quotedTokens() = %q, want %q", got, want)
	}
}

// A quoted npm entry path may itself contain `&` (e.g. `R&D.js`). The chain
// separator is the last `&` outside quotes, so slicing at the `&` inside the
// path would truncate the target and leave the shim on the cmd.exe path.
func TestQuotedTokensKeepsAmpersandInQuotedTarget(t *testing.T) {
	lines := []string{
		`"%_prog%" "%dp0%\node_modules\pkg\bin\R&D.js" %*`,
		`endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & ` +
			`"%_prog%" "%dp0%\node_modules\pkg\bin\R&D.js" %*`,
	}
	want := []string{"%_prog%", `%dp0%\node_modules\pkg\bin\R&D.js`}
	for _, line := range lines {
		got := quotedTokens(line)
		if !equalArgs(got, want) {
			t.Fatalf("quotedTokens(%q) = %q, want %q", line, got, want)
		}
	}
}
