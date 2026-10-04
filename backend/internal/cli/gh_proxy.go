package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

var createdGitHubPRURL = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*$`)

// ExecuteGH is the gh executable alias entrypoint. It deliberately bypasses
// Cobra: gh owns argument parsing, help, streams, and exit codes.
func ExecuteGH() int {
	c := commandContext{deps: DefaultDeps()}
	c.deps.RunInteractiveCommand = runGHCommand
	realGH, err := agentlaunch.RealGH(os.Getenv("PATH"))
	if err != nil {
		_, _ = fmt.Fprintln(c.deps.Err, err)
		return 127
	}
	return c.runGH(context.Background(), realGH, os.Args[1:])
}

func (c *commandContext) runGH(ctx context.Context, executable string, args []string) int {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	capture := sessionIDPattern.MatchString(sessionID) && os.Getenv("AO_REVIEW_SESSION_ID") == "" && ghCreatesPR(args)
	// Observing stdout through a pipe would change gh's terminal detection and
	// interactive prompts. Keep terminal invocations fully transparent.
	if file, ok := c.deps.Out.(*os.File); ok && term.IsTerminal(file.Fd()) {
		capture = false
	}
	var output ghOutput
	stdout := c.deps.Out
	if capture {
		stdout = io.MultiWriter(stdout, &output)
	}
	err := c.deps.RunInteractiveCommand(ctx, executable, args, c.deps.In, stdout, c.deps.Err)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code := exit.ExitCode(); code >= 0 {
				return code
			}
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal())
			}
			return 1
		}
		_, _ = fmt.Fprintln(c.deps.Err, err)
		return 1
	}
	if capture {
		ref := strings.TrimSpace(output.String())
		if !output.overflow && createdGitHubPRURL.MatchString(ref) {
			c.registerGHPR(ctx, sessionID, ref)
		}
	}
	return 0
}

// gh's global repository flag can precede the command. Stop at the command so
// flag values containing "pr create" cannot be mistaken for an invocation.
func ghCreatesPR(args []string) bool {
	for len(args) > 0 {
		switch {
		case args[0] == "-R" || args[0] == "--repo":
			if len(args) < 2 {
				return false
			}
			args = args[2:]
		case strings.HasPrefix(args[0], "--repo=") || strings.HasPrefix(args[0], "-R") && len(args[0]) > 2:
			args = args[1:]
		default:
			if len(args) < 2 || args[0] != "pr" || args[1] != "create" {
				return false
			}
			for _, arg := range args[2:] {
				if arg == "--help" || arg == "-h" || arg == "--web" || arg == "-w" || arg == "--dry-run" {
					return false
				}
			}
			return true
		}
	}
	return false
}

// Bound observation memory without truncating the real command's output.
type ghOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *ghOutput) String() string { return b.buffer.String() }
func (b *ghOutput) Len() int       { return b.buffer.Len() }

func (b *ghOutput) Write(p []byte) (int, error) {
	const limit = 4096
	n := len(p)
	if n > limit-b.Len() {
		b.overflow = true
		p = p[:limit-b.Len()]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (c *commandContext) registerGHPR(ctx context.Context, sessionID, ref string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Exactly one attempt. The standard client preserves API code/request ID;
	// Server faults use the daemon's existing logging/Sentry policy.
	err := c.postJSON(ctx, "sessions/"+url.PathEscape(sessionID)+"/pr/claim", claimPRRequest{PR: ref, AllowTakeover: false}, nil)
	if err == nil {
		return
	}
	msg := fmt.Sprintf("ao gh: PR created, but AO could not attach it: %v; attach with ao session claim-pr %s %s", err, sessionID, ref)
	_, _ = fmt.Fprintln(c.deps.Err, msg)
	dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	if filepath.IsAbs(dataDir) {
		appendHooksLog(dataDir, fmt.Sprintf("%s session=%s %s\n", time.Now().UTC().Format(time.RFC3339), sessionID, msg))
	}
}
