package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A project's Setup (postCreate) and Cleanup (preRemove) scripts, the same
// project settings a local project uses (Project settings > Scripts). Setup
// commands run in order in the session's workspace after its checkout and
// before the coding agent starts; Cleanup commands run before the session's
// workspace is permanently deleted.
const (
	// ProjectSetupStepTimeout bounds each Setup command, as the local daemon
	// does.
	ProjectSetupStepTimeout = 30 * time.Minute
	// ProjectCleanupStepTimeout bounds each Cleanup command; Cleanup as a whole
	// also has the sandbox provider's deletion budget.
	ProjectCleanupStepTimeout = 10 * time.Minute
	// ProjectScriptOutputLimit bounds the command output kept for the worker
	// log and the session event, as the local daemon does.
	ProjectScriptOutputLimit = 8 << 10
	// setupDoneFile, in the worker data directory, records that the session's
	// Setup succeeded. Setup runs once per session, like a local workspace's
	// setup runs once when the workspace is created: a worker restart or a
	// workspace stop/start keeps the prepared checkout and does not run it
	// again.
	setupDoneFile = "setup.done"
	// setupAttemptedFile records that this session's Setup started running,
	// so a worker restart after a failed Setup runs it again (and keeps the
	// agent blocked) instead of treating the session as already set up.
	setupAttemptedFile = "setup.attempted"
	// CleanupHookFile and CleanupCommandsFile, in the worker data directory,
	// are what the control plane runs before it deletes a Coder workspace: the
	// hook enters the session's workspace with the session environment and
	// runs the Cleanup commands script beside it. The control plane rewrites
	// that script with the project's current Cleanup before running the hook,
	// as a local project reads its current config at removal time.
	CleanupHookFile     = "cleanup-hook.sh"
	CleanupCommandsFile = "cleanup-commands.sh"
	// cleanupStateDir, in the worker data directory, makes Cleanup run at most
	// once per workspace: the first hook run creates it and records the
	// outcome there, and any later run (another control-plane replica, a
	// re-issued delete) waits for and reports that outcome instead.
	cleanupStateDir = "cleanup-state"
)

// Setup outcomes reported in SetupEvent.Status.
const (
	SetupSucceeded = "succeeded"
	SetupFailed    = "failed"
	SetupTimedOut  = "timed_out"
)

// SetupEventType is the worker event that reports a project's Setup outcome
// on the session's event stream.
const SetupEventType = "workspace.setup"

// SetupEvent is the payload of SetupEventType.
type SetupEvent struct {
	Status string `json:"status"`
	// Step is the 1-based command that failed or timed out; 0 on success.
	Step       int   `json:"step,omitempty"`
	ExitCode   int   `json:"exitCode"`
	DurationMS int64 `json:"durationMs"`
	// Output is the tail of the failed command's output (the last command's on
	// success).
	Output string `json:"output,omitempty"`
}

// Summary names the failed step and why, in the local daemon's words
// ("The project's Setup script failed at step N (exit status X)"), without the
// command's output.
func (e SetupEvent) Summary() string {
	if e.Status == SetupTimedOut {
		return fmt.Sprintf("The project's Setup script stopped at step %d after %d minutes", e.Step, int(ProjectSetupStepTimeout/time.Minute))
	}
	return fmt.Sprintf("The project's Setup script failed at step %d (exit status %d)", e.Step, e.ExitCode)
}

// Message is the startup error shown when Setup did not succeed: the summary
// followed by the command's output.
func (e SetupEvent) Message() string {
	message := e.Summary()
	if output := strings.TrimSpace(e.Output); output != "" {
		message += ": " + output
	}
	return message
}

// WorkspaceStartupEventType is the worker event that reports whether the
// session's workspace (checkout, working directory and Setup) is ready for
// its agent. A failure becomes the session's startup error, shown on the
// desktop's startup screen; a later ready report clears it.
const WorkspaceStartupEventType = "workspace.startup"

const (
	WorkspaceStartupFailed = "failed"
	WorkspaceStartupReady  = "ready"
)

// WorkspaceStartupEvent is the payload of WorkspaceStartupEventType. Code is
// one of domain.WorkspaceStartupErrorCodes on a failure. Detail is a bounded,
// redacted excerpt (the failed Setup command's output).
type WorkspaceStartupEvent struct {
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// SessionScriptEnvironment is the AO_* environment a Setup or Cleanup command
// sees.
type SessionScriptEnvironment struct {
	SessionID     string
	SessionKind   string
	Branch        string
	WorkspaceDir  string
	RepositoryURL string
	ProjectID     string
	ProjectName   string
}

// Map returns the environment as AO_* variables. AO_WORKTREE_PATH and
// AO_SOURCE_TREE_PATH are the variables a local project's scripts use. A
// cloud session has no separate source tree (its checkout is the only copy of
// the repository in the sandbox), so both name the session's workspace.
func (e SessionScriptEnvironment) Map() map[string]string {
	return map[string]string{
		"AO_SESSION_ID":       e.SessionID,
		"AO_SESSION_KIND":     e.SessionKind,
		"AO_SESSION_BRANCH":   e.Branch,
		"AO_WORKSPACE_DIR":    e.WorkspaceDir,
		"AO_WORKTREE_PATH":    e.WorkspaceDir,
		"AO_SOURCE_TREE_PATH": e.WorkspaceDir,
		"AO_REPOSITORY_URL":   e.RepositoryURL,
		"AO_PROJECT_ID":       e.ProjectID,
		"AO_PROJECT_NAME":     e.ProjectName,
	}
}

// SetupRunner runs a project's Setup commands once per session.
type SetupRunner struct {
	DataDir string
	// FirstStart is true on the session's first worker start (no worker
	// token persisted yet). Setup runs when the session's workspace is first
	// prepared, like a local workspace's: a session that started without Setup
	// (or under a worker from before Setup existed) never runs one added to
	// the project later inside its already-prepared workspace.
	FirstStart bool
	// StepTimeout overrides ProjectSetupStepTimeout for tests.
	StepTimeout time.Duration
}

// Run runs commands in order in dir with `sh -c`, stopping at the first one
// that fails. ran is false when there is nothing to run or Setup already
// succeeded for this session. Only a success is recorded, so a failed Setup
// runs again when the session is reopened.
func (r SetupRunner) Run(
	ctx context.Context,
	commands []string,
	dir string,
	environment SessionScriptEnvironment,
) (event SetupEvent, ran bool) {
	commands = nonEmptyCommands(commands)
	marker := filepath.Join(r.DataDir, setupDoneFile)
	if _, err := os.Stat(marker); err == nil {
		return SetupEvent{}, false
	}
	attempted := filepath.Join(r.DataDir, setupAttemptedFile)
	_, attemptErr := os.Stat(attempted)
	if len(commands) == 0 || (!r.FirstStart && attemptErr != nil) {
		// Nothing to run, or a restarted worker whose session never started a
		// Setup: the workspace is already prepared, so record it as set up.
		r.writeMarker(marker)
		return SetupEvent{}, false
	}
	r.writeMarker(attempted)
	timeout := r.StepTimeout
	if timeout <= 0 {
		timeout = ProjectSetupStepTimeout
	}
	started := time.Now()
	event = SetupEvent{Status: SetupSucceeded}
	for index, command := range commands {
		step := runProjectScriptStep(ctx, command, timeout, dir, environment.Map())
		// Only a failure carries output: it is what the startup screen shows to
		// fix the step. A successful Setup's output (often secrets a step
		// loaded or printed) is never published to the session's events.
		if step.Status != SetupSucceeded {
			step.Step = index + 1
			step.DurationMS = time.Since(started).Milliseconds()
			return step, true
		}
	}
	event.DurationMS = time.Since(started).Milliseconds()
	r.writeMarker(marker)
	return event, true
}

func (r SetupRunner) writeMarker(path string) {
	if err := os.MkdirAll(r.DataDir, 0o700); err == nil {
		_ = os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	}
}

func nonEmptyCommands(commands []string) []string {
	kept := make([]string, 0, len(commands))
	for _, command := range commands {
		if strings.TrimSpace(command) != "" {
			kept = append(kept, command)
		}
	}
	return kept
}

// runProjectScriptStep runs one command as `sh -c command` in its own process
// group, so a timeout also stops whatever the command started.
func runProjectScriptStep(
	ctx context.Context,
	command string,
	timeout time.Duration,
	dir string,
	environment map[string]string,
) SetupEvent {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	process := exec.CommandContext(runCtx, "sh", "-c", command)
	process.Dir = dir
	process.Env = mergeEnvironment(os.Environ(), environment)
	output := &tailBuffer{limit: ProjectScriptOutputLimit}
	process.Stdout = output
	process.Stderr = output
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process.Cancel = func() error {
		if process.Process == nil {
			return nil
		}
		return syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
	}
	// A successful shell can leave a detached child holding its output pipes;
	// WaitDelay closes them without failing the step (as the local daemon does).
	process.WaitDelay = time.Second
	err := process.Run()
	if errors.Is(err, exec.ErrWaitDelay) && runCtx.Err() == nil {
		err = nil
	}
	event := SetupEvent{Status: SetupSucceeded, Output: strings.TrimSpace(output.String())}
	switch {
	case err == nil:
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		event.Status, event.ExitCode = SetupTimedOut, -1
	default:
		event.Status, event.ExitCode = SetupFailed, -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			event.ExitCode = exitErr.ExitCode()
		} else if event.Output == "" {
			event.Output = err.Error()
		}
	}
	return event
}

// mergeEnvironment overlays overrides on a KEY=VALUE environment.
func mergeEnvironment(base []string, overrides map[string]string) []string {
	merged := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			merged = append(merged, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		merged = append(merged, key+"="+overrides[key])
	}
	return merged
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	limit int
	data  bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.data.Write(p)
	if over := b.data.Len() - b.limit; over > 0 {
		b.data.Next(over)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return b.data.String() }

// CleanupCommandsScript is the shell script the Cleanup hook runs: each
// command with `sh -c` in order, each bounded by ProjectCleanupStepTimeout when
// timeout(1) is available, stopping at the first failure with that command's
// status. With no commands it does nothing.
func CleanupCommandsScript(commands []string) string {
	seconds := strconv.Itoa(int(ProjectCleanupStepTimeout / time.Second))
	var steps strings.Builder
	steps.WriteString("#!/bin/sh\n# Written by AO: the project's Cleanup commands.\n")
	steps.WriteString("ao_timeout=\nif command -v timeout >/dev/null 2>&1; then ao_timeout=\"timeout -k 5 " + seconds + "\"; fi\n")
	for index, command := range nonEmptyCommands(commands) {
		step := strconv.Itoa(index + 1)
		steps.WriteString("$ao_timeout sh -c " + shellQuote(command) + " </dev/null\n")
		steps.WriteString("ao_code=$?\nif [ \"$ao_code\" -ne 0 ]; then echo \"cleanup step " + step +
			" failed: exit status $ao_code\" >&2; exit \"$ao_code\"; fi\n")
	}
	return steps.String()
}

// WriteCleanupHook installs the hook the control plane runs before it deletes
// the session's workspace. The hook enters dir with the session environment
// and runs the Cleanup commands script (CleanupCommandsScript). It is
// installed even when the project has no Cleanup yet, so Cleanup added to the
// project later still runs in the session's workspace with its environment.
func WriteCleanupHook(dataDir string, commands []string, dir string, environment SessionScriptEnvironment) error {
	hook := filepath.Join(dataDir, CleanupHookFile)
	commandsPath := filepath.Join(dataDir, CleanupCommandsFile)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(commandsPath, []byte(CleanupCommandsScript(commands)), 0o600); err != nil {
		return err
	}
	state := filepath.Join(dataDir, cleanupStateDir)
	var content strings.Builder
	content.WriteString("#!/bin/sh\n# Written by the AO worker: runs the project's Cleanup commands once.\n")
	content.WriteString("ao_state=" + shellQuote(state) + "\n")
	content.WriteString("if ! mkdir \"$ao_state\" 2>/dev/null; then\n" +
		"  while [ ! -f \"$ao_state/status\" ]; do sleep 2; done\n" +
		"  cat \"$ao_state/output\" 2>/dev/null\n" +
		"  exit \"$(cat \"$ao_state/status\")\"\n" +
		"fi\n")
	content.WriteString("cd " + shellQuote(dir) + " >\"$ao_state/output\" 2>&1 || { echo 1 >\"$ao_state/status\"; cat \"$ao_state/output\"; exit 1; }\n")
	values := environment.Map()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		content.WriteString("export " + key + "=" + shellQuote(values[key]) + "\n")
	}
	content.WriteString("sh " + shellQuote(commandsPath) + " >>\"$ao_state/output\" 2>&1\n" +
		"ao_code=$?\n" +
		"tail -c " + strconv.Itoa(ProjectScriptOutputLimit) + " \"$ao_state/output\"\n" +
		"echo \"$ao_code\" >\"$ao_state/status\"\n" +
		"exit \"$ao_code\"\n")
	return writeFileAtomic(hook, []byte(content.String()), 0o700)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	temporary := path + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
