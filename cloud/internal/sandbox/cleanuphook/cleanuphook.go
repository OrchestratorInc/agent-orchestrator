// Package cleanuphook runs a session's project Cleanup before a provider
// deletes the session's sandbox, and reports it. The worker installs the
// Cleanup hook in its data directory (worker.WriteCleanupHook) when the
// session starts; the worker is gone by the time the sandbox is deleted, so
// the control plane runs that hook remotely, as its owner, with a time budget,
// and reports the outcome as the project_cleanup stage and each step as a
// workspace.script.step event (phase cleanup) on the session.
//
// Every provider shares the hook command, the run bookkeeping and the
// reporting here; each brings only how it runs a shell command in its sandbox
// (Coder's agent terminal, a Freestyle exec, a Docker exec).
package cleanuphook

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/google/uuid"
)

const (
	// Stage is the project's Cleanup commands, run before AO deletes the
	// session's sandbox.
	Stage = "project_cleanup"
	// Marker precedes the hook command's last line, which carries the hook's
	// exit status or Skipped.
	Marker = "__AO_PROJECT_CLEANUP__"
	// Skipped is what the hook command reports, followed by a reason
	// (SkipReason), when the worker never installed a hook (the worker never
	// started), or the hook may not run.
	Skipped = "skipped"
	// Budget is the least a whole Cleanup run is given; a run gets the sum
	// of its steps' time limits (Plan.Budget, sandbox.CleanupBudget), at
	// most sandbox.MaxCleanupBudget, and the reconciler's deletion deadline
	// grows with it. Each command also has its own time limit inside the
	// hook.
	Budget = sandbox.MinCleanupBudget
	// Margin is extra time AO waits beyond Budget for the command to start
	// and the result to arrive.
	Margin = 20 * time.Second
	// RecordTTL is how long a run is remembered after it ends, so a Delete
	// the reconciler re-issues while the sandbox winds down never runs the
	// commands a second time. A run may end hours after it started (it waits
	// up to its queue wait and its organization's hold, then takes its
	// budget), so it is at least the longest of those, the largest budget,
	// Margin and StaleAfter, and an hour more. The runs live only in this
	// process's memory: a control-plane restart forgets them.
	RecordTTL = time.Hour + sandbox.MinCleanupQueueWait + (sandbox.MaxCleanupBudget - sandbox.MinCleanupBudget) +
		2*sandbox.MaxCleanupBudget + Margin + StaleAfter
	// InlineWait is how long one Delete waits for a Cleanup run before
	// handing the sandbox back to the reconciler. Most Cleanup commands
	// finish within it, so the sandbox is deleted in the same pass and a
	// later tick is only needed for slow ones.
	InlineWait = 5 * time.Second
	// StaleAfter is added to a run's limit before a run that never reported
	// is treated as finished, so a lost or wedged run can never hold a
	// deletion indefinitely.
	StaleAfter = 30 * time.Second
	// PayloadLimit bounds the encoded Cleanup commands passed in the hook
	// command; larger Cleanup falls back to the commands the worker
	// installed. Coder's terminal carries the command in its request URL.
	PayloadLimit = 12 << 10
	// OutputLimit bounds the Cleanup output the hook command prints and AO
	// keeps: the hook's own tail (worker.CleanupHookOutputLimit) and its last
	// lines, split into the run's step events.
	OutputLimit = worker.CleanupHookOutputLimit + 1<<10
	// stageOutputLimit bounds the output excerpt on a failed run's stage.
	stageOutputLimit = 2048
)

// Why the hook command skipped the hook: the word after Skipped in its
// status (SkipReason).
const (
	// skipNoHook: there is no hook, so the worker never started there.
	skipNoHook = "no_hook"
	// skipNotRunnable: the hook is there but its owner cannot run it.
	skipNotRunnable = "not_runnable"
	// skipOwner: the hook belongs to root, the login user or no account, so
	// it is not the worker's and is never run.
	skipOwner = "hook_owner"
	// skipNoSudo: the login user does not own the hook and cannot use sudo
	// without a password to run it as its owner.
	skipNoSudo = "no_sudo"
	// skipNoAccount: the account the worker runs as does not exist.
	skipNoAccount = "no_account"
	// skipLink: the hook is a symbolic link, which never runs.
	skipLink = "hook_link"
	// skipSudoRootOnly: the login user may use sudo only as root, and
	// runuser is not there to reach the worker's account from root.
	skipSudoRootOnly = "sudo_root_only"
	// skipNoWorkingFolder: the session never got its working folder (it was
	// in use, for example by the Coder template), so its worker installed a
	// hook that runs no Cleanup (worker.WriteSkippedCleanupHook): the folder
	// is not the session's to clean up.
	skipNoWorkingFolder = worker.CleanupSkipNoWorkingFolder
)

// accountPattern is an account name safe to spell as ~NAME in the hook command.
var accountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// SkipReason reads a hook command's status: whether it skipped the hook, and
// the reason it gave ("" from a command that gave none).
func SkipReason(status string) (reason string, skipped bool) {
	fields := strings.Fields(status)
	if len(fields) == 0 || fields[0] != Skipped {
		return "", false
	}
	if len(fields) > 1 {
		reason = fields[1]
	}
	return reason, true
}

// SkippedReason is what each step of a Cleanup the hook command skipped says,
// for the reason it gave.
func SkippedReason(reason string) string {
	switch reason {
	case skipNoHook:
		return "AO could not run this Cleanup step: the sandbox has no Cleanup hook from the AO worker (the worker may never have started there), so it was deleted without Cleanup."
	case skipNotRunnable:
		return "AO could not run this Cleanup step: the AO worker's user cannot run the Cleanup hook in the sandbox, so it was deleted without Cleanup."
	case skipOwner:
		return "AO could not run this Cleanup step: the Cleanup hook in the sandbox does not belong to the AO worker's user, so AO did not run it before deleting the sandbox."
	case skipNoSudo:
		return "AO could not run this Cleanup step: the workspace's login user cannot use sudo without a password, which AO needs to run Cleanup as the AO worker's user."
	case skipNoAccount:
		return "AO could not run this Cleanup step: the AO worker's user does not exist in the sandbox, so it was deleted without Cleanup."
	case skipLink:
		return "AO did not run this Cleanup step: the Cleanup hook in the sandbox is a symbolic link, which AO never runs."
	case skipSudoRootOnly:
		return "AO could not run this Cleanup step: the workspace's login user may use sudo only as root, and AO runs Cleanup as the AO worker's user, never as root."
	case skipNoWorkingFolder:
		return "Cleanup did not run: this session never got its working folder."
	default:
		return "AO could not run this Cleanup step in the sandbox before deleting it."
	}
}

// NotRunningReason is what each step of a Cleanup says when the sandbox was
// not running when AO deleted it.
const NotRunningReason = "The sandbox was not running when AO deleted it, so this Cleanup step did not run."

// UnreachableReason is what each step of a Cleanup says when the sandbox was
// running but AO could not reach it to run Cleanup.
const UnreachableReason = "AO could not reach the sandbox when it deleted it, so this Cleanup step did not run."

// WorkspaceHook is the Cleanup hook's path in the /workspace layout the
// reconciler gives every provider but Coder (AO_DATA_DIR
// /workspace/.ao/worker).
const WorkspaceHook = "/workspace/.ao/worker/" + worker.CleanupHookFile

// Runner is how the hook command reaches the hook's owner, the user the
// worker ran as.
type Runner int

const (
	// AsLoginUser runs the hook command as an ordinary login user. With an
	// Owner other than the login user the hook runs only as that account,
	// through sudo, never as root or as the login user (Coder's agent
	// terminal with a dedicated worker user); without one only a hook the
	// login user owns runs, directly (a Docker exec as the container's user,
	// a Coder workspace-user worker).
	AsLoginUser Runner = iota
	// AsRoot runs the hook command as root, which runs the hook as its owner
	// through runuser and never as root (a Freestyle exec).
	AsRoot
)

// Stop says whether the hook command first stops the hook owner's processes.
type Stop int

const (
	// KeepOwnerProcesses runs the hook beside whatever the owner runs
	// (Coder: the login user may also run the Coder agent that carries the
	// hook command, so nothing is stopped there).
	KeepOwnerProcesses Stop = iota
	// StopOwnerProcesses ends every process of the hook's owner (the AO
	// worker, its agent and whatever they started) before the hook runs as
	// a fresh process, so neither acts during Cleanup: a Freestyle VM
	// resumed for its Cleanup resumes them too, and a Docker container runs
	// them until it is removed. Each gets SIGTERM, up to OwnerStopWait to
	// exit, then SIGKILL. PID 1 is spared: in a Docker container it is the
	// worker itself, and ending it would stop the container and the Cleanup
	// with it (with its agent gone and its session deleted it has nothing to
	// act on); in a VM it is root's init. Containers the Cleanup stops are
	// root's (dockerd), so they are untouched; a dev server the agent started
	// is stopped, which is what a Cleanup that stops it wants.
	StopOwnerProcesses
)

// OwnerStopWait is how long the owner's processes have to exit after SIGTERM.
const OwnerStopWait = 5 * time.Second

// stopOwnerScript defines ao_stop USER SIGNAL, which signals every process of
// USER but PID 1 and this shell and reports whether any was signalled. It
// reads /proc rather than using pkill, which slim images lack; signalling 0
// only checks a process is still there.
const stopOwnerScript = "ao_stop() {\n" +
	"  ao_uid=$(id -u \"$1\" 2>/dev/null) || return 1\n" +
	"  ao_found=1\n" +
	"  for ao_pid in /proc/[0-9]*; do\n" +
	"    ao_pid=${ao_pid#/proc/}\n" +
	"    case \"$ao_pid\" in 1|\"$$\") continue ;; esac\n" +
	"    [ \"$(stat -c %u \"/proc/$ao_pid\" 2>/dev/null)\" = \"$ao_uid\" ] || continue\n" +
	"    kill -\"$2\" \"$ao_pid\" 2>/dev/null && ao_found=0\n" +
	"  done\n" +
	"  return $ao_found\n" +
	"}\n"

// Options says how Command reaches the hook's owner.
type Options struct {
	Runner Runner
	Stop   Stop
	// Owner is the account the hook runs as: the user the worker was
	// provisioned to run as. With AsRoot and no Owner the hook's file owner
	// is used (never root); with AsLoginUser and no Owner the hook must be
	// the login user's own.
	Owner string
}

// Command is the shell command that runs the worker-installed Cleanup hook
// (hook is a shell word naming it) as the user that owns it, under timeout(1)
// when the image has it, then prints the tail of its output and, after
// Marker, its exit status (or Skipped). With override (a CommandsPayload), it
// first replaces the commands script beside the hook with the project's
// current Cleanup, as that user, so the hook keeps its working directory,
// session environment and run-once lock; if that fails the installed
// commands run. With StopOwnerProcesses it ends the owner's processes first.
func Command(hook string, budget time.Duration, override string, options Options) string {
	seconds := strconv.Itoa(max(1, int(budget/time.Second)))
	command := "ao_hook=" + hook + "\n" +
		// ao_skip REASON reports the hook as not run, saying why (SkipReason).
		"ao_skip() { printf '\\n%s:%s %s\\n' " + Marker + " " + Skipped + " \"$1\"; exit 0; }\n" +
		"ao_owner=\n"
	// asOwner prefixes a command run as the hook's owner (ao_owner set).
	asOwner := "ao_as"
	// Whatever the runner, a hook that is a symbolic link never runs: it
	// could name another account's program.
	refuseLink := "if [ -L \"$ao_hook\" ]; then ao_skip " + skipLink + "; fi\n"
	switch {
	case options.Runner == AsRoot && options.Owner != "":
		// The hook runs as the account the worker was provisioned with,
		// whoever owns the file: an agent that swaps the hook for a link to
		// another account's program cannot borrow that account.
		asOwner = `runuser -u "$ao_owner" --`
		command += refuseLink +
			"if ! [ -e \"$ao_hook\" ]; then ao_skip " + skipNoHook + "\n" +
			"elif ! [ -f \"$ao_hook\" ] || ! [ -x \"$ao_hook\" ]; then ao_skip " + skipNotRunnable + "\n" +
			"elif ! id -u " + shellQuote(options.Owner) + " >/dev/null 2>&1; then ao_skip " + skipNoAccount + "\n" +
			"fi\n" +
			"ao_owner=" + shellQuote(options.Owner) + "\n" +
			"case \"$ao_owner\" in root|\"$(id -un)\") ao_skip " + skipOwner + " ;; esac\n" +
			"set -- " + asOwner + " \"$ao_hook\"\n"
	case options.Runner == AsRoot:
		asOwner = `runuser -u "$ao_owner" --`
		command += refuseLink +
			"if ! [ -e \"$ao_hook\" ]; then ao_skip " + skipNoHook + "\n" +
			"elif ! [ -f \"$ao_hook\" ] || ! [ -x \"$ao_hook\" ]; then ao_skip " + skipNotRunnable + "\n" +
			"fi\n" +
			"ao_owner=$(stat -c %U \"$ao_hook\" 2>/dev/null) || ao_skip " + skipNoHook + "\n" +
			"case \"$ao_owner\" in ''|root|UNKNOWN|\"$(id -un)\") ao_skip " + skipOwner + " ;; esac\n" +
			"set -- " + asOwner + " \"$ao_hook\"\n"
	case options.Owner != "":
		// A login user that is not the worker's account (Coder's agent
		// terminal, the worker running as ao-worker) runs the hook only as
		// that account, through sudo, never as itself or as root: the agent
		// controls the hook, and the login user can usually use sudo. So a
		// hook the agent points anywhere runs with the agent's own rights,
		// and no check here can be raced into more. Whether the hook can run
		// is asked as the owner too: Ubuntu 25.10 and later ship uutils
		// coreutils, whose test -x run as root is false for a file only its
		// owner may run (the worker's hook is mode 0700). A login user that
		// may use sudo only as root reaches the owner through runuser.
		home := ""
		if accountPattern.MatchString(options.Owner) {
			home = "~" + options.Owner
		}
		command += refuseLink +
			"ao_owner=" + shellQuote(options.Owner) + "\n" +
			"ao_home=" + home + "\n" +
			"ao_via=\n" +
			"ao_as() {\n" +
			"  case \"$ao_via\" in\n" +
			"    sudo) sudo -n -H -u \"$ao_owner\" \"$@\" ;;\n" +
			"    runuser) sudo -n runuser -u \"$ao_owner\" -- env HOME=\"$ao_home\" \"$@\" ;;\n" +
			"    *) \"$@\" ;;\n" +
			"  esac\n" +
			"}\n" +
			"if [ \"$(id -un)\" = \"$ao_owner\" ]; then\n" +
			"  ao_owner=\n" +
			"elif ! id -u \"$ao_owner\" >/dev/null 2>&1; then ao_skip " + skipNoAccount + "\n" +
			"elif ! sudo -n true 2>/dev/null; then\n" +
			// Without sudo the hook can only be looked for from outside.
			"  if [ -e \"$ao_hook\" ] || ! [ -x \"$(dirname \"$ao_hook\")\" ]; then ao_skip " + skipNoSudo + "; fi\n" +
			"  ao_skip " + skipNoHook + "\n" +
			"elif sudo -n -u \"$ao_owner\" true 2>/dev/null; then ao_via=sudo\n" +
			"elif command -v runuser >/dev/null 2>&1 && sudo -n runuser -u \"$ao_owner\" -- true 2>/dev/null; then ao_via=runuser\n" +
			"else ao_skip " + skipSudoRootOnly + "; fi\n" +
			"ao_state=$(ao_as sh -c 'if [ -L \"$1\" ]; then echo " + skipLink + "; elif ! [ -e \"$1\" ]; then echo " + skipNoHook +
			"; elif [ -f \"$1\" ] && [ -x \"$1\" ]; then echo ok; else echo " + skipNotRunnable + "; fi' _ \"$ao_hook\" 2>/dev/null)\n" +
			"case \"$ao_state\" in ok) ;; " + skipLink + "|" + skipNoHook + ") ao_skip \"$ao_state\" ;; *) ao_skip " + skipNotRunnable + " ;; esac\n" +
			"case \"$ao_via\" in\n" +
			"  sudo) set -- sudo -n -H -u \"$ao_owner\" \"$ao_hook\" ;;\n" +
			"  runuser) set -- sudo -n runuser -u \"$ao_owner\" -- env HOME=\"$ao_home\" \"$ao_hook\" ;;\n" +
			"  *) set -- \"$ao_hook\" ;;\n" +
			"esac\n"
	default:
		// The hook is the login user's own (the worker ran as it: a Docker
		// container's user, a Coder workspace-user worker); any other hook is
		// not the worker's and never runs. The agent runs as that same user,
		// so running its hook gives it nothing it does not have.
		command += refuseLink +
			"if ! [ -e \"$ao_hook\" ]; then ao_skip " + skipNoHook + "\n" +
			"elif ! [ -O \"$ao_hook\" ]; then ao_skip " + skipOwner + "\n" +
			"elif ! [ -f \"$ao_hook\" ] || ! [ -x \"$ao_hook\" ]; then ao_skip " + skipNotRunnable + "\n" +
			"fi\n" +
			"set -- \"$ao_hook\"\n"
	}
	if override != "" {
		write := shellQuote(`cat >"$1.tmp" && chmod 600 "$1.tmp" && mv "$1.tmp" "$1"`)
		command += "ao_commands=$(dirname \"$ao_hook\")/" + worker.CleanupCommandsFile + "\n" +
			"ao_payload=" + shellQuote(override) + "\n" +
			"if [ -n \"$ao_owner\" ]; then\n" +
			"  printf %s \"$ao_payload\" | base64 -d | gzip -dc | " + asOwner + " sh -c " + write + " _ \"$ao_commands\"\n" +
			"else\n" +
			"  printf %s \"$ao_payload\" | base64 -d | gzip -dc | sh -c " + write + " _ \"$ao_commands\"\n" +
			"fi || echo \"AO: could not install the project's current Cleanup; running the commands from the session's start\" >&2\n"
	}
	if options.Stop == StopOwnerProcesses {
		polls := strconv.Itoa(int(OwnerStopWait / (500 * time.Millisecond)))
		command += stopOwnerScript +
			// A hook the login user owns runs as that user (ao_owner unset).
			"ao_who=${ao_owner:-$(id -un)}\n" +
			"if ao_stop \"$ao_who\" TERM; then\n" +
			"  ao_wait=0\n" +
			"  while [ \"$ao_wait\" -lt " + polls + " ] && ao_stop \"$ao_who\" 0; do sleep 0.5; ao_wait=$((ao_wait + 1)); done\n" +
			"  ao_stop \"$ao_who\" KILL\n" +
			"fi\n"
	}
	return command +
		"if command -v timeout >/dev/null 2>&1; then\n" +
		"  ao_out=$(timeout -k 5 " + seconds + " \"$@\" 2>&1 </dev/null)\n" +
		"else\n" +
		"  ao_out=$(\"$@\" 2>&1 </dev/null)\n" +
		"fi\n" +
		"ao_code=$?\n" +
		// A hook that runs no Cleanup prints only why (worker.WriteSkippedCleanupHook),
		// as its last line: sudo may print to stderr first, for example
		// "sudo: unable to resolve host".
		"case \"$(printf '%s\\n' \"$ao_out\" | tail -n 1)\" in " + shellQuote(worker.CleanupHookSkippedPrefix+skipNoWorkingFolder) + ") ao_skip " + skipNoWorkingFolder + " ;; esac\n" +
		"printf '%s\\n' \"$ao_out\" | tail -c " + strconv.Itoa(OutputLimit) + "\n" +
		"printf '\\n%s:%s\\n' " + Marker + " \"$ao_code\"\n"
}

// HookWord is the shell word for a hook path, quoted.
func HookWord(hook string) string {
	return shellQuote(path.Clean(hook))
}

// CommandsPayload is the gzip-compressed, base64-encoded Cleanup commands
// script for the project's current Cleanup, its step markers carrying nonce,
// and whether it fits PayloadLimit.
func CommandsPayload(commands []string, nonce string) (string, bool) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(worker.CleanupCommandsScript(commands, nonce))); err != nil {
		return "", false
	}
	if err := writer.Close(); err != nil {
		return "", false
	}
	payload := base64.StdEncoding.EncodeToString(compressed.Bytes())
	return payload, len(payload) <= PayloadLimit
}

// Plan is one Cleanup run's hook command and the run its steps are read as.
type Plan struct {
	// Override is the project's current Cleanup for Command, or "" to run
	// the commands the worker installed.
	Override string
	// Steps names the run and, with an override, its marker nonce and
	// commands.
	Steps worker.CleanupRun
	// Budget bounds the whole run: the sum of the time limits of the steps
	// it may run (the current Cleanup's, and the worker's installed ones the
	// hook falls back to), at least Budget and at most
	// sandbox.MaxCleanupBudget (sandbox.CleanupBudget).
	Budget time.Duration
}

// NewPlan plans a Cleanup run from the project's current Cleanup when the
// caller read it (sandbox.WithCleanupCommands). It reports false when nothing
// is to run: the project has no Cleanup now. A current Cleanup too large to
// pass is logged and the commands the worker installed run instead.
func NewPlan(ctx context.Context, logArgs ...any) (Plan, bool) {
	current, known := sandbox.CleanupCommands(ctx)
	if known && len(current) == 0 {
		return Plan{}, false
	}
	// The current Cleanup's commands script marks its steps with a nonce
	// made for this run, so its step events are read only from those markers.
	plan := Plan{Steps: worker.CleanupRun{RunID: uuid.NewString()}, Budget: Budget}
	// What the hook runs if the current Cleanup is not installed: the
	// commands the worker was last given, else (a sandbox from before they
	// were stored) the current ones, the best guess left.
	installed, installedKnown := sandbox.InstalledCleanupCommands(ctx)
	if installedKnown {
		plan.Steps.KnownCommands = installed
	} else if known {
		plan.Steps.KnownCommands = current
	}
	// Either list may be what runs: the current one, or the installed one
	// when the current one cannot be put in place.
	plan.Budget = sandbox.CleanupBudget(current, installed)
	if known {
		nonce := worker.NewScriptMarkerNonce()
		if payload, fits := CommandsPayload(current, nonce); fits {
			plan.Override, plan.Steps.Nonce, plan.Steps.Commands = payload, nonce, current
			plan.Steps.RunID = worker.CleanupRunID(nonce)
		} else {
			slog.WarnContext(ctx, "the project's current Cleanup is too large to pass to the sandbox; running the commands from the session's start",
				append(logArgs, "limit_bytes", PayloadLimit)...)
		}
	}
	return plan, true
}

// Split splits a hook command's output into what the hook printed and the
// status after Marker; ok is false when the marker never came.
func Split(output string) (printed, status string, ok bool) {
	index := strings.LastIndex(output, Marker+":")
	if index < 0 {
		return output, "", false
	}
	status, _, _ = strings.Cut(output[index+len(Marker)+1:], "\n")
	return output[:index], strings.TrimSpace(strings.TrimRight(status, "\r")), true
}

// ExitCode is a reported status as an exit code; -1 when it is not a number.
func ExitCode(status string) int {
	code, err := strconv.Atoi(strings.TrimSpace(status))
	if err != nil {
		return -1
	}
	return code
}

// Report reports a finished hook command. printed is what the hook printed
// before the status (at least its last OutputLimit bytes), status the status
// after Marker, and err why no status arrived. It appends the run's step
// events to the session (sandbox.RecordSessionEvent) and finishes the stage
// with attrs: a failed run with its output excerpt and hint, a run the hook
// command skipped as skipped (each step skipped with the reason it gave), and
// a run that never reported as an error. A run that
// never reported still appends its steps: those its partial output shows, or
// else each current step as skipped with the provider's error
// (ExecFailedReason), so the session shows why Cleanup did not run. Nothing
// it reports fails the deletion. name prefixes the stage's error.
func Report(
	ctx context.Context,
	finish func(error, map[string]string),
	attrs map[string]string,
	steps worker.CleanupRun,
	printed, status string,
	err error,
	name string,
) {
	exitCode := ExitCode(status)
	skipReason, skipped := SkipReason(status)
	switch {
	case err == nil && skipped:
		// The hook did not run: each step says why, so the session shows its
		// Cleanup did not run.
		recordSkippedSteps(ctx, steps, SkippedReason(skipReason))
	case err == nil:
		steps.ExitCode, steps.Duration = exitCode, time.Since(steps.StartedAt)
		for _, event := range worker.CleanupStepEvents(printed, steps) {
			sandbox.RecordSessionEvent(ctx, worker.ScriptStepEventType, event)
		}
	case err != nil && strings.Contains(printed, "@@ao-step-"):
		// The hook's output came back in part: its steps up to there.
		steps.ExitCode, steps.Duration = -1, time.Since(steps.StartedAt)
		for _, event := range worker.CleanupStepEvents(printed, steps) {
			sandbox.RecordSessionEvent(ctx, worker.ScriptStepEventType, event)
		}
	case err != nil:
		// The provider could not run the hook: each step says why, so the
		// session shows its Cleanup did not run.
		recordSkippedSteps(ctx, steps, ExecFailedReason(err))
	}
	switch {
	case err != nil:
		finish(fmt.Errorf("%s: project cleanup did not report: %w", name, err), attrs)
	case skipped:
		attrs["result"] = Skipped
		if skipReason != "" {
			attrs["skip_reason"] = skipReason
		}
		finish(nil, attrs)
	case exitCode != 0:
		// The tail carries the failed step, its command, the user and groups
		// it ran as, and where its full output is (worker.CleanupCommandsScript).
		output := boundedOutput(printed, steps)
		attrs["output"] = activity.Excerpt(output, stageOutputLimit)
		if hint := worker.ScriptFailureHint(output, exitCode, ""); hint != "" {
			attrs["hint"] = hint
		}
		finish(fmt.Errorf("%s: project cleanup exited %d", name, exitCode), attrs)
	default:
		finish(nil, attrs)
	}
}

// ReportNotRun reports a Cleanup that could not run at all: the stage as
// skipped, failed with err (nil when nothing went wrong), and each of its
// steps, when known, as skipped on the session with reason (said to the user)
// as its hint. The deletion goes ahead.
func ReportNotRun(
	ctx context.Context,
	finish func(error, map[string]string),
	attrs map[string]string,
	steps worker.CleanupRun,
	reason string,
	err error,
) {
	recordSkippedSteps(ctx, steps, reason)
	attrs["result"] = Skipped
	finish(err, attrs)
}

// ExecFailedReason is what each step of a Cleanup the provider could not run
// says: the provider's error, redacted and short.
func ExecFailedReason(err error) string {
	detail := strings.Join(strings.Fields(worker.RedactScriptOutput(err.Error())), " ")
	return "AO could not run this Cleanup step in the sandbox before deleting it: " + activity.Truncate(detail, 300)
}

// recordSkippedSteps appends each of the run's Cleanup steps as skipped, with
// reason as its hint: the project's current Cleanup when the run carries it,
// else the commands the hook would have run; nothing when neither is known.
func recordSkippedSteps(ctx context.Context, steps worker.CleanupRun, reason string) {
	commands := steps.Commands
	if len(commands) == 0 {
		commands = steps.KnownCommands
	}
	total := len(commands)
	for index, stored := range commands {
		plain, timeout := domain.SplitScriptStepTimeout(stored, domain.DefaultCleanupStepTimeoutMinutes)
		repository, command, _ := domain.ParseRepoScriptCommand(plain)
		sandbox.RecordSessionEvent(ctx, worker.ScriptStepEventType, worker.SanitizeScriptStepEvent(worker.ScriptStepEvent{
			Phase: worker.ScriptPhaseCleanup, RunID: steps.RunID, Step: index + 1, Total: total,
			Command: command, Repository: repository, Status: worker.ScriptStepSkipped, Hint: reason,
			TimeoutMinutes: int(timeout / time.Minute),
		}))
	}
}

// boundedOutput is the hook's output for the stage: redacted as its step
// events are (the run's redaction, which also knows the values its commands
// assign to secret-named variables), without carriage returns or control
// characters, at most stageOutputLimit bytes.
func boundedOutput(output string, steps worker.CleanupRun) string {
	output = steps.RedactOutput(strings.ReplaceAll(output, "\r", ""))
	output = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || character >= ' ' {
			return character
		}
		return -1
	}, output)
	output = strings.TrimSpace(output)
	if len(output) > stageOutputLimit {
		output = output[len(output)-stageOutputLimit:]
	}
	return output
}

// Run is one sandbox's Cleanup run. It never fails a Delete.
type Run struct {
	done    chan struct{}
	started time.Time
	// ended is when the run ended; it is set before done is closed.
	ended time.Time
	// limit is how long the run may take before Delete stops waiting for it.
	limit time.Duration
}

// Tracker remembers one provider's Cleanup runs in this control-plane
// process, by key (the sandbox, or for Coder the workspace build). The
// reconciler finishes a whole pass before starting the next, so Delete starts
// a run in the background and reports it unfinished instead of holding every
// other sandbox in the pass for up to Budget; a later tick's Delete waits for
// the same run. The hook itself runs the commands at most once per sandbox,
// so a run another replica starts only waits for and reports the first one.
//
// At most MaxRuns runs go at once for the provider, and MaxOrgRuns for one
// organization; the rest wait for a slot in arrival order, each skipping only
// the runs ahead of it whose organization is at its share, so one
// organization's long Cleanups never hold every slot or keep another
// organization's run waiting behind its own queue. A run waits up to its
// queue wait (QueueWait scaled by its budget, sandbox.CleanupQueueWait, plus
// how long its organization's runs ahead of it may still take) and is then
// reported as not run, so a burst of deletions cannot flood the provider and
// a deletion is never held past its deadline.
type Tracker struct {
	runs sync.Map // key -> *Run
	// skipped records the sandboxes whose Cleanup was reported as not run
	// (SkipOnce): key -> when.
	skipped sync.Map

	// MaxRuns, MaxOrgRuns and QueueWait override DefaultMaxRuns,
	// DefaultMaxOrgRuns and DefaultQueueWait when positive (tests).
	MaxRuns    int
	MaxOrgRuns int
	QueueWait  time.Duration

	mu      sync.Mutex
	running int
	byOrg   map[string]int
	// holding are the runs holding a slot.
	holding map[*slotWaiter]struct{}
	// waiting are the runs waiting for a slot, in arrival order.
	waiting []*slotWaiter
}

// slotWaiter is one run waiting for a slot; granted is closed once it holds
// one, and ends is then when its limit runs out.
type slotWaiter struct {
	orgID   string
	limit   time.Duration
	granted chan struct{}
	ends    time.Time
}

const (
	// DefaultMaxRuns bounds one provider's concurrent Cleanup runs in a
	// control-plane process: room for eight organizations at their share.
	DefaultMaxRuns = 16
	// DefaultMaxOrgRuns bounds one organization's share of them.
	DefaultMaxOrgRuns = 2
	// DefaultQueueWait is how long a run with no more than Budget waits for a
	// slot; a longer budget waits that much longer (sandbox.CleanupQueueWait),
	// and a run behind its organization's own runs waits as long as they may
	// still take (at most sandbox.MaxCleanupBudget more). With a run's own
	// limit (its Plan.Budget, Margin and a resume) and StaleAfter it keeps a
	// deletion within the reconciler's deletion deadline (15 minutes, plus
	// sandbox.CleanupDeletionExtra of the run's budget, plus the wait for its
	// organization's runs, sandbox.ReportCleanupQueueHold).
	DefaultQueueWait = sandbox.MinCleanupQueueWait
)

// QueueFullReason is what a Cleanup that waited too long for a slot reports
// on each of its steps.
const QueueFullReason = "AO was running too many Cleanups at once, so this Cleanup step did not run before the sandbox was deleted."

// OrgQueueFullReason is what a Cleanup that waited too long for one of its
// organization's maxOrgRuns slots reports on each of its steps.
func OrgQueueFullReason(maxOrgRuns int) string {
	return "This organization already has " + strconv.Itoa(maxOrgRuns) +
		" Cleanups running, so this Cleanup step did not run before the sandbox was deleted."
}

// Lookup returns the run recorded for key.
func (t *Tracker) Lookup(key string) (*Run, bool) {
	run, ok := t.runs.Load(key)
	if !ok {
		return nil, false
	}
	return run.(*Run), true
}

// Start runs fn in the background as key's run, once a slot is free for the
// provider and orgID ("" counts only against the provider), unless a run is
// already recorded for key; it returns the run recorded for key. budget is the
// run's Cleanup budget (Plan.Budget) and limit how long the run may take once
// it has a slot. A run that waits its queue wait (QueueWait plus how far
// budget exceeds Budget, see sandbox.CleanupQueueWait, plus how long orgID's
// runs ahead of it may still take, reported to ctx's
// sandbox.ReportCleanupQueueHold) without a slot calls skip with
// QueueFullReason, or OrgQueueFullReason when orgID's share was what it
// waited for, instead. Delete waits for the run up to the queue wait, limit
// and StaleAfter.
func (t *Tracker) Start(ctx context.Context, key, orgID string, budget, limit time.Duration, fn func(), skip func(reason string)) *Run {
	t.prune()
	waiter := &slotWaiter{orgID: orgID, limit: limit, granted: make(chan struct{})}
	t.mu.Lock()
	hold := t.orgHold(orgID)
	queueWait := t.queueWait(budget) + hold
	run := &Run{done: make(chan struct{}), started: time.Now(), limit: queueWait + limit}
	if existing, loaded := t.runs.LoadOrStore(key, run); loaded {
		t.mu.Unlock()
		return existing.(*Run)
	}
	t.waiting = append(t.waiting, waiter)
	t.dispatch()
	t.mu.Unlock()
	if hold > 0 {
		sandbox.ReportCleanupQueueHold(ctx, hold)
	}
	go func() {
		defer func() {
			run.ended = time.Now()
			close(run.done)
		}()
		if reason, ok := t.acquire(waiter, queueWait); !ok {
			slog.Warn("project cleanup waited too long for a slot; deleting the sandbox without it",
				"key", key, "org_id", orgID, "queue_wait", queueWait, "reason", reason)
			skip(reason)
			return
		}
		defer t.release(waiter)
		fn()
	}()
	return run
}

// queueWait is how long a run with budget waits for a slot: QueueWait (else
// DefaultQueueWait) plus how far budget exceeds Budget, as
// sandbox.CleanupQueueWait scales it.
func (t *Tracker) queueWait(budget time.Duration) time.Duration {
	base := t.QueueWait
	if base <= 0 {
		base = DefaultQueueWait
	}
	return base + sandbox.CleanupQueueWait(budget) - sandbox.MinCleanupQueueWait
}

// limits are the provider's and one organization's slot counts.
func (t *Tracker) limits() (maxRuns, maxOrgRuns int) {
	maxRuns, maxOrgRuns = t.MaxRuns, t.MaxOrgRuns
	if maxRuns <= 0 {
		maxRuns = DefaultMaxRuns
	}
	if maxOrgRuns <= 0 {
		maxOrgRuns = DefaultMaxOrgRuns
	}
	return maxRuns, maxOrgRuns
}

// orgHold is how long a run of orgID that arrives now may wait for one of
// the organization's slots: the earliest a slot frees if each of its runs,
// holding one or waiting ahead in arrival order, takes its whole limit; at
// most sandbox.MaxCleanupBudget. The caller holds t.mu.
func (t *Tracker) orgHold(orgID string) time.Duration {
	if orgID == "" {
		return 0
	}
	_, maxOrgRuns := t.limits()
	now := time.Now()
	// frees[i] is how long from now the organization's slot i stays taken.
	frees := make([]time.Duration, 0, maxOrgRuns)
	for held := range t.holding {
		if held.orgID == orgID && len(frees) < maxOrgRuns {
			frees = append(frees, max(0, held.ends.Sub(now)))
		}
	}
	for len(frees) < maxOrgRuns {
		frees = append(frees, 0)
	}
	soonest := func() int {
		index := 0
		for slot, free := range frees {
			if free < frees[index] {
				index = slot
			}
		}
		return index
	}
	for _, queued := range t.waiting {
		if queued.orgID == orgID {
			frees[soonest()] += max(0, queued.limit)
		}
	}
	return min(frees[soonest()], sandbox.MaxCleanupBudget)
}

// acquire waits up to wait for waiter's slot (dispatch). Without one it
// reports why: QueueFullReason, or OrgQueueFullReason when the organization
// still holds its share.
func (t *Tracker) acquire(waiter *slotWaiter, wait time.Duration) (string, bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-waiter.granted:
		return "", true
	case <-timer.C:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-waiter.granted:
		// Granted while the timer fired: the run takes its slot.
		return "", true
	default:
	}
	for index, queued := range t.waiting {
		if queued == waiter {
			t.waiting = append(t.waiting[:index], t.waiting[index+1:]...)
			break
		}
	}
	if _, maxOrgRuns := t.limits(); waiter.orgID != "" && t.byOrg[waiter.orgID] >= maxOrgRuns {
		return OrgQueueFullReason(maxOrgRuns), false
	}
	return QueueFullReason, false
}

// dispatch gives free slots to the waiting runs in arrival order. A run whose
// organization already holds its share stays waiting without holding up the
// runs behind it, so one organization never keeps another's runs waiting.
// The caller holds t.mu.
func (t *Tracker) dispatch() {
	maxRuns, maxOrgRuns := t.limits()
	if t.byOrg == nil {
		t.byOrg = map[string]int{}
	}
	if t.holding == nil {
		t.holding = map[*slotWaiter]struct{}{}
	}
	kept := t.waiting[:0]
	for _, waiter := range t.waiting {
		if t.running < maxRuns && (waiter.orgID == "" || t.byOrg[waiter.orgID] < maxOrgRuns) {
			t.running++
			if waiter.orgID != "" {
				t.byOrg[waiter.orgID]++
			}
			waiter.ends = time.Now().Add(waiter.limit)
			t.holding[waiter] = struct{}{}
			close(waiter.granted)
			continue
		}
		kept = append(kept, waiter)
	}
	clear(t.waiting[len(kept):])
	t.waiting = kept
}

func (t *Tracker) release(waiter *slotWaiter) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running--
	delete(t.holding, waiter)
	if orgID := waiter.orgID; orgID != "" {
		if t.byOrg[orgID]--; t.byOrg[orgID] <= 0 {
			delete(t.byOrg, orgID)
		}
	}
	t.dispatch()
}

// Skipper returns a Start skip function that reports the run as not run
// (ReportNotRun) under ctx's stage observer and session event recorder.
func Skipper(ctx context.Context, attrs map[string]string, steps worker.CleanupRun) func(reason string) {
	return func(reason string) {
		ReportNotRun(ctx, sandbox.BeginStage(ctx, Stage), attrs, steps, reason,
			fmt.Errorf("project cleanup did not run: %s", reason))
	}
}

// SkipOnce reports, once per key (the sandbox), a Cleanup that does not run
// because the sandbox cannot run it (reason, said on each step: for example
// NotRunningReason). A re-issued Delete reports nothing again. The report is
// kept apart from the runs Lookup reads, so a later Delete that finds the
// sandbox running still runs its Cleanup. It takes no run slot and never
// holds the deletion.
func (t *Tracker) SkipOnce(ctx context.Context, key string, attrs map[string]string, steps worker.CleanupRun, reason string) {
	if _, loaded := t.skipped.LoadOrStore(key, time.Now()); loaded {
		return
	}
	t.prune()
	ReportNotRun(ctx, sandbox.BeginStage(ctx, Stage), attrs, steps, reason, nil)
}

func (t *Tracker) prune() {
	t.skipped.Range(func(key, value any) bool {
		if time.Since(value.(time.Time)) > RecordTTL {
			t.skipped.Delete(key)
		}
		return true
	})
	t.runs.Range(func(key, value any) bool {
		run := value.(*Run)
		select {
		case <-run.done:
			if time.Since(run.ended) > RecordTTL {
				t.runs.Delete(key)
			}
		default:
		}
		return true
	})
}

// Await waits up to inline (InlineWait when not positive) for the run and
// reports whether Delete may proceed. A run past its own limit plus
// StaleAfter counts as finished, so Cleanup can delay a deletion but never
// block it.
func (r *Run) Await(ctx context.Context, inline time.Duration, logArgs ...any) bool {
	if inline <= 0 {
		inline = InlineWait
	}
	timer := time.NewTimer(inline)
	defer timer.Stop()
	select {
	case <-r.done:
		return true
	case <-ctx.Done():
	case <-timer.C:
	}
	if time.Since(r.started) > r.limit+StaleAfter {
		slog.WarnContext(ctx, "project cleanup never reported; deleting the sandbox anyway",
			append(logArgs, "started", r.started)...)
		return true
	}
	return false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
