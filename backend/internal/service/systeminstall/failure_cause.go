package systeminstall

import (
	"path/filepath"
	"regexp"
	"strings"
)

// FailureCause is why a failed Job failed, from a closed list. It is derived
// from the job's error and output so it never carries their text, and the
// desktop can send it as analytics.
type FailureCause string

// The closed list of failure causes. Keep the Job field's enum tag in step.
const (
	// FailureTimeout: the install, or the check after it, ran past its deadline.
	FailureTimeout FailureCause = "timeout"
	// FailureNotFoundAfterInstall: the command finished but the executable
	// could not be found afterwards (not on PATH, or the adapter cannot resolve it).
	FailureNotFoundAfterInstall FailureCause = "not_found_after_install"
	// FailureVerifyFailed: the installed executable was found but did not run
	// or report the expected version.
	FailureVerifyFailed FailureCause = "verify_failed"
	FailureNodeMissing  FailureCause = "node_missing"
	FailureNPMMissing   FailureCause = "npm_missing"
	FailureBrewMissing  FailureCause = "brew_missing"
	// FailureToolMissing: another program the installer runs (curl, bash,
	// winget, apt-get...) is not installed.
	FailureToolMissing FailureCause = "tool_missing"
	// FailureXcodeToolsMissing: macOS Command Line Tools are missing or broken.
	FailureXcodeToolsMissing FailureCause = "xcode_tools_missing"
	FailureDiskFull          FailureCause = "disk_full"
	FailurePermissionDenied  FailureCause = "permission_denied"
	// FailureNetwork: a download or registry request failed (DNS, connection,
	// proxy, TLS or an HTTP error status).
	FailureNetwork FailureCause = "network"
	// FailureLocked: the package manager or harness was busy with another
	// install (a held brew or dpkg lock, an install already running).
	FailureLocked FailureCause = "locked"
	// FailureExitNonzero: the installer exited with an error none of the
	// above explains.
	FailureExitNonzero FailureCause = "exit_nonzero"
	FailureOther       FailureCause = "other"
)

var (
	verifyNotFoundText = []string{
		"is still not in path",
		"resolve installed ",
		"installed, but ", // managed: "Installed, but X was not found."
	}
	verifyFailedText = []string{
		"version probe",
		"binary identity check",
		"did not start.",
		"did not report its version",
	}
	// managed: "The new X reports 1.2, not 1.3."
	versionMismatch = regexp.MustCompile(`^the new .+ reports .+, not `)
	// The program a shell, env or exec could not find, in the forms Go, bash,
	// zsh, env, cmd.exe and PowerShell print.
	missingCommandPatterns = []*regexp.Regexp{
		regexp.MustCompile(`exec: "([^"]+)": executable file not found`),
		regexp.MustCompile(`fork/exec ([^:\s]+): no such file or directory`),
		regexp.MustCompile(`(?:^|[\s:/])env: ['‘"]?([\w.+-]+)['’"]?: No such file or directory`),
		regexp.MustCompile(`command not found: ([\w.+-]+)`),
		regexp.MustCompile(`([\w.+/-]+): command not found`),
		regexp.MustCompile(`: \d+: ([\w.+/-]+): not found`),
		regexp.MustCompile(`'([^']+)' is not recognized as (?:an internal or external command|the name of a cmdlet)`),
		regexp.MustCompile(`The term '([^']+)' is not recognized`),
	}
	xcodeToolsText = []string{
		"xcrun: error: invalid active developer path",
		"no developer tools were found",
		"command line tools are not installed",
		"install the command line tools",
		"xcode-select --install",
	}
	diskFullText = []string{
		"enospc",
		"no space left on device",
		"not enough space on the disk",
		"there is not enough space",
		"disk full",
		"disk quota exceeded",
	}
	permissionText = []string{
		"eacces",
		"eperm",
		"permission denied",
		"operation not permitted",
		"access is denied",
		"a terminal is required to read the password",
		"a password is required",
		"need sudo access",
		"is not writable",
		"insufficient permissions",
		"requires administrator",
		"run as administrator",
	}
	lockedText = []string{
		"another active homebrew process",
		"a `brew install` process has already locked",
		"has already locked",
		"could not get lock",
		"is being installed or updated",
		"another installation is in progress",
		"were not idle within", // an update waiting for sessions
	}
	networkText = []string{
		"enotfound",
		"econnrefused",
		"econnreset",
		"etimedout",
		"eai_again",
		"enetunreach",
		"ehostunreach",
		"socket hang up",
		"could not resolve host",
		"could not resolve proxy",
		"failed to connect to",
		"connection refused",
		"connection reset",
		"connection timed out",
		"network is unreachable",
		"no such host",
		"i/o timeout",
		"tls handshake",
		"x509:",
		"ssl certificate problem",
		"certificate verify failed",
		"unable_to_get_issuer_cert",
		"self_signed_cert_in_chain",
		"temporary failure in name resolution",
		"getaddrinfo",
		"network connectivity",
		"npm error network",
		"npm err! network",
		"curl: (",
		"download installer:",
		"unable to access 'http",
		"proxy authentication required",
	}
	exitText = []string{
		"exit status ",
		"exit code ",
		"signal: ",
		"the install command failed.",
	}
)

// classifyFailure is the cause of a job that ended in status, from its error
// message and output; empty unless the job failed. The message is checked
// first: it names what the daemon itself saw (a timeout, a check after a clean
// run), while the output is only what the installer printed.
func classifyFailure(status Status, message, output string) FailureCause {
	if status != StatusFailed {
		return ""
	}
	lowerMessage := strings.ToLower(message)
	switch {
	case strings.Contains(lowerMessage, "timed out after"):
		return FailureTimeout
	case containsAny(lowerMessage, verifyNotFoundText):
		return FailureNotFoundAfterInstall
	case containsAny(lowerMessage, verifyFailedText), versionMismatch.MatchString(lowerMessage):
		return FailureVerifyFailed
	}
	for _, text := range []string{message, output} {
		if cause := missingCommandCause(text); cause != "" {
			return cause
		}
	}
	lowerBoth := lowerMessage + "\n" + strings.ToLower(output)
	switch {
	case containsAny(lowerBoth, xcodeToolsText):
		return FailureXcodeToolsMissing
	case containsAny(lowerBoth, diskFullText):
		return FailureDiskFull
	case containsAny(lowerBoth, lockedText):
		return FailureLocked
	case containsAny(lowerBoth, permissionText):
		return FailurePermissionDenied
	case containsAny(lowerBoth, networkText):
		return FailureNetwork
	case containsAny(lowerMessage, exitText):
		return FailureExitNonzero
	}
	return FailureOther
}

// missingCommandCause names the missing program text reports, if any.
func missingCommandCause(text string) FailureCause {
	for _, pattern := range missingCommandPatterns {
		match := pattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		name := strings.ToLower(filepath.Base(strings.ReplaceAll(match[1], `\`, "/")))
		for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1"} {
			name = strings.TrimSuffix(name, suffix)
		}
		switch name {
		case "node", "nodejs":
			return FailureNodeMissing
		case "npm", "npx":
			return FailureNPMMissing
		case "brew":
			return FailureBrewMissing
		}
		return FailureToolMissing
	}
	return ""
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
