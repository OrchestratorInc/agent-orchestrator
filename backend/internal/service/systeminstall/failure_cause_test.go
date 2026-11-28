package systeminstall

import (
	"reflect"
	"strings"
	"testing"
)

func TestClassifyFailure(t *testing.T) {
	tests := []struct {
		name    string
		message string
		output  string
		want    FailureCause
	}{
		{name: "install timeout", message: "install timed out after 15m0s", output: "npm error code ETIMEDOUT", want: FailureTimeout},
		{name: "agent update timeout", message: "update timed out after 15m0s", want: FailureTimeout},
		{name: "verify timeout", message: "run codex version probe: timed out after 30s (a newly installed CLI can be slow on its first launch; choose Verify again): context deadline exceeded", want: FailureTimeout},
		{name: "system binary not on PATH", message: "install command finished but gh is still not in PATH", output: "==> Pouring gh--2.63.0.arm64_sonoma.bottle.tar.gz", want: FailureNotFoundAfterInstall},
		{name: "adapter cannot resolve", message: `resolve installed codex binary: exec: "codex": executable file not found in $PATH`, want: FailureNotFoundAfterInstall},
		{name: "managed binary missing", message: "Installed, but Claude Code was not found.", want: FailureNotFoundAfterInstall},
		{name: "version probe failed", message: "run opencode version probe: exit status 1", output: "npm warn ETIMEDOUT retrying", want: FailureVerifyFailed},
		{name: "managed copy did not start", message: "The new Codex did not start.", want: FailureVerifyFailed},
		{name: "managed version mismatch", message: "The new Codex reports 0.40.0, not 0.41.0.", want: FailureVerifyFailed},
		{name: "node missing under npm", message: "exit status 127", output: "env: node: No such file or directory\n", want: FailureNodeMissing},
		{name: "node missing linux env", message: "exit status 127", output: "/usr/bin/env: ‘node’: No such file or directory\n", want: FailureNodeMissing},
		{name: "npm missing exec", message: `exec: "npm": executable file not found in $PATH`, want: FailureNPMMissing},
		{name: "npm missing bash", message: "exit status 127", output: "bash: line 1: npm: command not found\n", want: FailureNPMMissing},
		{name: "npm missing zsh", message: "exit status 127", output: "zsh:1: command not found: npm\n", want: FailureNPMMissing},
		{name: "npm missing windows", message: "exit status 9009", output: "'npm' is not recognized as an internal or external command,\r\noperable program or batch file.", want: FailureNPMMissing},
		{name: "optional probe then network", message: "exit status 1", output: "pyenv: command not found\ncurl: (6) Could not resolve host: github.com\n", want: FailureNetwork},
		{name: "probe only, ordinary exit", message: "exit status 1", output: "foo: command not found\nError: install failed\n", want: FailureExitNonzero},
		{name: "npm missing after a probe, exit 127", message: "exit status 127", output: "pyenv: command not found\nInstalling with npm...\nbash: line 4: npm: command not found\n", want: FailureNPMMissing},
		{name: "managed npm missing", message: "The install command failed.", output: "bash: line 1: npm: command not found\nThe install command failed.\nexit status 127\n", want: FailureNPMMissing},
		{name: "managed ordinary exit", message: "The install command failed.", output: "bash: line 1: npm: command not found\nThe install command failed.\nexit status 1\n", want: FailureExitNonzero},
		{name: "nested 127 not on the last line", message: "exit status 1", output: "foo: command not found\nsub-step: exit status 127\nError: install failed\n", want: FailureExitNonzero},
		{name: "brew missing exec", message: `exec: "brew": executable file not found in $PATH`, want: FailureBrewMissing},
		{name: "brew missing by path", message: "fork/exec /opt/homebrew/bin/brew: no such file or directory", want: FailureBrewMissing},
		{name: "other tool missing", message: "exit status 127", output: "sh: 1: curl: not found\n", want: FailureToolMissing},
		{name: "winget missing", message: `exec: "winget": executable file not found in %PATH%`, want: FailureToolMissing},
		{name: "xcode tools", message: "exit status 1", output: "xcrun: error: invalid active developer path (/Library/Developer/CommandLineTools), missing xcrun at: /Library/Developer/CommandLineTools/usr/bin/xcrun", want: FailureXcodeToolsMissing},
		{name: "disk full npm", message: "exit status 228", output: "npm error code ENOSPC\nnpm error syscall write", want: FailureDiskFull},
		{name: "disk full brew", message: "exit status 1", output: "Error: No space left on device @ rb_sysopen", want: FailureDiskFull},
		{name: "npm EACCES", message: "exit status 243", output: "npm error code EACCES\nnpm error path /usr/local/lib/node_modules", want: FailurePermissionDenied},
		{name: "sudo prompt", message: "exit status 1", output: "sudo: a terminal is required to read the password; either use the -S option", want: FailurePermissionDenied},
		{name: "windows access denied", message: "exit status 5", output: "Access is denied.", want: FailurePermissionDenied},
		{name: "managed folder permission", message: "Could not create the install folder.", output: "\nCould not create the install folder.\ncreate managed install directory: mkdir /Users/x/.ao/bin: permission denied\n", want: FailurePermissionDenied},
		{name: "brew lock", message: "exit status 1", output: "Error: Another active Homebrew process is already using /opt/homebrew.", want: FailureLocked},
		{name: "apt not root", message: "exit status 100", output: "E: Could not open lock file /var/lib/dpkg/lock-frontend - open (13: Permission denied)\nE: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), are you root?", want: FailurePermissionDenied},
		{name: "apt lock", message: "exit status 100", output: "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 1234 (apt)", want: FailureLocked},
		{name: "harness busy", message: "systeminstall: an install is already running: opencode is being installed or updated; retry once it finishes", want: FailureLocked},
		{name: "npm network", message: "exit status 1", output: "npm error code ENOTFOUND\nnpm error network request to https://registry.npmjs.org/x failed", want: FailureNetwork},
		{name: "curl network", message: "exit status 6", output: "curl: (6) Could not resolve host: github.com", want: FailureNetwork},
		{name: "go download", message: "download installer: Get \"https://claude.ai/install.sh\": dial tcp: lookup claude.ai: no such host", want: FailureNetwork},
		{name: "download HTTP status", message: "download installer: HTTP 503 Service Unavailable", want: FailureNetwork},
		{name: "tls proxy", message: "exit status 1", output: "npm error code SELF_SIGNED_CERT_IN_CHAIN", want: FailureNetwork},
		{name: "plain exit", message: "exit status 1", output: "Error: something unexpected happened", want: FailureExitNonzero},
		{name: "killed", message: "signal: killed", want: FailureExitNonzero},
		{name: "winget not found wraps exit", message: "winget could not find package GitHub.cli. Run `winget source update` and retry. (exit status 0x8a150014)", want: FailureExitNonzero},
		{name: "managed command failed", message: "The install command failed.", want: FailureExitNonzero},
		{name: "unknown", message: "remote installer runner is not configured", want: FailureOther},
		{name: "empty", want: FailureOther},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyFailure(StatusFailed, test.message, test.output); got != test.want {
				t.Fatalf("classifyFailure(%q, %q) = %q, want %q", test.message, test.output, got, test.want)
			}
		})
	}
}

func TestClassifyFailureOnlyForFailedJobs(t *testing.T) {
	for _, status := range []Status{StatusSucceeded, StatusInterrupted, StatusUnsupported, StatusRunning, StatusVerifying, StatusIdle} {
		if got := classifyFailure(status, "exit status 1", "EACCES"); got != "" {
			t.Fatalf("classifyFailure(%q) = %q, want empty", status, got)
		}
	}
}

// The Job field's enum tag is what the API and the desktop's allowlist read,
// so it must list exactly the causes classifyFailure can return.
func TestFailureCauseEnumMatchesTheClosedList(t *testing.T) {
	want := []FailureCause{
		FailureTimeout, FailureNotFoundAfterInstall, FailureVerifyFailed, FailureNodeMissing, FailureNPMMissing,
		FailureBrewMissing, FailureToolMissing, FailureXcodeToolsMissing, FailureDiskFull, FailurePermissionDenied,
		FailureNetwork, FailureLocked, FailureExitNonzero, FailureOther,
	}
	names := make([]string, len(want))
	for index, cause := range want {
		names[index] = string(cause)
	}
	field, ok := reflect.TypeOf(Job{}).FieldByName("FailureCause")
	if !ok {
		t.Fatal("Job has no FailureCause field")
	}
	if got := field.Tag.Get("enum"); got != strings.Join(names, ",") {
		t.Fatalf("FailureCause enum = %q, want %q", got, strings.Join(names, ","))
	}
}
