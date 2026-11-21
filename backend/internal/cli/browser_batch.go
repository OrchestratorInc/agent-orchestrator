package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// maxBrowserBatchSteps bounds one batch so a runaway script cannot hold the
// shared page for long; split longer flows into several batches.
const maxBrowserBatchSteps = 50

// browserBatchReadCommands print their output for successful steps: they
// exist to return page state. Every other successful step prints one status
// line, so the agent pays tokens only for what it asked to read.
var browserBatchReadCommands = map[string]bool{
	"get": true, "snapshot": true, "tabs": true, "console": true, "errors": true,
	"screenshot": true, "status": true, "network": true, "dialog": true,
}

// browserBatchForbiddenCommands are not page actions: nesting, and commands
// that change which browser AO drives or install host integration.
var browserBatchForbiddenCommands = map[string]string{
	"batch":  "a batch cannot contain another batch",
	"chrome": "Chrome mode setup is not a page action; run it on its own",
	"target": "switching the browser target is not a page action; run it on its own",
}

type browserBatchStepResult struct {
	Index      int      `json:"index"`
	Args       []string `json:"args"`
	Status     string   `json:"status"`
	DurationMS int64    `json:"durationMs,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Output     string   `json:"output,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type browserBatchResult struct {
	OK         bool                     `json:"ok"`
	Total      int                      `json:"total"`
	Completed  int                      `json:"completed"`
	FailedStep int                      `json:"failedStep,omitempty"`
	DurationMS int64                    `json:"durationMs"`
	Steps      []browserBatchStepResult `json:"steps"`
	Snapshot   string                   `json:"snapshot,omitempty"`
}

// browserBatchObservation is what one step's runBrowserAction reported back
// through commandContext.browserResultObserver.
type browserBatchObservation struct {
	action string
	result map[string]any
}

func newBrowserBatchCommand(ctx *commandContext, jsonOutput *bool) *cobra.Command {
	var inline, snapshotMode string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "batch [file|-]",
		Short: "Run several browser commands in one call, stopping at the first failure",
		Long: "Run several `ao browser` commands in one call. Each step is an ordinary\n" +
			"`ao browser` command line without the `ao browser` prefix, one per line\n" +
			"(blank lines and # comments are ignored), or a JSON array whose items are\n" +
			"argument arrays or command-line strings. Steps come from --steps, a file,\n" +
			"or standard input (the default, or `-`; a terminal is refused).\n\n" +
			"For the desktop browser and Chrome mode, every step is checked for a known\n" +
			"command, valid flags, and argument count before any step runs. On a remote\n" +
			"host (AO_AGENT_BROWSER_URL) the service checks each step's syntax when it\n" +
			"runs, so a typo fails after earlier steps ran. Steps then run in order through the same path as the\n" +
			"single command, and the batch stops at the first failure: a command error,\n" +
			"an `act` that is ambiguous or matches nothing, or an `act` postcondition that\n" +
			"is unmet or cancelled.\n\n" +
			"Output is one status line per step, printed as each step finishes, with the\n" +
			"summary line last. Read steps (get, snapshot, tabs, console,\n" +
			"errors, screenshot, status, network, dialog) also print their output; the\n" +
			"failed step prints its full output. With --snapshot auto (default) a failed\n" +
			"batch ends with an interactive snapshot so you can recover without another call.",
		Example: "  # after a snapshot of the login page showed button \"Sign in\" [ref=e2]\n" +
			"  ao browser batch <<'EOF'\n" +
			"  act \"Email\" --action fill --value ada@example.test\n" +
			"  act \"Password\" --action fill --value hunter2\n" +
			"  click e2\n" +
			"  wait --text \"Welcome\"\n" +
			"  get text body\n" +
			"  EOF",
		Args: rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch snapshotMode {
			case "auto", "always", "never":
			default:
				return usageError{errors.New("--snapshot must be auto, always, or never")}
			}
			input, err := readBrowserBatchInput(cmd, inline, args)
			if err != nil {
				return err
			}
			steps, err := parseBrowserBatch(input)
			if err != nil {
				return usageError{err}
			}
			// A remote agent browser has its own command syntax (`click @e1`,
			// `tab list`), so only the desktop tree can be checked up front.
			remote, _, err := remoteBrowserForward(os.Getenv, []string{"browser", "status"})
			if err != nil {
				return err
			}
			for i, step := range steps {
				if err := validateBrowserBatchStep(step, remote == nil); err != nil {
					return usageError{fmt.Errorf("step %d (%s): %w", i+1, browserBatchEcho(step), err)}
				}
			}
			// Text output streams one line per step as it finishes, so a
			// harness that kills a long batch still shows which steps ran.
			var onStep func(browserBatchStepResult) error
			if !*jsonOutput {
				onStep = func(step browserBatchStepResult) error {
					return writeBrowserBatchStep(cmd.OutOrStdout(), step, verbose)
				}
			}
			result, err := ctx.runBrowserBatch(cmd.Context(), steps, snapshotMode, onStep)
			if err != nil {
				return err
			}
			if *jsonOutput {
				if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			} else if err := writeBrowserBatchSummary(cmd.OutOrStdout(), result); err != nil {
				return err
			}
			if !result.OK {
				return fmt.Errorf("browser batch stopped at step %d of %d", result.FailedStep, result.Total)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&inline, "steps", "", "steps inline (newline-separated commands or a JSON array) instead of a file or stdin")
	cmd.Flags().StringVar(&snapshotMode, "snapshot", "auto", "end with an interactive snapshot: auto (only on failure), always, or never")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "print the output of every step, not only read steps")
	return cmd
}

// browserBatchStdinIsTerminal is a variable so tests can stand in a terminal.
var browserBatchStdinIsTerminal = func(in io.Reader) bool {
	file, ok := in.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

func readBrowserBatchInput(cmd *cobra.Command, inline string, args []string) (string, error) {
	if inline != "" {
		if len(args) > 0 {
			return "", usageError{errors.New("use either --steps or a file argument, not both")}
		}
		return inline, nil
	}
	if len(args) == 1 && args[0] != "-" {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return "", fmt.Errorf("read batch file: %w", err)
		}
		return string(data), nil
	}
	// Reading a terminal would wait for EOF that an agent never sends.
	if browserBatchStdinIsTerminal(cmd.InOrStdin()) {
		return "", usageError{errors.New("no batch steps: pass --steps, a file, or pipe them in (ao browser batch <<'EOF' ... EOF)")}
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("read batch steps from stdin: %w", err)
	}
	return string(data), nil
}

// parseBrowserBatch turns batch input into argument lists. A JSON array is
// accepted for callers that already hold structured data; otherwise each
// non-blank, non-comment line is one command with shell-style quoting.
func parseBrowserBatch(input string) ([][]string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, errors.New("no batch steps given")
	}
	var steps [][]string
	if strings.HasPrefix(trimmed, "[") {
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
			return nil, fmt.Errorf("parse batch JSON: %w", err)
		}
		for i, item := range raw {
			var line string
			if err := json.Unmarshal(item, &line); err == nil {
				step, err := splitBrowserBatchLine(line)
				if err != nil {
					return nil, fmt.Errorf("step %d: %w", i+1, err)
				}
				steps = append(steps, step)
				continue
			}
			var step []string
			if err := json.Unmarshal(item, &step); err != nil {
				return nil, fmt.Errorf("step %d: must be a command string or an array of strings", i+1)
			}
			steps = append(steps, step)
		}
	} else {
		for n, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			step, err := splitBrowserBatchLine(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			steps = append(steps, step)
		}
	}
	if len(steps) == 0 {
		return nil, errors.New("no batch steps given")
	}
	if len(steps) > maxBrowserBatchSteps {
		return nil, fmt.Errorf("a batch runs at most %d steps (got %d); split it", maxBrowserBatchSteps, len(steps))
	}
	for i, step := range steps {
		if len(step) == 0 {
			return nil, fmt.Errorf("step %d is empty", i+1)
		}
		// Agents often paste the full command; accept it.
		if len(step) > 2 && step[0] == "ao" && step[1] == "browser" {
			steps[i] = step[2:]
		}
	}
	return steps, nil
}

// splitBrowserBatchLine splits one command line like a POSIX shell would for
// plain words, single quotes, double quotes, and backslash escapes. It never
// expands variables or globs: a batch is data, not a shell script.
func splitBrowserBatchLine(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	inWord := false
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				current.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\r':
			if inWord {
				args = append(args, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if escaped {
		return nil, errors.New("trailing backslash")
	}
	if inWord {
		args = append(args, current.String())
	}
	return args, nil
}

// validateBrowserBatchStep resolves the step against the real command tree
// and checks flags and argument count, so a typo in step 5 fails the batch
// before step 1 touches the page.
func validateBrowserBatchStep(step []string, checkTree bool) error {
	// A leading flag would let Cobra skip ahead to a forbidden command
	// (`--json=false batch ...`), and remote steps are never resolved here.
	if strings.HasPrefix(step[0], "-") {
		return errors.New("a step must start with a browser command, not a flag")
	}
	if reason, ok := browserBatchForbiddenCommands[step[0]]; ok {
		return errors.New(reason)
	}
	if step[0] == "help" {
		return errors.New("help is not a batch step")
	}
	// Only flags are checked here: `help` or `--json` typed as a value
	// (`fill e2 help`) is page data.
	for _, arg := range step {
		if arg == "--" {
			break
		}
		switch {
		case arg == "--json" || strings.HasPrefix(arg, "--json="):
			return errors.New("--json is set on the batch, not on a step")
		case arg == "-h" || arg == "--help":
			return errors.New("help is not a batch step")
		}
	}
	if !checkTree {
		return nil
	}
	tree := newBrowserCommand(&commandContext{})
	target, rest, err := tree.Find(step)
	if err != nil {
		return err
	}
	if target == tree || target.RunE == nil {
		return fmt.Errorf("unknown browser command %q", strings.Join(step, " "))
	}
	// Check the command Cobra resolved, not only the first word.
	top := target
	for top.Parent() != tree {
		top = top.Parent()
	}
	if reason, ok := browserBatchForbiddenCommands[top.Name()]; ok {
		return errors.New(reason)
	}
	if err := target.ParseFlags(rest); err != nil {
		return err
	}
	positional := target.Flags().Args()
	if target.Args != nil {
		if err := target.Args(target, positional); err != nil {
			return err
		}
	}
	// PreRunE holds each command's option-combination checks and has no side
	// effects, so running it here rejects `--expect-text a --expect-url b`
	// before an earlier step has touched the page.
	if target.PreRunE != nil {
		if err := target.PreRunE(target, positional); err != nil {
			return err
		}
	}
	return nil
}

// browserBatchIsReadStep reports whether a step only returns page state, in
// either the desktop syntax or the remote agent-browser syntax (`tab list`).
func browserBatchIsReadStep(step []string) bool {
	if browserBatchReadCommands[step[0]] {
		return true
	}
	return len(step) > 1 && step[0] == "tab" && step[1] == "list"
}

func (c *commandContext) runBrowserBatch(ctx context.Context, steps [][]string, snapshotMode string, onStep func(browserBatchStepResult) error) (browserBatchResult, error) {
	started := c.batchNow()
	result := browserBatchResult{OK: true, Total: len(steps)}
	// An `act` with no match already printed the latest snapshot; a second
	// copy would only cost tokens.
	failureHasSnapshot := false
	for i, step := range steps {
		stepResult := browserBatchStepResult{Index: i + 1, Args: step, Status: "skipped"}
		if !result.OK {
			result.Steps = append(result.Steps, stepResult)
			if onStep != nil {
				if err := onStep(stepResult); err != nil {
					return result, err
				}
			}
			continue
		}
		stepStarted := c.batchNow()
		output, observations, err := c.runBrowserBatchStep(ctx, step)
		stepResult.DurationMS = c.batchNow().Sub(stepStarted).Milliseconds()
		stepResult.Output = output
		summary, failure := browserBatchAssess(observations)
		stepResult.Summary = summary
		switch {
		case err != nil:
			stepResult.Status = "failed"
			stepResult.Error = err.Error()
		case failure != "":
			stepResult.Status = "failed"
			stepResult.Error = failure
		default:
			stepResult.Status = "ok"
			result.Completed++
		}
		if stepResult.Status == "failed" {
			result.OK = false
			result.FailedStep = i + 1
			for _, observation := range observations {
				if outcome, _ := observation.result["outcome"].(string); observation.action == "act" && outcome == "no-match" {
					failureHasSnapshot = true
				}
			}
		}
		result.Steps = append(result.Steps, stepResult)
		if onStep != nil {
			if err := onStep(stepResult); err != nil {
				return result, err
			}
		}
	}
	if snapshotMode == "always" || (snapshotMode == "auto" && !result.OK && !failureHasSnapshot) {
		output, _, err := c.runBrowserBatchStep(ctx, []string{"snapshot", "--interactive"})
		if err != nil {
			result.Snapshot = "snapshot unavailable: " + err.Error()
		} else {
			result.Snapshot = strings.TrimRight(output, "\n")
		}
	}
	result.DurationMS = c.batchNow().Sub(started).Milliseconds()
	return result, nil
}

func (c *commandContext) batchNow() time.Time {
	if c.deps.Now != nil {
		return c.deps.Now()
	}
	return time.Now()
}

// runBrowserBatchStep runs one step exactly as `ao browser <step...>` would,
// capturing its output. A configured remote agent browser receives the step
// through the same forwarder as a single command; otherwise the step runs
// through a fresh browser command tree (fresh flag state per step) that sends
// the same daemon request, so postconditions, ref checks, and any approval the
// browser runtime requires apply to each step unchanged.
func (c *commandContext) runBrowserBatchStep(ctx context.Context, step []string) (string, []browserBatchObservation, error) {
	var out bytes.Buffer
	remote, rest, err := remoteBrowserForward(os.Getenv, append([]string{"browser"}, step...))
	if err != nil {
		return "", nil, err
	}
	if remote != nil {
		deps := c.deps
		deps.Out, deps.Err = &out, &out
		err := runRemoteBrowserCommand(ctx, deps, *remote, rest)
		return out.String(), nil, err
	}
	var observations []browserBatchObservation
	stepCtx := &commandContext{
		deps: c.deps,
		browserResultObserver: func(action string, result map[string]any) {
			observations = append(observations, browserBatchObservation{action: action, result: result})
		},
	}
	tree := newBrowserCommand(stepCtx)
	tree.SilenceErrors, tree.SilenceUsage = true, true
	tree.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	tree.SetArgs(step)
	tree.SetOut(&out)
	tree.SetErr(&out)
	tree.SetIn(strings.NewReader(""))
	err = tree.ExecuteContext(ctx)
	return out.String(), observations, err
}

// browserBatchAssess turns a step's structured results into a short summary
// and, for an `act` that did not do what was asked, a failure reason. The
// single command reports these outcomes with exit 0 and leaves the judgment
// to the reader; a batch must stop instead of acting on a page it misread.
func browserBatchAssess(observations []browserBatchObservation) (summary, failure string) {
	for _, observation := range observations {
		if observation.action != "act" {
			continue
		}
		outcome, _ := observation.result["outcome"].(string)
		switch outcome {
		case "ambiguous":
			candidates, _ := observation.result["candidates"].([]any)
			return "", fmt.Sprintf("act was ambiguous (%d candidates); refine the description or add --nth", len(candidates))
		case "no-match":
			return "", "act matched no element"
		case "matched":
		default:
			continue
		}
		candidate, _ := observation.result["candidate"].(map[string]any)
		role, _ := candidate["role"].(string)
		ref, _ := observation.result["resolvedRef"].(string)
		summary = fmt.Sprintf("matched %s [ref=%s]", role, ref)
		if postcondition, ok := observation.result["postcondition"].(map[string]any); ok {
			status, _ := postcondition["status"].(string)
			kind, _ := postcondition["kind"].(string)
			summary += fmt.Sprintf("; postcondition %s: %s", kind, status)
			if status == "unmet" || status == "cancelled" {
				reason, _ := postcondition["reason"].(string)
				failure = fmt.Sprintf("postcondition %s %s", kind, status)
				if reason != "" {
					failure += " (" + reason + ")"
				}
			}
		}
		if navigation, ok := observation.result["navigation"].(map[string]any); ok {
			if status, _ := navigation["status"].(string); status == "cancelled" && failure == "" {
				reason, _ := navigation["reason"].(string)
				failure = "navigation cancelled (" + reason + ")"
			}
		}
	}
	return summary, failure
}

// writeBrowserBatchStep prints one finished (or skipped) step.
func writeBrowserBatchStep(w io.Writer, step browserBatchStepResult, verbose bool) error {
	var b strings.Builder
	label := map[string]string{"ok": "ok", "failed": "FAILED", "skipped": "not run"}[step.Status]
	fmt.Fprintf(&b, "[%d] %s: %s", step.Index, label, browserBatchEcho(step.Args))
	if step.Summary != "" {
		fmt.Fprintf(&b, " -> %s", step.Summary)
	}
	if step.Error != "" {
		fmt.Fprintf(&b, "\n    error: %s", step.Error)
	}
	b.WriteString("\n")
	showOutput := step.Status == "failed" || (step.Status == "ok" && (verbose || browserBatchIsReadStep(step.Args)))
	if output := strings.TrimRight(step.Output, "\n"); showOutput && output != "" {
		b.WriteString(output + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeBrowserBatchSummary prints the final snapshot and the summary line
// after the streamed steps.
func writeBrowserBatchSummary(w io.Writer, result browserBatchResult) error {
	var b strings.Builder
	if result.Snapshot != "" {
		b.WriteString("Final snapshot (interactive):\n" + result.Snapshot + "\n")
	}
	if result.OK {
		fmt.Fprintf(&b, "Batch completed: %d/%d steps ok (%d ms).\n", result.Completed, result.Total, result.DurationMS)
	} else {
		fmt.Fprintf(&b, "Batch stopped at step %d of %d: %d ok, 1 failed, %d not run (%d ms).\n",
			result.FailedStep, result.Total, result.Completed, result.Total-result.Completed-1, result.DurationMS)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// browserBatchEcho prints a step back compactly: long values are elided so a
// filled paragraph does not cost its length again in every result.
func browserBatchEcho(step []string) string {
	const maxArg = 48
	parts := make([]string, 0, len(step))
	for _, arg := range step {
		if len([]rune(arg)) > maxArg {
			arg = string([]rune(arg)[:maxArg]) + "…"
		}
		if arg == "" || strings.ContainsAny(arg, " \t\"'\\") {
			arg = strconv.Quote(arg)
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}
