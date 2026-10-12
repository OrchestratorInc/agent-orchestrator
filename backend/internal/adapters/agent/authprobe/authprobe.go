package authprobe

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// CmdRunner runs the command and returns the combined stdout/stderr.
// It is exposed as a package variable to allow mocking in tests.
var CmdRunner = func(ctx context.Context, name string, arg ...string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, name, arg...).CombinedOutput()
}

// CLIStatus runs bounded local CLI probes and classifies their output.
// Callers must pass adapter-specific commands; catalog refresh should not run
// a generic sequence of auth-like commands against every installed binary.
func CLIStatus(ctx context.Context, binary string, commands [][]string) (ports.AgentAuthStatus, error) {
	return CLIStatusWithTimeout(ctx, binary, commands, 3*time.Second)
}

// Classifier reads one probe's raw output. The bool reports whether it
// recognized the shape at all; false defers to the shared prose classifier.
type Classifier func(out []byte) (ports.AgentAuthStatus, bool)

// CLIStatusWithClassifier is CLIStatus for a CLI whose status output the shared
// prose classifier cannot read — structured output, or prose wrapped around it.
// The adapter's classifier is consulted first and the shared one remains the
// fallback, so an unrecognized shape still gets the ordinary treatment.
func CLIStatusWithClassifier(ctx context.Context, binary string, commands [][]string, classify Classifier) (ports.AgentAuthStatus, error) {
	return cliStatus(ctx, binary, commands, 3*time.Second, classify)
}

// CLIStatusWithTimeout is CLIStatus with an adapter-specific per-command
// timeout for CLIs whose native status command has documented startup work.
func CLIStatusWithTimeout(ctx context.Context, binary string, commands [][]string, timeout time.Duration) (ports.AgentAuthStatus, error) {
	return cliStatus(ctx, binary, commands, timeout, nil)
}

func cliStatus(ctx context.Context, binary string, commands [][]string, timeout time.Duration, classify Classifier) (ports.AgentAuthStatus, error) {
	status, _, err := CLIStatusWithDiagnostics(ctx, binary, commands, timeout, classify)
	return status, err
}

// CLIStatusWithDiagnostics retains each bounded probe's outcome without
// changing the legacy verdict or exposing its stdout/stderr. Commands must be
// adapter-owned status commands with no credentials in their arguments.
func CLIStatusWithDiagnostics(ctx context.Context, binary string, commands [][]string, timeout time.Duration, classify Classifier) (ports.AgentAuthStatus, []domain.AgentAuthDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, nil, err
	}
	if binary == "" || len(commands) == 0 {
		return ports.AgentAuthStatusUnknown, nil, nil
	}
	steps := make([]domain.AgentAuthDiagnostic, 0, len(commands))
	for _, args := range commands {
		status, step, err := commandStatus(ctx, binary, args, timeout, classify)
		steps = append(steps, step)
		if err != nil || status != ports.AgentAuthStatusUnknown {
			return status, steps, err
		}
	}
	return ports.AgentAuthStatusUnknown, steps, nil
}

func commandStatus(ctx context.Context, binary string, args []string, timeout time.Duration, classify Classifier) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	out, err := CmdRunner(probeCtx, binary, args...)
	status := ports.AgentAuthStatusUnknown
	if probeCtx.Err() == nil {
		recognized := false
		if classify != nil {
			status, recognized = classify(out)
		}
		if !recognized {
			status = StatusFromText(string(out))
		}
	}
	step := CommandDiagnostic(filepath.Base(binary)+" "+strings.Join(args, " "), status, err, probeCtx.Err(), started)
	if probeCtx.Err() != nil {
		if probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return status, step, nil
		}
		return status, step, probeCtx.Err()
	}
	return status, step, nil
}

// CommandDiagnostic classifies execution facts; it never copies err.Error().
func CommandDiagnostic(method string, status ports.AgentAuthStatus, err, contextErr error, started time.Time) domain.AgentAuthDiagnostic {
	code := "output_unrecognized"
	switch {
	case errors.Is(contextErr, context.DeadlineExceeded):
		code = "probe_timeout"
	case contextErr != nil:
		code = "probe_cancelled"
	case status == ports.AgentAuthStatusUnauthorized:
		code = "cli_signed_out"
	case status == ports.AgentAuthStatusAuthorized || status == ports.AgentAuthStatusConfigured:
		code = "cli_signed_in"
	case err != nil:
		code = "command_failed"
	}
	step := domain.NewAgentAuthDiagnostic(method, code)
	step.DurationMs = time.Since(started).Milliseconds()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		step.ExitCode = &code
	} else if err == nil {
		code := 0
		step.ExitCode = &code
	}
	return step
}

// StatusFromText classifies common CLI auth/status output.
func StatusFromText(out string) ports.AgentAuthStatus {
	text := strings.ToLower(out)
	compactText := compact(text)
	if hasAny(text,
		"not logged in",
		"not currently logged in",
		"logged out",
		"not authenticated",
		"unauthenticated",
		"authentication required",
		"not authorized",
		"unauthorized",
		"login required",
		"no credentials",
		"0 credentials",
		"no api key",
		"no token",
		`"loggedin": false`,
		`"loggedin":false`,
	) || hasAny(compactText,
		// A null account is a CLI saying "nobody is signed in" in JSON rather
		// than prose. Kiro's `whoami --format json` answers exactly this when
		// signed out, and matched none of the phrases above.
		`"account":null`,
		`'account':null`,
		"account:null",
		`"authenticated":false`,
		`'authenticated':false`,
		"authenticated:false",
		"authenticated=false",
		`"authorized":false`,
		`'authorized':false`,
		"authorized:false",
		"authorized=false",
		`"logged_in":false`,
		`'logged_in':false`,
		"logged_in:false",
		"logged_in=false",
		`"loggedin":false`,
		`'loggedin':false`,
		"loggedin:false",
		"loggedin=false",
	) {
		return ports.AgentAuthStatusUnauthorized
	}
	if hasAny(text,
		"logged in",
		"authenticated",
		"authorized",
		"token valid",
		"api key found",
		"credentials found",
		`"loggedin": true`,
		`"loggedin":true`,
	) {
		return ports.AgentAuthStatusAuthorized
	}
	return ports.AgentAuthStatusUnknown
}

func compact(text string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(text)
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
