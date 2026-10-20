package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

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
	event, ran := worker.SetupRunner{DataDir: dataDir, FirstStart: firstStart}.Run(ctx, commands, workspace, environment)
	if !ran {
		if len(commands) > 0 {
			logger.Info("project setup: not run; the session's workspace was set up before", "steps", len(commands))
		}
		return nil
	}
	logger.Info("project setup: ran", "workspace", workspace, "steps", len(commands))
	attrs := []any{
		"status", event.Status, "step", event.Step, "exit_code", event.ExitCode,
		"duration_ms", event.DurationMS, "output", activity.Redact(event.Output),
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

// projectScripts are the project's Setup and Cleanup commands (Project
// settings > Scripts).
type projectScripts struct {
	PostCreate []string
	PreRemove  []string
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
	refreshed := projectScripts{PostCreate: response.Launch.PostCreate, PreRemove: response.Launch.PreRemove}
	if err := worker.WriteCleanupHook(dataDir, refreshed.PreRemove, workspace, environment); err != nil {
		logger.Warn("reinstall project cleanup hook", "error", err)
	}
	return refreshed
}

// setupStartupFailure is the session startup error for a failed Setup run:
// the step and its exit status, with the command's output as the detail.
func setupStartupFailure(event worker.SetupEvent) worker.WorkspaceStartupEvent {
	return worker.WorkspaceStartupEvent{
		Status:  worker.WorkspaceStartupFailed,
		Code:    domain.WorkspaceStartupErrorProjectSetup,
		Message: event.Summary() + ". Fix the project's Setup script (Project settings > Scripts), then reopen this session to retry.",
		Detail:  event.Output,
	}
}

// checkoutStartupFailure is the session startup error for a checkout that
// could not be prepared: the Coder connection's working directory, or the
// repository checkout itself.
func checkoutStartupFailure(err error) worker.WorkspaceStartupEvent {
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
