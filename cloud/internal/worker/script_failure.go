package worker

import (
	"io"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// What a failed Setup or Cleanup step reports beyond its output, so the
// person can tell what failed without opening the sandbox: the command, the
// Linux user and groups it ran as, where it ran, its full log, and a hint for
// the failures a sandbox's user setup causes (no Docker, no sudo).

const (
	// setupLogsDir, in the worker data directory, holds each Setup step's full
	// output as step-<n>.log. The session event keeps only the tail.
	setupLogsDir = "setup-logs"
	// scriptLogLimit bounds one step's log file.
	scriptLogLimit = 4 << 20
	// scriptCommandLimit bounds the command shown with a failure.
	scriptCommandLimit = 300
)

var ranAsPattern = regexp.MustCompile(`(?m)^ran as ([A-Za-z0-9_.][A-Za-z0-9_.-]*) `)

// ScriptIdentity is the Linux user a Setup or Cleanup step runs as.
type ScriptIdentity struct {
	User   string
	Groups []string
}

// CurrentScriptIdentity is the user and groups this worker runs commands as.
// Names it cannot resolve are reported by number.
func CurrentScriptIdentity() ScriptIdentity {
	identity := ScriptIdentity{User: strconv.Itoa(os.Getuid())}
	if current, err := user.Current(); err == nil && current.Username != "" {
		identity.User = current.Username
	}
	gids, err := os.Getgroups()
	if err != nil {
		return identity
	}
	seen := map[string]bool{}
	for _, gid := range append([]int{os.Getgid()}, gids...) {
		name := strconv.Itoa(gid)
		if group, err := user.LookupGroupId(name); err == nil && group.Name != "" {
			name = group.Name
		}
		if !seen[name] {
			seen[name] = true
			identity.Groups = append(identity.Groups, name)
		}
	}
	return identity
}

// ScriptFailureHint explains the common ways a step fails because of who it
// ran as, from its output and exit status; "" when nothing matches. runAs may
// be empty when it is not known.
func ScriptFailureHint(output string, exitCode int, runAs string) string {
	runAs = strings.TrimSpace(runAs)
	if runAs == "" {
		// A Cleanup failure names its user in the output.
		if match := ranAsPattern.FindStringSubmatch(output); match != nil {
			runAs = match[1]
		}
	}
	who := "the sandbox user"
	if runAs != "" {
		who = runAs
	}
	lower := strings.ToLower(output)
	denied := strings.Contains(lower, "permission denied") || strings.Contains(lower, "eacces") ||
		strings.Contains(lower, "operation not permitted")
	switch {
	case strings.Contains(lower, "permission denied (publickey"):
		// Not a file permission: ssh had no key the host accepts.
		return "A git or ssh command in this step could not sign in with an SSH key: the sandbox has none for that host. " +
			"Use the repository's https:// URL, which AO's GitHub access covers, or add a key to the workspace image."
	case strings.Contains(lower, "docker.sock") && denied,
		strings.Contains(lower, "cannot connect to the docker daemon") && denied:
		return "This step ran as " + who + ", which cannot use Docker: add " + who +
			" to the docker group in the workspace image or template, then reopen the session."
	case strings.Contains(lower, "sudo:") && (strings.Contains(lower, "password is required") ||
		strings.Contains(lower, "not in the sudoers") || strings.Contains(lower, "terminal is required") ||
		strings.Contains(lower, "no tty present") || strings.Contains(lower, "not allowed to execute")):
		return "This step ran as " + who + ", which cannot use sudo: install what it needs in the workspace image, " +
			"or give " + who + " passwordless sudo for it."
	case denied:
		return "This step ran as " + who + ", which was refused access to a file or directory: " +
			"check that " + who + " owns or can write the path in the step's output."
	case exitCode == 127 || strings.Contains(lower, "command not found") || strings.Contains(lower, ": not found"):
		return "A program this step runs is not installed in the sandbox, or not on " + who + "'s PATH."
	}
	return ""
}

// scriptCommandSummary is a step's command as the person wrote it (a
// repository step without the cd AO wraps it in), on one line, redacted and
// bounded, and the repository folder it ran in ("" for the main repository).
// It is redacted (with the step events' redaction, redactor) before it is cut,
// so a cut cannot leave part of a secret that redaction no longer recognizes.
func scriptCommandSummary(stored string, redactor scriptRedactor) (command, repository string) {
	repository, command, _ = domain.ParseRepoScriptCommand(stored)
	command = redactor.redactCommand(command)
	if len(command) > scriptCommandLimit {
		command = strings.ToValidUTF8(command[:scriptCommandLimit], "") + "..."
	}
	return command, repository
}

// cappedFile writes to a file until limit bytes, then drops the rest and
// notes the cut once.
type cappedFile struct {
	file    *os.File
	left    int
	clipped bool
}

func (c *cappedFile) Write(p []byte) (int, error) {
	n := min(len(p), c.left)
	if n > 0 {
		_, _ = c.file.Write(p[:n])
		c.left -= n
	}
	if n < len(p) && !c.clipped {
		c.clipped = true
		_, _ = io.WriteString(c.file, "\n[AO: output beyond this point was not kept]\n")
	}
	return len(p), nil
}
