package worker

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A Cleanup run's step events. The control plane runs the Cleanup hook
// remotely and only gets its output, so the commands script prints a marker
// line around each command (CleanupCommandsScript) and CleanupStepEvents
// splits the output back into steps:
//
//	@@ao-step-<nonce> log <path>
//	@@ao-step-<nonce> <n> <total> start <epochMs> <repository or /> <command>
//	@@ao-step-<nonce> <n> <total> end <exitCode> <durationMs> [<timeoutSeconds>]
//	@@ao-step-<nonce> <n> <total> folder <folder>
//	@@ao-step-<nonce> <n> <total> skip <reason> <repository> <command>
//
// A skip marker stands for a step the script did not run, for reason: so far
// only scriptSkipForeignFolder, an additional repository's folder that is not
// AO's (ForeignRepoFolderHint). The folder marker before it names that folder
// as Setup's skip names it: absolute, or below the workspace user's home with
// "~/" (DisplayPath). A script from before folder markers prints none; its
// folder is named relative to the main checkout.
//
// A script from before per-step time limits leaves the timeout out; its steps
// had ProjectCleanupStepTimeout.
// The nonce is random per script, so a command that prints something that
// looks like a marker is read as output.

const (
	scriptMarkerPrefix = "@@ao-step-"
	// scriptMarkerMainRepository stands for the main repository in a start
	// marker; a repository folder never contains a slash.
	scriptMarkerMainRepository = "/"
	// scriptSkipForeignFolder is a skip marker's reason for a step of an
	// additional repository whose folder is not AO's.
	scriptSkipForeignFolder = "foreign_folder"
	// ownedRepoFoldersEnv, set to 1 by a Cleanup hook whose session's
	// checkouts must be AO's own (OwnedRepoFoldersOnly), makes its commands
	// script skip the steps of an additional repository whose folder is not.
	ownedRepoFoldersEnv = "AO_CLEANUP_OWNED_REPO_FOLDERS_ONLY"
	// cleanupUserHomeEnv, set by a Cleanup hook to the workspace login user's
	// home (UserHomeEnv) when the worker knows it, lets its commands script
	// name a skipped step's folder below it with "~/", as Setup does.
	cleanupUserHomeEnv = "AO_CLEANUP_USER_HOME"
)

var (
	scriptMarkerPattern = regexp.MustCompile(scriptMarkerPrefix + `([0-9a-f]{16}) `)
	cleanupRanAsPattern = regexp.MustCompile(`(?m)^ran as (\S+) \(groups: ([^)]*)\) in (.+)$`)
	cleanupLogPattern   = regexp.MustCompile(`(?m)^full cleanup output: (\S.*)$`)
)

// NewScriptMarkerNonce returns a random nonce for a commands script's markers.
func NewScriptMarkerNonce() string {
	var random [8]byte
	_, _ = rand.Read(random[:])
	return hex.EncodeToString(random[:])
}

// CleanupRunID is the run id of a Cleanup whose commands script marks its
// steps with nonce. The hook runs its commands once and prints that run's
// output again when it is run again (a control plane that restarted while
// deleting the sandbox), so the same run keeps its id rather than appearing
// twice.
func CleanupRunID(nonce string) string {
	return "cleanup-" + nonce
}

// CleanupRun describes one run of the Cleanup hook for CleanupStepEvents.
type CleanupRun struct {
	// RunID names a run whose output has no markers; one with markers is
	// named by their nonce (CleanupRunID).
	RunID string
	// Nonce is the marker nonce of the commands script the control plane
	// installed for this run, and Commands that script's commands (each led by
	// its time limit's marker when it sets one); both are empty when the hook
	// ran the commands the worker installed, whose nonce is then read from the
	// output.
	Nonce    string
	Commands []string
	// KnownCommands are what the hook otherwise runs, as best known: the
	// commands the worker was last given (stored when it connected), or the
	// project's current Cleanup. With Commands they say which values the run
	// assigns to secret-named variables (a step that sets one prints it with
	// nothing naming it), masked in its output; both count, as a current
	// Cleanup the hook could not install leaves the worker's commands to run.
	KnownCommands []string
	// User is the account the hook runs as when the control plane knows it
	// (the worker account it provisioned): a failed step whose output does
	// not say whom it ran as (it was stopped before the commands script could
	// print that) is reported as run by it.
	User string
	// ExitCode is the hook's exit status, and StartedAt and Duration when the
	// run started and how long it took.
	ExitCode  int
	StartedAt time.Time
	Duration  time.Duration
}

// redactor masks a run's output: the values its commands (Commands and
// KnownCommands) assign to secret-named variables, then the patterns.
func (run CleanupRun) redactor() scriptRedactor {
	return newScriptRedactor(nil, assignedSecretValues(append(append([]string{}, run.Commands...), run.KnownCommands...)))
}

// RedactOutput masks credentials in the run's output as its step events are
// masked: the script redaction (RedactScriptOutput) and the values its
// commands assign to secret-named variables.
func (run CleanupRun) RedactOutput(text string) string {
	return run.redactor().redact(activity.StripControl(collapseCarriageReturns(text)))
}

type cleanupStep struct {
	event       ScriptStepEvent
	startedMs   int64
	output      strings.Builder
	headDropped bool
	finished    bool
}

// CleanupStepEvents splits a Cleanup hook's output into one final event per
// step of the run, ordered by step: the steps that ran with their redacted
// output tails, and the ones a failure skipped. Output that lost its
// beginning (the hook prints only a tail) still reports every step: one whose
// start is no longer shown succeeded, as a later one ran. A hook without
// markers (a commands script from before them) is reported as a single step
// with the run's exit status and output. Nothing is reported for a silent,
// successful run without markers (no Cleanup commands).
func CleanupStepEvents(output string, run CleanupRun) []ScriptStepEvent {
	nonce, own := run.Nonce, run.Nonce != ""
	output = strings.ToValidUTF8(output, "")
	if nonce == "" || !strings.Contains(output, scriptMarkerPrefix+nonce+" ") {
		match := scriptMarkerPattern.FindStringSubmatch(output)
		if match == nil {
			return cleanupEventsWithoutMarkers(output, run)
		}
		nonce, own = match[1], match[1] == run.Nonce
	}
	prefix := scriptMarkerPrefix + nonce + " "
	// The run's commands, when the control plane knows them, say which values
	// they assign to secret-named variables: a step that sets one prints it
	// with nothing naming it.
	redactor := run.redactor()
	// The run's own commands and time limits, when the control plane wrote its
	// script; the end markers carry each step's limit too.
	var ownCommands []string
	var ownTimeouts []time.Duration
	if own {
		ownCommands, ownTimeouts = splitScriptSteps(run.Commands, domain.DefaultCleanupStepTimeoutMinutes)
	}
	stepTimeout := func(step int) time.Duration {
		if own && len(ownTimeouts) >= step {
			return ownTimeouts[step-1]
		}
		return 0
	}
	steps := map[int]*cleanupStep{}
	// The folder markers' folders, by step.
	folders := map[int]string{}
	var current *cleanupStep
	var preamble, trailer strings.Builder
	logPath, total, lastFinished := "", 0, 0
	sawLog := false
	write := func(text string) {
		switch {
		case current != nil:
			current.output.WriteString(text + "\n")
		case lastFinished == 0:
			preamble.WriteString(text + "\n")
		default:
			trailer.WriteString(text + "\n")
		}
	}
	finish := func(step *cleanupStep, exitCode int, duration, timeout time.Duration) {
		if timeout <= 0 {
			timeout = stepTimeout(step.event.Step)
		}
		if timeout > 0 {
			step.event.TimeoutMinutes = timeoutMinutes(timeout)
		} else {
			timeout = ProjectCleanupStepTimeout
		}
		status := SetupSucceeded
		switch {
		case (exitCode == 124 || exitCode == 137) && duration >= timeout-time.Second:
			status, exitCode = SetupTimedOut, -1
		case exitCode != 0:
			status = SetupFailed
		}
		step.event.finish(status, exitCode, duration)
		step.finished = true
		lastFinished = step.event.Step
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		index := strings.Index(line, prefix)
		if index < 0 {
			write(line)
			continue
		}
		if before := line[:index]; before != "" {
			if current != nil {
				// The step ended mid-line: its output's unfinished last line,
				// kept without a newline so its end is read as one.
				current.output.WriteString(before)
			} else {
				write(before)
			}
		}
		marker := line[index+len(prefix):]
		if path, ok := strings.CutPrefix(marker, "log "); ok {
			logPath, sawLog = strings.TrimSpace(path), true
			continue
		}
		fields := strings.SplitN(marker, " ", 4)
		if len(fields) < 3 {
			write(line)
			continue
		}
		step, stepErr := strconv.Atoi(fields[0])
		count, countErr := strconv.Atoi(fields[1])
		if stepErr != nil || countErr != nil || step < 1 || count < step || count > 1000 {
			write(line)
			continue
		}
		rest := ""
		if len(fields) == 4 {
			rest = fields[3]
		}
		total = max(total, count)
		switch fields[2] {
		case "folder":
			folders[step] = activity.Truncate(strings.TrimSpace(rest), 1024)
		case "start":
			if current != nil && !current.finished {
				// A start without the previous step's end: that step stopped
				// the script without reporting.
				finish(current, -1, 0, 0)
			}
			parts := strings.SplitN(rest, " ", 3)
			current = &cleanupStep{event: ScriptStepEvent{Step: step}}
			if milliseconds, err := strconv.ParseInt(parts[0], 10, 64); err == nil && milliseconds > 0 {
				current.startedMs = milliseconds
			}
			if len(parts) > 1 && parts[1] != scriptMarkerMainRepository {
				current.event.Repository = parts[1]
			}
			if len(parts) > 2 {
				current.event.Command = boundScriptCommand(redactor.redactCommand(parts[2]))
			}
			steps[step] = current
		case "skip":
			if current != nil && !current.finished {
				finish(current, -1, 0, 0)
			}
			parts := strings.SplitN(rest, " ", 3)
			skipped := &cleanupStep{event: ScriptStepEvent{Step: step, Status: ScriptStepSkipped}, finished: true}
			if len(parts) > 1 && parts[1] != scriptMarkerMainRepository {
				skipped.event.Repository = parts[1]
			}
			if len(parts) > 2 {
				skipped.event.Command = boundScriptCommand(redactor.redactCommand(parts[2]))
			}
			if parts[0] == scriptSkipForeignFolder && skipped.event.Repository != "" {
				folder := folders[step]
				if folder == "" {
					folder = "../" + skipped.event.Repository
				}
				skipped.event.Hint = ForeignRepoFolderHint(folder)
			}
			steps[step] = skipped
			current, lastFinished = nil, step
		case "end":
			parts := strings.Fields(rest)
			exitCode, duration, timeout := -1, time.Duration(0), time.Duration(0)
			if len(parts) > 0 {
				if code, err := strconv.Atoi(parts[0]); err == nil {
					exitCode = code
				}
			}
			if len(parts) > 1 {
				if milliseconds, err := strconv.ParseInt(parts[1], 10, 64); err == nil && milliseconds >= 0 {
					duration = time.Duration(milliseconds) * time.Millisecond
				}
			}
			if len(parts) > 2 {
				if seconds, err := strconv.Atoi(parts[2]); err == nil && seconds > 0 {
					timeout = time.Duration(seconds) * time.Second
				}
			}
			if current == nil || current.event.Step != step || current.finished {
				// Its start is no longer in the output: what came before
				// this marker is the end of its output.
				current = &cleanupStep{event: ScriptStepEvent{Step: step}, headDropped: true}
				current.output.WriteString(preamble.String())
				preamble.Reset()
				steps[step] = current
			}
			finish(current, exitCode, duration, timeout)
			current = nil
		default:
			write(line)
		}
	}
	if total == 0 {
		return cleanupEventsWithoutMarkers(output, run)
	}
	if current != nil && !current.finished {
		// The hook stopped during this step: its budget ran out, or it failed
		// without reporting.
		ended := run.StartedAt.Add(run.Duration)
		duration := run.Duration
		if current.startedMs > 0 {
			duration = max(0, ended.Sub(time.UnixMilli(current.startedMs)))
		}
		if timeout := stepTimeout(current.event.Step); timeout > 0 {
			current.event.TimeoutMinutes = timeoutMinutes(timeout)
		}
		switch code := run.ExitCode; code {
		case 124, 137:
			current.event.finish(SetupTimedOut, -1, duration)
		case 0:
			current.event.finish(SetupFailed, -1, duration)
		default:
			current.event.finish(SetupFailed, code, duration)
		}
		current.finished = true
		lastFinished = current.event.Step
	}
	if logPath == "" {
		if match := cleanupLogPattern.FindStringSubmatch(trailer.String()); match != nil {
			logPath = strings.TrimSpace(match[1])
		}
	}
	firstSeen := total + 1
	for step := range steps {
		firstSeen = min(firstSeen, step)
	}
	events := make([]ScriptStepEvent, 0, total)
	for number := 1; number <= total; number++ {
		event := ScriptStepEvent{Step: number}
		step, seen := steps[number]
		switch {
		case seen:
			event = step.event
			if step.startedMs > 0 {
				event.StartedAt = scriptTimestamp(time.UnixMilli(step.startedMs))
			}
			event.Output, event.OutputTruncated = redactor.scriptStepOutput(step.output.String(), step.headDropped)
			if event.Status != SetupSucceeded && event.Status != ScriptStepSkipped {
				cleanupFailureContext(&event, event.Output, run.User)
			}
		case number < firstSeen:
			// Before the first step the output still shows; a later step ran,
			// so this one succeeded.
			zero := 0
			event.Status, event.ExitCode, event.OutputTruncated = SetupSucceeded, &zero, true
		default:
			// After the step that stopped the run (or past what the output
			// shows): never ran.
			event.Status = ScriptStepSkipped
		}
		if event.Command == "" && own && len(ownCommands) == total {
			event.Command, event.Repository = scriptStepCommand(ownCommands[number-1], redactor)
		}
		if event.TimeoutMinutes == 0 {
			if timeout := stepTimeout(number); timeout > 0 && len(ownCommands) == total {
				event.TimeoutMinutes = timeoutMinutes(timeout)
			}
		}
		event.Phase, event.RunID, event.Total = ScriptPhaseCleanup, CleanupRunID(nonce), total
		if event.Status != ScriptStepSkipped && (sawLog || logPath != "") {
			event.LogPath = logPath
		}
		events = append(events, fitScriptStepEvent(event))
	}
	return events
}

// cleanupEventsWithoutMarkers reports a run whose output has no markers as
// one step: the run's exit status and output tail.
func cleanupEventsWithoutMarkers(output string, run CleanupRun) []ScriptStepEvent {
	if run.ExitCode == 0 && strings.TrimSpace(output) == "" {
		return nil
	}
	// The run's commands, when the control plane knows them, say which values
	// they assign to secret-named variables: a step that sets one prints it
	// with nothing naming it.
	redactor := run.redactor()
	event := ScriptStepEvent{
		Phase: ScriptPhaseCleanup, RunID: run.RunID, Step: 1, Total: 1,
		StartedAt: scriptTimestamp(run.StartedAt),
	}
	switch {
	case run.ExitCode == 0:
		event.finish(SetupSucceeded, 0, run.Duration)
	case run.ExitCode == 124 && run.Duration >= ProjectCleanupStepTimeout:
		event.finish(SetupTimedOut, -1, run.Duration)
	default:
		event.finish(SetupFailed, run.ExitCode, run.Duration)
	}
	// The hook prints only the tail of a long output (CleanupHookOutputLimit):
	// one that long may start part way through a line, which is dropped, as a
	// secret cut in half there is not recognized. The tail may also have
	// started inside a character, whose bytes CleanupStepEvents dropped
	// (strings.ToValidUTF8), so a tail up to utf8.UTFMax bytes shorter is
	// one too.
	event.Output, event.OutputTruncated = redactor.scriptStepOutput(output, len(output) >= CleanupHookOutputLimit-utf8.UTFMax)
	if match := cleanupLogPattern.FindStringSubmatch(event.Output); match != nil {
		event.LogPath = strings.TrimSpace(match[1])
	}
	if event.Status != SetupSucceeded {
		cleanupFailureContext(&event, event.Output, run.User)
	}
	return []ScriptStepEvent{fitScriptStepEvent(event)}
}

// cleanupFailureContext fills a failed Cleanup step's user, groups and
// directory from the "ran as" line the commands script prints, and its hint.
// Without that line (the step was stopped before it printed one) the user is
// runAs, the account the hook runs as, when known.
func cleanupFailureContext(event *ScriptStepEvent, output, runAs string) {
	if match := cleanupRanAsPattern.FindStringSubmatch(output); match != nil {
		event.User, event.Groups, event.Dir = match[1], strings.Fields(match[2]), strings.TrimSpace(match[3])
	} else {
		event.User = runAs
	}
	exitCode := -1
	if event.ExitCode != nil {
		exitCode = *event.ExitCode
	}
	event.Hint = ScriptFailureHint(output, exitCode, event.User)
}
