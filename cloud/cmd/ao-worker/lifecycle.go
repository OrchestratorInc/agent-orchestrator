package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// setupMessageOutputLimit bounds the command output quoted in the startup
// error a failed Setup shows on the session.
const setupMessageOutputLimit = 2048

// readWorkingDirectory reads a bring-your-own Coder connection's working
// directory and removes it from the process environment, so the coding agent
// and its tools never inherit it.
func readWorkingDirectory() string {
	defer func() { _ = os.Unsetenv(worker.WorkingDirectoryEnv) }()
	return strings.TrimSpace(os.Getenv(worker.WorkingDirectoryEnv))
}

// readCheckoutDirectory reads the project's working-folder checkout a
// dedicated-user worker clones into (worker.CheckoutDirectoryEnv) and removes
// it from the process environment; AO_WORKSPACE_DIR names the same folder.
func readCheckoutDirectory() string {
	defer func() { _ = os.Unsetenv(worker.CheckoutDirectoryEnv) }()
	return strings.TrimSpace(os.Getenv(worker.CheckoutDirectoryEnv))
}

// resolveCheckoutDirectory checks the project's working folder is AO's own
// before the session checks the repository out there (see
// worker.ResolveOwnedCheckoutDirectory). A folder the template created stops
// the startup: AO never falls back to another folder for it.
func resolveCheckoutDirectory(logger *slog.Logger, dir string) error {
	if err := worker.ResolveOwnedCheckoutDirectory(dir, os.Getenv(worker.UserHomeEnv)); err != nil {
		logger.Error("working folder: the project's working folder cannot be used", "workspace", dir, "error", err)
		return err
	}
	logger.Info("working folder: checking the repository out in the project's working folder", "workspace", dir)
	_ = os.Setenv("AO_WORKSPACE_DIR", dir)
	return nil
}

// recordOwnedRepoFolders marks, for a session whose checkouts must be AO's
// own (owned: a dedicated-user worker's working folder), that an additional
// repository's folder that already exists beside the working folder and is not
// the worker's (the Coder template's own clone) is never used, as the main
// folder is not (worker.RecordOwnedRepoFolders): AO does not check the
// repository out there, and its Setup and Cleanup steps are skipped. Each such
// repository is reported as skipped. Any other session clears the mark.
func recordOwnedRepoFolders(logger *slog.Logger, dataDir string, owned bool, launch worker.LaunchContext, workspace string) {
	refused, err := worker.RecordOwnedRepoFolders(dataDir, owned,
		worker.SessionRepoCheckouts(workspace, launch.RepositoryURL, launch.ExtraRepos)[1:])
	if err != nil {
		logger.Warn("multi-repo: could not record which additional repository folders are AO's own", "error", err)
	}
	for _, checkout := range refused {
		logger.Warn("multi-repo: additional repository skipped: "+
			worker.DisplayPath(checkout.Path, os.Getenv(worker.UserHomeEnv))+" already exists (created by your Coder template), "+
			"so AO does not check it out there or run its Setup and Cleanup steps in it. "+
			"Untick it in the project's Template options, or turn on 'Run the agent as the workspace user' to reuse it.",
			"repo", checkout.URL, "path", checkout.Path)
	}
}

// refusedCheckoutWorkspace names the empty folder, in the worker's own data
// directory, a session whose working folder was refused works in: its shell,
// terminal and agent never open in the refused folder, which is not AO's.
const refusedCheckoutWorkspace = "no-working-folder"

// settleCheckoutDirectory settles the session's workspace on its working
// folder (resolveCheckoutDirectory). When the folder cannot be used it returns
// the error with a workspace of AO's own instead (refusedCheckoutWorkspace in
// dataDir), named by AO_WORKSPACE_DIR, so nothing of the session (its shell,
// agent or Cleanup) runs in a folder something else created.
func settleCheckoutDirectory(logger *slog.Logger, dir, dataDir string) (string, error) {
	err := resolveCheckoutDirectory(logger, dir)
	if err == nil {
		return dir, nil
	}
	fallback := filepath.Join(dataDir, refusedCheckoutWorkspace)
	_ = os.Setenv("AO_WORKSPACE_DIR", fallback)
	return fallback, err
}

// resolveConfiguredWorkspace settles the session on the connection's working
// directory. An adopted checkout gets the same process-scoped git setup as a
// reused one; a directory AO clones into is treated like its own checkout.
func resolveConfiguredWorkspace(
	ctx context.Context,
	logger *slog.Logger,
	bootstrap worker.BootstrapResponse,
	template, defaultWorkspace, dataDir string,
) (worker.ReuseDecision, error) {
	home := strings.TrimSpace(os.Getenv("HOME"))
	decision, err := worker.ResolveWorkingDirectory(
		ctx, worker.ExecGitRunner{}, template, home, dataDir,
		bootstrap.Launch.RepositoryURL, bootstrap.SessionID,
		bootstrap.Launch.Branch, bootstrap.Launch.DefaultBranch,
	)
	if err != nil {
		logger.Error("working directory: the configured directory cannot be used",
			"working_directory", template, "error", err)
		return worker.ReuseDecision{Workspace: defaultWorkspace}, err
	}
	if decision.Reused {
		logger.Info("working directory: working in the template's existing checkout",
			"workspace", decision.Workspace, "adopted_now", decision.AdoptedNow, "stash", decision.Stash)
		for key, value := range worker.ReusedCheckoutGitEnvironment(dataDir) {
			_ = os.Setenv(key, value)
		}
	} else {
		logger.Info("working directory: cloning the repository into the configured directory",
			"workspace", decision.Workspace)
	}
	_ = os.Setenv("AO_WORKSPACE_DIR", decision.Workspace)
	return decision, nil
}

// runProjectSetup runs the project's Setup commands (Project settings >
// Scripts) once per session, after the checkout and before the coding agent
// starts, and reports the outcome on the session's event stream. Like a
// local project, a failed step stops the session from starting its agent: it
// returns the failed outcome, and the next session open retries.
func runProjectSetup(
	ctx context.Context,
	logger *slog.Logger,
	client *client,
	dataDir string,
	firstStart bool,
	commands []string,
	workspace string,
	environment worker.SessionScriptEnvironment,
) (failed *worker.SetupEvent) {
	event, ran := worker.SetupRunner{
		DataDir: dataDir, FirstStart: firstStart,
		Publish: scriptStepPublisher(logger, client),
		Secrets: func() []string { return []string{client.currentToken()} },
	}.Run(ctx, commands, workspace, environment)
	if !ran {
		if len(commands) > 0 {
			logger.Info("project setup: not run; the session's workspace was set up before", "steps", len(commands))
		}
		return nil
	}
	logger.Info("project setup: ran", "workspace", workspace, "steps", len(commands))
	// The run already masked a failure's output and command as its step
	// events are (the secret environment values and the worker's token, then
	// the patterns); the script redaction again here keeps that true of
	// anything else the outcome carries. activity.Redact alone masks a quoted
	// value only up to an escaped quote, which leaves a SecretString's
	// password in place.
	event.Command = worker.RedactScriptOutput(event.Command)
	event.Output = worker.RedactScriptOutput(event.Output)
	attrs := []any{
		"status", event.Status, "step", event.Step, "exit_code", event.ExitCode,
		"duration_ms", event.DurationMS, "output", event.Output,
	}
	if event.Status != worker.SetupSucceeded {
		attrs = append(attrs, "command", event.Command, "repository", event.Repository, "dir", event.Dir,
			"user", event.User, "groups", strings.Join(event.Groups, " "), "log", event.LogPath, "hint", event.Hint)
	}
	event.Output = activity.Excerpt(event.Output, setupMessageOutputLimit)
	if event.Status == worker.SetupSucceeded {
		logger.Info("project setup: finished", attrs...)
	} else {
		logger.Warn("project setup: a step did not succeed; the agent will not start", attrs...)
		failed = &event
	}
	if err := client.publishEvent(ctx, worker.SetupEventType, event); err != nil {
		logger.Warn("publish project setup outcome", "error", err)
	}
	return failed
}

// scriptStepPublisher publishes a Setup run's step events. A control plane
// from before live Setup logs refuses the event type: that is reported as
// worker.ErrScriptStepsUnsupported (the run then stops sending) and logged
// once, and any other failure only at debug level, since a dropped update is
// expected and Setup never depends on it.
func scriptStepPublisher(logger *slog.Logger, client *client) func(context.Context, worker.ScriptStepEvent) error {
	return func(ctx context.Context, event worker.ScriptStepEvent) error {
		err := client.publishEvent(ctx, worker.ScriptStepEventType, event)
		switch {
		case err == nil:
			return nil
		case strings.Contains(err.Error(), "INVALID_EVENT_TYPE"):
			scriptStepsUnsupportedOnce.Do(func() {
				logger.Info("project setup: the control plane does not take live step logs; sending only the outcome")
			})
			return worker.ErrScriptStepsUnsupported
		default:
			logger.Debug("publish project setup step", "step", event.Step, "status", event.Status, "error", err)
			return err
		}
	}
}

var scriptStepsUnsupportedOnce sync.Once

// projectScripts are the project's Setup and Cleanup commands (Project
// settings > Scripts), each led by its time limit's marker when it sets one
// (worker.LaunchContext.ProjectScripts).
type projectScripts struct {
	PostCreate []string
	PreRemove  []string
}

// awaitStartupRetry reports a failed checkout or Setup (report) and waits
// for a retry request: the session reopened, or its Retry. A request already
// waiting when the step failed is dropped first. The desktop asks once when
// the session is opened, and opening a new session usually happens while its
// checkout and Setup are still running, so that request was made before
// anything failed. Kept, it re-ran the failed step the moment it failed, with
// nothing done by the user. Only a request made once the failure is reported
// retries the step. It reports false when ctx ends first.
func awaitStartupRetry(ctx context.Context, requests <-chan struct{}, report func()) bool {
	dropPendingReopen(requests)
	report()
	select {
	case <-ctx.Done():
		return false
	case <-requests:
		return true
	}
}

// refreshProjectScripts reads the project's current Setup and Cleanup
// commands before a reopened session retries Setup, so a script fixed in
// Project settings > Scripts is the one the retry runs, as a local retry reads
// the current project config. The Cleanup hook is reinstalled with the current
// commands too. If the control plane can't be read, the commands the worker
// already has are kept.
func refreshProjectScripts(
	ctx context.Context,
	logger *slog.Logger,
	client *client,
	dataDir, workspace string,
	environment worker.SessionScriptEnvironment,
	current projectScripts,
) projectScripts {
	response, err := client.reconnect(ctx)
	if err != nil {
		logger.Warn("project scripts: could not read the current Setup and Cleanup commands; retrying with the known ones", "error", err)
		return current
	}
	var refreshed projectScripts
	refreshed.PostCreate, refreshed.PreRemove = response.Launch.ProjectScripts()
	if err := worker.WriteCleanupHook(dataDir, refreshed.PreRemove, workspace, environment); err != nil {
		logger.Warn("reinstall project cleanup hook", "error", err)
	}
	return refreshed
}

// setupStartupFailure is the session startup error for a failed Setup run, in
// the local daemon's words: the step and its exit status (or time limit), a
// hint for a cause the output names, and where to fix it. The detail is the
// step's command, who and where it ran, and its output.
func setupStartupFailure(event worker.SetupEvent) worker.WorkspaceStartupEvent {
	message := event.Summary() + ", so the agent did not start."
	if event.Hint != "" {
		message += " " + event.Hint
	}
	message += " Fix the script in Project settings > Scripts, then try again."
	detail := event.Output
	if context := event.Context(); context != "" {
		detail = context
		if output := strings.TrimSpace(event.Output); output != "" {
			detail += "\n\n" + output
		}
	}
	return worker.WorkspaceStartupEvent{
		Status:  worker.WorkspaceStartupFailed,
		Code:    domain.WorkspaceStartupErrorProjectSetup,
		Message: message,
		Detail:  detail,
	}
}

// checkoutStartupFailure is the session startup error for a checkout that
// could not be prepared: the Coder connection's working directory, or the
// repository checkout itself.
func checkoutStartupFailure(err error) worker.WorkspaceStartupEvent {
	if errors.Is(err, worker.ErrWorkingDirectoryOccupied) {
		folder := strings.TrimPrefix(err.Error(), worker.ErrWorkingDirectoryOccupied.Error()+": ")
		return worker.WorkspaceStartupEvent{
			Status: worker.WorkspaceStartupFailed,
			Code:   domain.WorkspaceStartupErrorWorkingDirectoryOccupied,
			Message: folder + " already exists (created by your Coder template). " +
				"Untick it in the project's Template options, or turn on 'Run the agent as the workspace user' to reuse it. " +
				"Start a new session after changing it.",
		}
	}
	if errors.Is(err, worker.ErrCheckoutDirectoryUnusable) {
		return worker.WorkspaceStartupEvent{
			Status: worker.WorkspaceStartupFailed,
			Code:   domain.WorkspaceStartupErrorWorkingDirectory,
			Message: "AO couldn't use this project's working folder: " +
				strings.TrimPrefix(err.Error(), worker.ErrCheckoutDirectoryUnusable.Error()+": ") +
				". Fix the folder or the project's working folder (Project settings > Repositories), then reopen this session.",
		}
	}
	if errors.Is(err, worker.ErrWorkingDirectoryUnusable) {
		return worker.WorkspaceStartupEvent{
			Status: worker.WorkspaceStartupFailed,
			Code:   domain.WorkspaceStartupErrorWorkingDirectory,
			Message: "AO couldn't use the working directory set on your Coder connection: " +
				strings.TrimPrefix(err.Error(), worker.ErrWorkingDirectoryUnusable.Error()+": ") +
				". Fix the directory or the connection's working directory, then reopen this session.",
		}
	}
	if errors.Is(err, worker.ErrSessionBranchSwitch) {
		return worker.WorkspaceStartupEvent{
			Status: worker.WorkspaceStartupFailed,
			Code:   domain.WorkspaceStartupErrorCheckout,
			Message: "AO couldn't put the repository on this session's branch, so the session did not start on a branch cut from somewhere else. " +
				"Move or commit the local files git names below, then reopen this session to retry.",
			Detail: activity.Excerpt(err.Error(), setupMessageOutputLimit),
		}
	}
	return worker.WorkspaceStartupEvent{
		Status:  worker.WorkspaceStartupFailed,
		Code:    domain.WorkspaceStartupErrorCheckout,
		Message: "Repository checkout failed. Check this project's GitHub access, then reopen this session to retry.",
		Detail:  activity.Excerpt(err.Error(), setupMessageOutputLimit),
	}
}

// agentStartFailure is the session startup error for a terminal agent that
// could not start once the workspace was ready: the session's own, or one a
// switch to the terminal (or the Chat-start fallback) started
// (Supervisor.AgentStartFailed), with what failed as the detail.
func agentStartFailure(err error) worker.WorkspaceStartupEvent {
	return worker.WorkspaceStartupEvent{
		Status:  worker.WorkspaceStartupFailed,
		Code:    domain.WorkspaceStartupErrorAgentStart,
		Message: "AO couldn't start the coding agent in the terminal. Select Retry on this session to try again.",
		Detail:  activity.Excerpt(err.Error(), setupMessageOutputLimit),
	}
}

// reportWorkspaceStartup tells the control plane whether the workspace is
// ready for the agent, so a failure is shown on the session's startup screen
// (and cleared once a retry succeeds). A control plane that predates the event
// refuses it; the worker then behaves as before.
func reportWorkspaceStartup(ctx context.Context, logger *slog.Logger, client *client, event worker.WorkspaceStartupEvent) {
	if err := client.publishEvent(ctx, worker.WorkspaceStartupEventType, event); err != nil {
		logger.Warn("publish workspace startup outcome", "status", event.Status, "error", err)
	}
}

// dropPendingReopen discards a session open saved while a failed start was
// still running. It came before the failure, so it is not a retry: the start
// waits for the session to be opened again after the failure is reported.
func dropPendingReopen(reopen <-chan struct{}) {
	select {
	case <-reopen:
	default:
	}
}
