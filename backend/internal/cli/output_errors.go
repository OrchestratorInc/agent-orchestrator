package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/datadirlock"
)

// jsonFlag is the root's persistent --json flag. Commands with JSON output
// define their own "json" flag, which shadows this one; when this one is the
// flag a command resolved, that command has no JSON output yet.
const jsonFlag = "json"

// jsonModeError marks a failure of a command run with --json, so the
// entrypoint writes it as a JSON envelope instead of plain text.
type jsonModeError struct{ err error }

func (e jsonModeError) Error() string { return e.err.Error() }
func (e jsonModeError) Unwrap() error { return e.err }

// cliErrorEnvelope mirrors the daemon's API error envelope, so scripts and
// agents parse one shape whether the CLI or the daemon refused the call.
type cliErrorEnvelope struct {
	Error cliErrorBody `json:"error"`
}

type cliErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
	Status    int    `json:"status,omitempty"`
	ExitCode  int    `json:"exitCode"`
}

// CLI-side error codes. Daemon errors keep the daemon's own code.
const (
	errCodeUsage             = "USAGE"
	errCodeDaemonUnavailable = "DAEMON_UNAVAILABLE"
	errCodeDataDirLocked     = "DATA_DIR_LOCKED"
	errCodeError             = "ERROR"
)

// rejectUnsupportedJSON refuses --json on a command that has no JSON output,
// so a caller that asked for JSON never receives text on success.
func rejectUnsupportedJSON(root, cmd *cobra.Command) error {
	flag := cmd.Flags().Lookup(jsonFlag)
	if flag == nil || !flag.Changed || flag != root.PersistentFlags().Lookup(jsonFlag) {
		return nil
	}
	return usageError{fmt.Errorf("%s does not support --json yet", cmd.CommandPath())}
}

// jsonRequested reports whether the invocation asked for JSON. It reads the
// executed command's parsed flag, and falls back to the raw arguments when
// parsing failed before --json was reached.
func jsonRequested(executed *cobra.Command, args []string) bool {
	if executed != nil {
		if flag := executed.Flags().Lookup(jsonFlag); flag != nil && flag.Changed {
			on, _ := strconv.ParseBool(flag.Value.String())
			return on
		}
	}
	skipValue := false
	for _, arg := range args {
		if skipValue {
			// The previous flag consumed this token as its value, so a
			// literal "--json" here (`--project --json`) is data.
			skipValue = false
			continue
		}
		if arg == "--" {
			break
		}
		if executed != nil && strings.HasPrefix(arg, "--") && !strings.Contains(arg, "=") && arg != "--json" {
			if flag := executed.Flags().Lookup(arg[2:]); flag != nil && flag.NoOptDefVal == "" {
				skipValue = true
				continue
			}
		}
		if arg == "--json" {
			return true
		}
		if len(arg) > len("--json=") && arg[:len("--json=")] == "--json=" {
			on, err := strconv.ParseBool(arg[len("--json="):])
			return err == nil && on
		}
	}
	return false
}

// PrintError writes a failed command's error to w: a JSON envelope on one
// line when the command ran with --json, otherwise the plain message. The
// envelope is always the last line of stderr; a command may have written
// progress or warnings to stderr before it.
func PrintError(w io.Writer, err error) {
	if err == nil {
		return
	}
	var jm jsonModeError
	if !errors.As(err, &jm) {
		_, _ = fmt.Fprintln(w, err)
		return
	}
	_ = writeJSONLine(w, cliErrorEnvelope{Error: errorBody(jm.err)})
}

func errorBody(err error) cliErrorBody {
	body := cliErrorBody{Code: errCodeError, Message: err.Error(), ExitCode: ExitCode(err)}
	var api apiResponseError
	var usage usageError
	switch {
	case errors.As(err, &api):
		body.Status = api.StatusCode
		body.RequestID = api.ErrorBody.RequestID
		if api.ErrorBody.Code != "" {
			body.Code = api.ErrorBody.Code
		}
		// Use the daemon's bare message only when the error is the daemon
		// response itself; a wrapped or joined error keeps its full text
		// (for example a claim failure plus a failed restoration).
		if api.ErrorBody.Message != "" && err.Error() == api.Error() {
			body.Message = api.ErrorBody.Message
		}
	case errors.As(err, &usage):
		body.Code = errCodeUsage
	case errors.Is(err, errDaemonUnavailable):
		body.Code = errCodeDaemonUnavailable
	case errors.Is(err, datadirlock.ErrLocked):
		body.Code = errCodeDataDirLocked
	}
	return body
}
