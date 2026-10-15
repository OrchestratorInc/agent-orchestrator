// Command ao-worker is the supervisor process that runs inside one sandbox. It
// receives no permanent credential and opens no inbound port: it reads a
// one-time bootstrap ticket from its environment, dials the control plane
// outward to exchange it for a short-lived token, and then heartbeats.
//
// It prepares the repository checkout and supervises the interactive coding
// agent PTY while a separate heartbeat loop keeps its epoch-scoped token current.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/cloud/internal/notificationoutbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/skillassets"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workertransport"
)

const (
	workerVersion     = "0.1.0"
	heartbeatInterval = 20 * time.Second
	// The control plane recreates a sandbox whose worker has been silent for a
	// minute, so a worker that cannot reach home for longer than that is
	// already being replaced and should exit rather than compete with its
	// successor.
	maxHeartbeatFailures = 3
	requestTimeout       = 30 * time.Second
	maxResponseBody      = 1 << 20
)

var workerCapabilities = []string{
	"worker.heartbeat",
	"worker.events",
	"agent.activity",
	"worker.turns",
	"worker.credentials",
	"repository.checkout",
	"workspace.files",
	"terminal.workspace",
	"terminal.agent",
	"notification.events",
	worker.CapabilityWorkspacePathPlaceholder,
}

func main() {
	// The coding agent runs this binary as its PreToolUse hook (see
	// workerexec.RunSecretFileGuard); answer it before any worker setup.
	if len(os.Args) == 2 && os.Args[1] == workerexec.SecretFileGuardArg {
		os.Exit(workerexec.RunSecretFileGuard(os.Stdin, os.Stderr))
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("worker exited", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AO_CLOUD_PUBLIC_URL")), "/")
	sessionID := strings.TrimSpace(os.Getenv("AO_CLOUD_SESSION_ID"))
	bootstrapToken := strings.TrimSpace(os.Getenv("AO_WORKER_BOOTSTRAP_TOKEN"))
	workspace := strings.TrimSpace(os.Getenv("AO_WORKSPACE_DIR"))
	if publicURL == "" {
		return errors.New("AO_CLOUD_PUBLIC_URL is required")
	}
	if sessionID == "" {
		return errors.New("AO_CLOUD_SESSION_ID is required")
	}
	// AO_WORKER_BOOTSTRAP_TOKEN is not required up front: a restarted worker
	// reconnects with its persisted token, and connect() validates that a ticket
	// is present only when no valid token exists.
	if workspace == "" {
		return errors.New("AO_WORKSPACE_DIR is required")
	}
	// Nothing the worker runs may wait on an interactive prompt: there is no
	// one to answer it, and on a terminal it can stop the worker outright. A
	// git operation without usable credentials (the checkpoint push to a
	// read-only grant) must fail and be logged, not ask for a username.
	disableInteractivePrompts()
	dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create worker data directory: %w", err)
	}
	// The system prompt points the agent at this path; the skill enhances the
	// prompts and is not load-bearing, so a failed install only warns.
	if err := skillassets.Install(dataDir); err != nil {
		logger.Warn("install using-ao skill assets", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := &client{
		baseURL:   publicURL + "/api/cloud/v1",
		http:      &http.Client{Timeout: requestTimeout},
		tokenFile: filepath.Join(dataDir, "worker-token"),
	}

	// Heal a stale baked binary before any credential-bearing work, so the
	// control plane's exact worker version runs even when the template lags a
	// deployment. A no-op when the control plane advertised no expected hash.
	if err := selfUpdateIfStale(ctx, logger, publicURL, dataDir); err != nil {
		logger.Warn("worker self-update check failed; continuing", "error", err)
	}

	// The session's first worker start has no persisted worker token yet (a
	// self-update re-exec happens before connect, so it still counts).
	// Project Setup runs only when the session's workspace is first prepared.
	_, tokenErr := os.Stat(client.tokenFile)
	firstStart := errors.Is(tokenErr, os.ErrNotExist)

	bootstrap, err := client.connect(ctx, logger, bootstrapToken)
	if err != nil {
		return err
	}
	if bootstrap.SessionID != sessionID {
		return errors.New("worker session does not match AO_CLOUD_SESSION_ID")
	}
	// Any ticket was single-use and is now spent; from here the only credential
	// is the rotating worker token, already persisted by connect.
	_ = os.Unsetenv("AO_WORKER_BOOTSTRAP_TOKEN")
	bootstrapToken = ""
	logger.Info("worker connected",
		"session_id", bootstrap.SessionID,
		"worker_id", bootstrap.WorkerID,
		"epoch", bootstrap.Epoch,
		"harness", bootstrap.Launch.Harness,
		"repository_url", bootstrap.Launch.RepositoryURL,
	)

	// A Coder template that already cloned the repository (and runs its dev
	// stack there) can ask the session to work in that checkout instead of a
	// fresh clone. Decide before anything captures the workspace path.
	// A configured working directory replaces that search: the session works
	// exactly there, adopting the template's checkout or cloning into it.
	workingDirectory := readWorkingDirectory()
	protectSecretFiles := os.Getenv(worker.ProtectSecretFilesEnv) == "1"
	secretFileGuard := ""
	if protectSecretFiles {
		secretFileGuard = secretFileGuardCommand(logger)
	}
	reuse := worker.ReuseDecision{Workspace: workspace}
	var workingDirectoryErr error
	hasRepository := strings.TrimSpace(bootstrap.Launch.RepositoryURL) != "" &&
		!worker.IsScratchRepositoryURL(bootstrap.Launch.RepositoryURL)
	switch {
	case workingDirectory != "" && hasRepository:
		reuse, workingDirectoryErr = resolveConfiguredWorkspace(ctx, logger, bootstrap, workingDirectory, workspace, dataDir)
		workspace = reuse.Workspace
	case os.Getenv(worker.ReuseExistingCheckoutEnv) == "1" && hasRepository:
		reuse = resolveReusedWorkspace(ctx, logger, bootstrap, workspace, dataDir)
		workspace = reuse.Workspace
	}
	scriptEnvironment := worker.SessionScriptEnvironment{
		SessionID:     bootstrap.SessionID,
		SessionKind:   bootstrap.Launch.Kind,
		Branch:        bootstrap.Launch.Branch,
		WorkspaceDir:  workspace,
		RepositoryURL: bootstrap.Launch.RepositoryURL,
		ProjectID:     bootstrap.Launch.ProjectID,
		ProjectName:   bootstrap.Launch.ProjectName,
	}
	// The project's Cleanup commands run before the workspace is deleted; the
	// provider runs this hook (Coder today) since the worker is gone by then.
	if err := worker.WriteCleanupHook(dataDir, bootstrap.Launch.PreRemove, workspace, scriptEnvironment); err != nil {
		logger.Warn("install project cleanup hook", "error", err)
	}

	// The workspace shell is deliberately available before checkout starts. A
	// developer can inspect the sandbox immediately while repository preparation
	// and coding-agent authentication continue in the background.
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return fmt.Errorf("create workspace directory: %w", err)
	}
	for key, value := range map[string]string{
		"AO_CLOUD_PUBLIC_URL":   publicURL,
		"AO_SESSION_ID":         bootstrap.SessionID,
		"AO_SESSION_BRANCH":     bootstrap.Launch.Branch,
		"AO_DATA_DIR":           dataDir,
		"AO_CLOUD_WORKER_EPOCH": strconv.FormatInt(bootstrap.Epoch, 10),
	} {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set worker tooling environment %s: %w", key, err)
		}
	}
	// An agent that outlived the previous worker still has that worker's epoch
	// in its environment; its hooks read the current one from this file, so
	// the notifications they queue are delivered rather than discarded.
	if err := notificationoutbox.WriteWorkerEpoch(dataDir, bootstrap.Epoch); err != nil {
		logger.Warn("record worker epoch for agent hooks", "error", err)
	}
	if !worker.IsScratchRepositoryURL(bootstrap.Launch.RepositoryURL) {
		if err := os.Setenv("PATH", worker.ToolingBinDir(dataDir)+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			return fmt.Errorf("activate worker tooling: %w", err)
		}
	}
	// Heartbeat before waiting out the first interval. Bootstrap registration is
	// not a check-in, so a repaired worker can otherwise be replaced again
	// before the control plane ever observes it. It runs beside the rest of
	// startup: worker tokens are signed and a renewal does not revoke the one in
	// use, so nothing below has to wait a control-plane round trip for it.
	go func() {
		if renewed, err := client.heartbeat(ctx); err != nil {
			logger.Warn("first heartbeat failed", "error", err)
		} else if err := client.setToken(renewed); err != nil {
			logger.Warn("persist renewed worker token", "error", err)
		}
	}()
	var agentCommandFactory workertransport.AgentCommandFactory
	pullRequestSocketPath := filepath.Join(dataDir, "ao-pull-request.sock")
	reviewSocketPath := filepath.Join(dataDir, "ao-review.sock")
	checkpointSocketPath := filepath.Join(dataDir, "ao-checkpoint.sock")
	committedInterface := strings.TrimSpace(bootstrap.Launch.Interface)
	if committedInterface == "" {
		committedInterface = workertransport.InterfaceTUI
	}
	var chatRunner workertransport.ChatRunner
	if _, supported := workerexec.SupportedHarness(bootstrap.Launch.Harness); !supported {
		// Workspace files and shell terminals use the same worker transport as the
		// coding agent. Keep that transport alive when the selected harness is
		// unknown instead of making the whole sandbox unreachable.
		logger.Warn("coding-agent harness unavailable; continuing with workspace transport",
			"error", verifyHarnessAvailable(bootstrap.Launch.Harness))
	} else {
		if err := verifyHarnessAvailable(bootstrap.Launch.Harness); err != nil {
			// A template that installs its tools in the background (the Coder
			// agent reports ready before its own bootstrap finishes) can add the
			// binary moments later. The command is built lazily, so wire the agent
			// up anyway; startInteractiveAgent waits for the binary to appear.
			logger.Warn("coding-agent harness not installed yet; waiting for it", "error", err)
		}
		b := workerexec.HarnessBuilder{
			DataDir: dataDir, Launch: bootstrap.Launch, Env: map[string]string{},
			ProtectSecretFiles: protectSecretFiles, SecretFileGuard: secretFileGuard,
		}
		b.Env["AO_CLOUD_WORKER_API_URL"] = client.baseURL
		b.Env["AO_CLOUD_WORKER_TOKEN_FILE"] = client.tokenFile
		b.Env["AO_SESSION_ID"] = bootstrap.SessionID
		b.Env["AO_PROJECT_ID"] = bootstrap.Launch.ProjectID
		b.Env["AO_SESSION_KIND"] = bootstrap.Launch.Kind
		b.Env["AO_PULL_REQUEST_SOCKET"] = pullRequestSocketPath
		b.Env["AO_PULL_REQUEST_HELP"] = "curl --unix-socket $AO_PULL_REQUEST_SOCKET " +
			`-X POST http://localhost/pull-request -H 'Content-Type: application/json' ` +
			`-d '{"branch":"<pushed branch name>","title":"<PR title>","body":"<PR body>"}' ` +
			"to push the current branch and open a pull request against the project's configured default branch."
		b.Env["AO_REVIEW_SOCKET"] = reviewSocketPath
		b.Env["AO_REVIEW_HELP"] = "curl --unix-socket $AO_REVIEW_SOCKET " +
			`-X POST http://localhost/review -H 'Content-Type: application/json' ` +
			`-d '{"reviewRunId":"<review run id from the prompt>","verdict":"approved|changes_requested","body":"<your findings>"}' ` +
			"to submit an AO-triggered review verdict."
		agentCommandFactory = func(buildCtx context.Context, nativeConversationID, selectedModel, selectedEffort string) (workerexec.Command, error) {
			credential, err := client.Credential(buildCtx)
			if err != nil {
				return workerexec.Command{}, fmt.Errorf("load coding-agent credential: %w", err)
			}
			launch, err := interactiveLaunchSettings(bootstrap.Launch, dataDir, nativeConversationID, selectedModel, selectedEffort)
			if err != nil {
				return workerexec.Command{}, err
			}
			command, err := b.BuildInteractive(launch, credential, workspace)
			credential.Secret = ""
			if err != nil {
				return workerexec.Command{}, fmt.Errorf("build interactive coding-agent command: %w", err)
			}
			command.Env["AO_CHECKPOINT_SOCKET"] = checkpointSocketPath
			return command, nil
		}
		var gitRefReporter func(context.Context) error
		if bootstrap.Launch.RepositoryURL != "" {
			gitRefReporter = func(ctx context.Context) error { return client.reportGitRefs(ctx, workspace) }
		}
		chatRunner = &workerexec.Supervisor{
			Control: client, Builder: b, Runner: workerexec.OSRunner{},
			PullRequestClaimer:  client.claimPullRequest,
			GitRefReporter:      gitRefReporter,
			UseProviderProtocol: true,
			// Use the supervisor's 100 ms default. A one-second worker-command
			// poll makes every phase of a TUI <-> Chat handoff visibly laggy,
			// particularly on remote Linux sandboxes.
			Workspace: workspace, Logger: logger,
		}
	}
	if bootstrap.Launch.Harness == "claude-code" {
		// Chat UI needs the Claude ACP adapter; a bring-your-own template may
		// not ship it, so start installing it now rather than on first chat.
		workerexec.WarmClaudeACPAdapter()
		workerexec.WarmClaudeCode()
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Before any goroutine below can read the prefetch fields.
	client.startStartupPrefetch(runCtx, bootstrap.Launch)
	started := make(chan error, 1)
	chatWorkspaceReady := make(chan struct{})
	checkoutRequested := make(chan struct{}, 1)
	rehydrateDone := make(chan struct{})
	compareBase := ""
	if defaultBranch := strings.TrimSpace(bootstrap.Launch.DefaultBranch); defaultBranch != "" {
		compareBase = "origin/" + defaultBranch
	}
	transportSupervisor := workertransport.Supervisor{
		Control: client, Workspace: workspace, DataDir: dataDir, Harness: bootstrap.Launch.Harness, CompareBase: compareBase, Logger: logger,
		AgentCommandFactory: agentCommandFactory,
		Started:             started, ChatRunner: chatRunner,
		ChatWorkspaceReady: chatWorkspaceReady,
		RequestCheckout: func() error {
			select {
			case <-rehydrateDone:
				return nil
			default:
			}
			select {
			case checkoutRequested <- struct{}{}:
			default:
			}
			return nil
		},
		InitialInterface: committedInterface,
		// Set by providers that restore a paused VM's memory (Freestyle): the
		// agent then survives the worker restart a wake performs.
		PersistAgent:   os.Getenv("AO_WORKER_PERSIST_AGENT") == "1",
		AgentSessionID: bootstrap.Launch.AgentSessionID,
		SelectedModel:  bootstrap.Launch.Model,
		SelectedEffort: bootstrap.Launch.ReasoningEffort,
		SelectionAt:    bootstrap.Launch.SelectionAt,
	}
	busyProbe := transportSupervisor.Busy
	client.busy.Store(&busyProbe)
	transportSupervisor.ProjectReviewCommand = func(ctx context.Context, input worker.TerminalCommand) (workerexec.Command, error) {
		launch, err := reviewerLaunch(bootstrap.Launch, input)
		if err != nil {
			return workerexec.Command{}, err
		}
		credential, err := client.reviewCredential(ctx, input.ReviewRunID)
		if err != nil {
			return workerexec.Command{}, fmt.Errorf("load reviewer credential: %w", err)
		}
		command, err := (workerexec.HarnessBuilder{
			DataDir: dataDir, ConfigRoot: filepath.Join(dataDir, "reviews", input.ReviewRunID),
			ProtectSecretFiles: protectSecretFiles, SecretFileGuard: secretFileGuard,
		}).BuildInteractive(launch, credential, workspace)
		if err != nil {
			return workerexec.Command{}, err
		}
		command.Env["AO_REVIEW_SOCKET"] = reviewSocketPath
		command.Env["AO_REVIEW_HELP"] = reviewHelp()
		command.Env["AO_SESSION_ID"] = bootstrap.SessionID
		command.Env[worker.ReviewTerminalEnv] = "1"
		return command, nil
	}
	// A worker starting in Chat must not leave a TUI agent from an interrupted
	// handoff running beside the Chat runner; end it before anything starts.
	if committedInterface == workertransport.InterfaceChat {
		transportSupervisor.StopPersistentAgent()
	}
	// Real-time terminal streaming (duplex predictive echo) rides the same
	// worker transport; wire it before Run when the sandbox opts in. Preserved
	// from the terminal-stream feature alongside #4960's workspace-ready gate.
	if os.Getenv("AO_CLOUD_TERMINAL_STREAM") == "1" {
		transportSupervisor.Streams = client
	}
	// The coding-agent process may connect before checkout completes, but no
	// user prompt may reach it until the repository is usable. This keeps the
	// perceived connection path independent from clone latency without letting
	// a prompt run in an empty workspace.
	transportSupervisor.HoldAgentInputUntilWorkspaceReady()
	results := make(chan error, 5)
	backgroundWorkers := 4
	go func() { results <- client.heartbeatLoop(runCtx, logger) }()
	go func() { results <- transportSupervisor.Run(runCtx) }()
	go func() {
		results <- runPullRequestBridge(runCtx, pullRequestSocketPath, client, workspace, bootstrap.Launch.DefaultBranch, logger)
	}()
	go func() {
		results <- runReviewBridge(runCtx, reviewSocketPath, client, logger)
	}()
	if outbox, err := notificationoutbox.Open(filepath.Join(dataDir, "notification-outbox.db")); err != nil {
		logger.Warn("open notification outbox", "error", err)
	} else {
		backgroundWorkers++
		go func() {
			defer outbox.Close()
			results <- (&notificationoutbox.Flusher{
				Outbox: outbox, WorkerEpoch: bootstrap.Epoch, Logger: logger,
				// A persisted agent survives worker restarts; what it queued under
				// an earlier epoch is still its live state, so deliver it here.
				AdoptPriorEpochs: transportSupervisor.PersistsAgent(),
				Deliver: func(deliveryCtx context.Context, event notificationoutbox.Event) error {
					if err := transportSupervisor.DeliverNotification(deliveryCtx, event); err == nil {
						return nil
					}
					return client.publishNotification(deliveryCtx, event)
				},
			}).Run(runCtx)
		}()
	}
	if err := <-started; err != nil {
		cancel()
		for index := 0; index < backgroundWorkers; index++ {
			<-results
		}
		return fmt.Errorf("start workspace transport: %w", err)
	}
	if err := client.publishEvent(ctx, "worker.ready", map[string]any{
		"workerId":     bootstrap.WorkerID,
		"epoch":        bootstrap.Epoch,
		"version":      workerVersion,
		"capabilities": workerCapabilities,
	}); err != nil {
		logger.Warn("publish worker.ready failed", "error", err)
	}
	// rehydrateDone gates the coding agent on delete/restore rehydration: the
	// preserved uncommitted work must be applied and the transcript written
	// before the agent is built, so --resume finds the conversation and the
	// workspace holds the restored files. It is closed once (checkout success or
	// failure) so the agent never hangs.
	go func() {
		chatReadyClosed := false
		// reopened records that the session was reopened after a failed start,
		// so the retry reads the project's current Setup commands.
		reopened := false
		for {
			err := workingDirectoryErr
			if err == nil {
				err = prepareWorkspace(runCtx, logger, client, bootstrap, workspace, dataDir, publicURL, reuse)
			}
			if err == nil {
				break
			}
			if runCtx.Err() != nil {
				return
			}
			logger.Error("background workspace startup failed; waiting for session open", "error", err)
			failure := checkoutStartupFailure(err)
			reportWorkspaceStartup(runCtx, logger, client, failure)
			if runner, ok := chatRunner.(interface{ SetWorkspaceStartupError(string) }); ok {
				runner.SetWorkspaceStartupError(failure.Message)
			}
			if !chatReadyClosed {
				close(chatWorkspaceReady)
				chatReadyClosed = true
			}
			select {
			case <-runCtx.Done():
				return
			case <-checkoutRequested:
				reopened = true
			}
			if workingDirectoryErr != nil {
				// The whole worker (agent, terminal, Cleanup hook) was set up
				// around the fallback workspace, so the configured directory is
				// resolved again by a fresh worker: the control plane starts a
				// new one when this one exits.
				logger.Info("working directory: session reopened; restarting the worker to check the directory again")
				os.Exit(workingDirectoryRetryExitCode)
			}
		}
		// Restore a previously deleted session's state before the agent launches.
		// A fresh session finds nothing captured and this returns quickly.
		rehydrateSession(runCtx, logger, client, bootstrap, workspace, dataDir)
		if bootstrap.Launch.RepositoryURL != "" {
			if err := client.reportGitRefs(runCtx, workspace); err != nil {
				logger.Warn("report restored branch heads", "error", err)
			}
		}
		// The project's Setup commands run last, in the prepared checkout, and
		// gate the agent like the checkout does: a failed step keeps the agent
		// from starting until the session is reopened.
		scripts := projectScripts{PostCreate: bootstrap.Launch.PostCreate, PreRemove: bootstrap.Launch.PreRemove}
		for {
			if reopened {
				scripts = refreshProjectScripts(runCtx, logger, client, dataDir, workspace, scriptEnvironment, scripts)
			}
			failed := runProjectSetup(runCtx, logger, client, dataDir, firstStart, scripts.PostCreate, workspace, scriptEnvironment)
			if failed == nil {
				break
			}
			if runCtx.Err() != nil {
				return
			}
			failure := setupStartupFailure(*failed)
			reportWorkspaceStartup(runCtx, logger, client, failure)
			if runner, ok := chatRunner.(interface{ SetWorkspaceStartupError(string) }); ok {
				runner.SetWorkspaceStartupError(failure.Message + ": " + failed.Output)
			}
			if !chatReadyClosed {
				close(chatWorkspaceReady)
				chatReadyClosed = true
			}
			select {
			case <-runCtx.Done():
				return
			case <-checkoutRequested:
				reopened = true
			}
		}
		if runner, ok := chatRunner.(interface{ SetWorkspaceStartupError(string) }); ok {
			runner.SetWorkspaceStartupError("")
		}
		// Clears a failure this or an earlier worker reported for the session.
		reportWorkspaceStartup(runCtx, logger, client, worker.WorkspaceStartupEvent{Status: worker.WorkspaceStartupReady})
		close(rehydrateDone)
		if !chatReadyClosed {
			close(chatWorkspaceReady)
		}
		transportSupervisor.MarkWorkspaceReady()
		// Serve durable-restore checkpointing now that the checkout and the git
		// credential helper are in place. The capture is triggered by the agent's
		// turn-completion (Stop) hook via this unix socket, not a timer. Bound to
		// runCtx: it stops on shutdown.
		cp := newCheckpointer(client, bootstrap, workspace, dataDir, logger)
		if err := runCheckpointBridge(runCtx, checkpointSocketPath, cp.checkpoint, logger); err != nil &&
			runCtx.Err() == nil {
			logger.Warn("checkpoint bridge stopped", "error", err)
		}
	}()
	go func() {
		if err := startInteractiveAgent(
			runCtx, logger, client, bootstrap, workspace, dataDir,
			pullRequestSocketPath, reviewSocketPath, checkpointSocketPath, &transportSupervisor, rehydrateDone,
		); err != nil && runCtx.Err() == nil {
			logger.Error("background coding-agent startup failed", "error", err)
		}
	}()
	first := <-results
	cancel()
	for index := 1; index < backgroundWorkers; index++ {
		<-results
	}
	if ctx.Err() != nil {
		logger.Info("worker shutting down")
		return nil
	}
	return first
}

func interactiveLaunchSettings(launch worker.LaunchContext, dataDir, nativeConversationID, selectedModel, selectedEffort string) (worker.LaunchContext, error) {
	launch.AgentSessionID = strings.TrimSpace(nativeConversationID)
	if selectedModel != "" || selectedEffort != "" {
		if selectedModel != "" {
			launch.Model = selectedModel
		}
		launch.ReasoningEffort = selectedEffort
		return launch, nil
	}
	if launch.AgentSessionID == "" {
		return launch, nil
	}
	var model, effort string
	var err error
	switch launch.Harness {
	case "codex":
		model, effort, err = workerexec.CodexConversationSettingsAfter(dataDir, launch.AgentSessionID, launch.SelectionAt)
	case "claude-code":
		model, effort, err = workerexec.ClaudeConversationSettingsAfter(dataDir, launch.AgentSessionID, launch.SelectionAt)
	default:
		return launch, nil
	}
	if err != nil {
		return launch, fmt.Errorf("read %s conversation settings: %w", launch.Harness, err)
	}
	if model != "" {
		launch.Model = model
		launch.ReasoningEffort = effort
	}
	return launch, nil
}

func prepareWorkspace(
	ctx context.Context,
	logger *slog.Logger,
	client *client,
	bootstrap worker.BootstrapResponse,
	workspace, dataDir, publicURL string,
	reuse worker.ReuseDecision,
) error {
	if worker.IsScratchRepositoryURL(bootstrap.Launch.RepositoryURL) {
		if err := worker.PrepareScratchWorkspace(ctx, worker.ExecGitRunner{}, workspace); err != nil {
			return fmt.Errorf("prepare scratch workspace: %w", err)
		}
		logger.Info("initialized scratch workspace")
	} else if reuse.Reused {
		if err := prepareReusedWorkspace(ctx, logger, client, bootstrap, workspace, dataDir, publicURL, reuse); err != nil {
			return err
		}
	} else {
		checkoutGrant, err := client.checkoutGrant(ctx)
		if err != nil {
			if !anonymousCheckoutEnabled() {
				if errors.Is(err, errCheckoutForbidden) {
					logger.Error("checkout grant refused; session cannot start without a repository grant",
						"session_id", bootstrap.SessionID,
						"repository_url", bootstrap.Launch.RepositoryURL,
						"hint", "connect the repository through the GitHub App, or set AO_CLOUD_ALLOW_ANONYMOUS_GITHUB_CHECKOUT for a public repository",
					)
				}
				return fmt.Errorf("request checkout grant: %w", err)
			}
			checkoutGrant = worker.CheckoutGrantResponse{CloneURL: bootstrap.Launch.RepositoryURL}
			logger.Info("using anonymous public GitHub checkout")
		}
		if err := worker.PrepareCheckoutWithOptions(ctx, worker.ExecGitRunner{}, workspace, checkoutGrant,
			worker.CheckoutOptions{PartialClone: os.Getenv(worker.PartialCloneEnv) == "1"}); err != nil {
			return fmt.Errorf("prepare repository checkout: %w", err)
		}
		if err := worker.ConfigureWorkerGit(
			ctx, worker.ExecGitRunner{}, workspace, dataDir, publicURL,
			bootstrap.SessionID, bootstrap.Launch.Branch,
		); err != nil {
			return fmt.Errorf("configure repository tooling: %w", err)
		}
		// Multi-repo dev kit: clone any additional repositories alongside the
		// primary checkout. Non-fatal by design — an extra repo that cannot be
		// cloned (e.g. it is outside the session credential's GitHub App
		// installation) must never stop the session from starting on its primary
		// repo. Extra repos reuse the session's checkout-grant token, so they work
		// for repositories the installation can access; arbitrary private
		// third-party repos need per-repo grants (a follow-up).
		cloneExtraRepos(ctx, logger, checkoutGrant.Token, bootstrap.Launch.ExtraRepos, workspace, dataDir)
	}
	if err := worker.EnsureWorkspaceReviewBase(
		ctx, worker.ExecGitRunner{}, workspace, bootstrap.Launch.DefaultBranch,
	); err != nil {
		return fmt.Errorf("record workspace review base: %w", err)
	}
	return nil
}

// resolveReusedWorkspace decides whether the session works in the checkout
// the workspace template already cloned, logs why, and, when it does, scopes
// AO's git setup to this process tree (see worker.ReusedCheckoutGitEnvironment)
// so the checkout's and the user's git configuration stay untouched.
func resolveReusedWorkspace(
	ctx context.Context,
	logger *slog.Logger,
	bootstrap worker.BootstrapResponse,
	workspace, dataDir string,
) worker.ReuseDecision {
	home := strings.TrimSpace(os.Getenv("HOME"))
	decision := worker.ResolveReusedCheckout(
		ctx, worker.ExecGitRunner{}, home, workspace, dataDir,
		bootstrap.Launch.RepositoryURL, bootstrap.SessionID,
		bootstrap.Launch.Branch, bootstrap.Launch.DefaultBranch,
	)
	if !decision.Reused {
		logger.Info("checkout reuse: cloning into the AO workspace instead",
			"workspace", decision.Workspace, "reason", decision.Reason)
		return decision
	}
	logger.Info("checkout reuse: working in the template's existing checkout",
		"workspace", decision.Workspace, "adopted_now", decision.AdoptedNow, "stash", decision.Stash)
	for key, value := range worker.ReusedCheckoutGitEnvironment(dataDir) {
		_ = os.Setenv(key, value)
	}
	_ = os.Setenv("AO_WORKSPACE_DIR", decision.Workspace)
	return decision
}

// prepareReusedWorkspace refreshes an adopted checkout: it writes the session
// git tooling, fetches with the session's checkout grant, moves a just-adopted
// branch up to the fetched base, and clones any extra repositories beside it
// exactly as for a fresh checkout.
func prepareReusedWorkspace(
	ctx context.Context,
	logger *slog.Logger,
	client *client,
	bootstrap worker.BootstrapResponse,
	workspace, dataDir, publicURL string,
	reuse worker.ReuseDecision,
) error {
	checkoutGrant, err := client.checkoutGrant(ctx)
	if err != nil {
		if !anonymousCheckoutEnabled() {
			return fmt.Errorf("request checkout grant: %w", err)
		}
		checkoutGrant = worker.CheckoutGrantResponse{CloneURL: bootstrap.Launch.RepositoryURL}
	}
	if err := worker.ConfigureReusedCheckoutGit(dataDir, publicURL, bootstrap.SessionID); err != nil {
		return fmt.Errorf("configure repository tooling: %w", err)
	}
	if err := worker.PrepareReusedCheckout(ctx, worker.ExecGitRunner{}, workspace, checkoutGrant); err != nil {
		return fmt.Errorf("refresh the reused repository checkout: %w", err)
	}
	if reuse.AdoptedNow {
		if err := worker.AdvanceAdoptedCheckout(ctx, worker.ExecGitRunner{}, workspace, bootstrap.Launch.DefaultBranch); err != nil {
			logger.Warn("checkout reuse: could not fast-forward the session branch to the latest base", "error", err)
		}
	}
	cloneExtraRepos(ctx, logger, checkoutGrant.Token, bootstrap.Launch.ExtraRepos, workspace, dataDir)
	return nil
}

// cloneExtraRepos clones each additional dev-kit repository as a sibling of the
// primary checkout, so the agent (whose working directory is the primary repo)
// can reach it at ../<name>. It is best-effort: every failure is logged and
// skipped so the session always starts on its primary repo. The launcher
// (workerexec) computes the same paths via worker.ExtraRepoPath and lists them
// in the agent's system prompt.
func cloneExtraRepos(ctx context.Context, logger *slog.Logger, token string, repos []worker.RepoRef, workspace, dataDir string) {
	if len(repos) == 0 {
		return
	}
	parent := filepath.Dir(workspace)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		logger.Warn("multi-repo: cannot create extra-repos directory", "error", err)
		return
	}
	for _, repo := range repos {
		dest := worker.ExtraRepoPath(workspace, repo.URL)
		// Clone via worker.CloneExtraRepo, which uses the primary checkout's
		// askpass mechanism: the token stays in the command's environment and
		// never enters the URL, argv, or the repo's .git/config, and the repo is
		// wired to the session credential helper for the agent's own git ops.
		if err := worker.CloneExtraRepo(ctx, worker.ExecGitRunner{}, parent, dest, repo.URL, repo.Branch, token, dataDir); err != nil {
			logger.Warn("multi-repo: extra repo clone failed (non-fatal)", "repo", repo.URL, "error", err)
			continue
		}
		logger.Info("multi-repo: cloned extra repo", "repo", repo.URL, "path", dest)
	}
}

func startInteractiveAgent(
	ctx context.Context,
	logger *slog.Logger,
	client *client,
	bootstrap worker.BootstrapResponse,
	workspace, dataDir, pullRequestSocketPath, reviewSocketPath, checkpointSocketPath string,
	transportSupervisor *workertransport.Supervisor,
	rehydrateDone <-chan struct{},
) error {
	// Wait until the checkout has completed and any delete/restore rehydration
	// has run: the transcript must be on disk before the command is built, so
	// BuildInteractive detects the restored conversation and launches --resume.
	select {
	case <-ctx.Done():
		return nil
	case <-rehydrateDone:
	}
	if err := waitForHarness(ctx, logger, bootstrap.Launch.Harness); err != nil {
		if ctx.Err() == nil {
			logger.Warn("coding-agent harness unavailable", "error", err)
		}
		return nil
	}
	if strings.TrimSpace(bootstrap.Launch.Interface) == workertransport.InterfaceChat {
		return nil // The headless controller owns this worker until a TUI handoff.
	}
	if transportSupervisor.AgentCommandFactory == nil {
		return errors.New("coding-agent command factory is unavailable")
	}
	agentCommand, err := transportSupervisor.AgentCommandFactory(ctx, bootstrap.Launch.AgentSessionID, "", "")
	if err != nil {
		return fmt.Errorf("build interactive coding-agent command: %w", err)
	}
	runtimeEnv := map[string]string{
		"AO_CLOUD_WORKER_API_URL":    client.baseURL,
		"AO_CLOUD_WORKER_TOKEN_FILE": client.tokenFile,
		"AO_SESSION_ID":              bootstrap.SessionID,
		"AO_PROJECT_ID":              bootstrap.Launch.ProjectID,
		"AO_SESSION_KIND":            bootstrap.Launch.Kind,
		"AO_CHECKPOINT_SOCKET":       checkpointSocketPath,
		"AO_PULL_REQUEST_SOCKET":     pullRequestSocketPath,
		"AO_PULL_REQUEST_HELP": "curl --unix-socket $AO_PULL_REQUEST_SOCKET " +
			`-X POST http://localhost/pull-request -H 'Content-Type: application/json' ` +
			`-d '{"branch":"<pushed branch name>","title":"<PR title>","body":"<PR body>"}' ` +
			"to push the current branch and open a pull request against the repository's default branch.",
		"AO_REVIEW_SOCKET": reviewSocketPath,
		"AO_REVIEW_HELP": "curl --unix-socket $AO_REVIEW_SOCKET " +
			`-X POST http://localhost/review -H 'Content-Type: application/json' ` +
			`-d '{"reviewRunId":"<review run id from the prompt>","verdict":"approved|changes_requested","body":"<your findings>"}' ` +
			"to submit an AO-triggered review verdict.",
	}
	for key, value := range runtimeEnv {
		agentCommand.Env[key] = value
	}
	agentTerminal, err := client.ensureAgentTerminal(ctx)
	if err != nil {
		if agentCommand.Cleanup != nil {
			agentCommand.Cleanup()
		}
		return fmt.Errorf("initialize agent terminal: %w", err)
	}
	if err := transportSupervisor.StartAgent(ctx, agentCommand, agentTerminal); err != nil {
		return fmt.Errorf("start interactive coding-agent terminal: %w", err)
	}
	if err := client.publishEvent(ctx, "agent.ready", map[string]any{
		"workerId":     bootstrap.WorkerID,
		"epoch":        bootstrap.Epoch,
		"version":      workerVersion,
		"capabilities": workerCapabilities,
	}); err != nil {
		logger.Warn("publish agent.ready failed", "error", err)
	}
	return nil
}

var errStaleWorker = errors.New("worker credential replaced")

// errCheckoutForbidden marks a checkout grant the control plane refused. It is a
// permanent configuration fault (the session has no repository grant, e.g. a
// private repository not connected through the GitHub App), not a transient
// error, so retrying the worker cannot make progress.
var errCheckoutForbidden = errors.New("checkout grant not authorized")

type client struct {
	baseURL   string
	http      *http.Client
	tokenFile string
	mu        sync.RWMutex
	token     string
	// Startup lookups made beside the checkout (see startStartupPrefetch).
	// Set before any goroutine that reads them starts; nil means none.
	credentialPrefetch *prefetch[worker.CredentialResponse]
	transcriptPrefetch *prefetch[transcriptLookup]
	grantPrefetch      *prefetch[worker.CheckoutGrantResponse]
	// busy reports sandbox activity for the next heartbeat; set once the
	// transport supervisor exists.
	busy atomic.Pointer[func() bool]
	// busyAccepted records that the control plane's last heartbeat response
	// advertised worker.HeartbeatFeatureBusy. Until then (and after a
	// control-plane rollback) the field is not sent: an older control plane
	// rejects unknown heartbeat fields.
	busyAccepted atomic.Bool
}

// startStartupPrefetch begins the lookups the coding agent's launch needs but
// the checkout does not: the captured transcript (read by rehydration) and the
// coding-agent credential. They run while the repository is fetched, so once
// the checkout gate opens the agent launches without two more round trips. The
// gate itself is unchanged: the agent still starts only after checkout and
// rehydration.
//
// The checkout grant is requested here too, so the checkout does not wait for
// the transport to start and worker.ready to post before asking for it.
func (c *client) startStartupPrefetch(ctx context.Context, launch worker.LaunchContext) {
	if launch.RepositoryURL != "" && !worker.IsScratchRepositoryURL(launch.RepositoryURL) {
		c.grantPrefetch = startPrefetch(ctx, prefetchMaxAge, c.fetchCheckoutGrant)
	}
	c.transcriptPrefetch = startPrefetch(ctx, prefetchMaxAge, func(ctx context.Context) (transcriptLookup, error) {
		checkpoint, found, err := c.fetchTranscript(ctx)
		return transcriptLookup{checkpoint: checkpoint, found: found}, err
	})
	// Only a terminal (TUI) launch builds the agent right after the gate; a chat
	// session asks for the credential on its first turn, likely past maxAge.
	if verifyHarnessAvailable(launch.Harness) == nil &&
		strings.TrimSpace(launch.Interface) != workertransport.InterfaceChat {
		c.credentialPrefetch = startPrefetch(ctx, prefetchMaxAge, func(ctx context.Context) (worker.CredentialResponse, error) {
			return c.agentCredential(ctx, "")
		})
	}
}

func (c *client) bootstrap(ctx context.Context, bootstrapToken string) (worker.BootstrapResponse, error) {
	var response worker.BootstrapResponse
	err := c.do(ctx, "/worker/bootstrap", worker.BootstrapRequest{
		BootstrapToken: bootstrapToken,
		Version:        workerVersion,
		Capabilities:   workerCapabilities,
	}, &response)
	if err != nil {
		return worker.BootstrapResponse{}, err
	}
	if response.WorkerToken == "" {
		return worker.BootstrapResponse{}, errors.New("control plane returned no worker token")
	}
	return response, nil
}

// connect establishes the worker's live credential. It prefers a persisted
// token so a rebooted or crash-restarted worker reconnects without a fresh
// ticket — the single-use bootstrap ticket baked into the sandbox environment is
// only spent to register a genuinely new sandbox. It falls back to redeeming the
// ticket when no valid token exists.
func (c *client) connect(
	ctx context.Context, logger *slog.Logger, bootstrapToken string,
) (worker.BootstrapResponse, error) {
	if persisted := c.loadPersistedToken(); persisted != "" {
		c.mu.Lock()
		c.token = persisted
		c.mu.Unlock()
		if renewed, err := c.heartbeat(ctx); err == nil {
			if err := c.setToken(renewed); err != nil {
				return worker.BootstrapResponse{}, err
			}
			resp, err := c.reconnect(ctx)
			if err == nil {
				logger.Info("worker reconnected with a persisted credential",
					"session_id", resp.SessionID, "worker_id", resp.WorkerID, "epoch", resp.Epoch)
				return resp, nil
			}
			logger.Warn("reconnect after heartbeat failed; bootstrapping fresh", "error", err)
		} else {
			logger.Info("persisted worker credential is no longer valid; bootstrapping fresh", "error", err)
		}
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
	}
	if strings.TrimSpace(bootstrapToken) == "" {
		return worker.BootstrapResponse{}, errors.New(
			"no valid worker credential and AO_WORKER_BOOTSTRAP_TOKEN is required")
	}
	resp, err := c.bootstrapWithRetry(ctx, logger, bootstrapToken)
	if err != nil {
		return worker.BootstrapResponse{}, fmt.Errorf("bootstrap: %w", err)
	}
	if err := c.setToken(resp.WorkerToken); err != nil {
		return worker.BootstrapResponse{}, err
	}
	return resp, nil
}

// bootstrapAttempts bounds how often a worker retries its first request when
// the network drops it (about 3.75s of backoff in all). Without it, one dropped
// connection at startup strands the session until the control plane's startup
// deadline replaces the sandbox.
const bootstrapAttempts = 5

// bootstrapWithRetry retries only transport failures. An HTTP error status is
// returned at once: the ticket is single-use, so a request the control plane
// already answered must not be replayed. A transport failure after the ticket
// was spent is answered on retry with a clean rejection, no worse than failing.
func (c *client) bootstrapWithRetry(
	ctx context.Context, logger *slog.Logger, bootstrapToken string,
) (worker.BootstrapResponse, error) {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		resp, err := c.bootstrap(ctx, bootstrapToken)
		var transport *url.Error
		if err == nil || !errors.As(err, &transport) || attempt == bootstrapAttempts {
			return resp, err
		}
		logger.Warn("worker bootstrap request failed; retrying", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return resp, err
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *client) loadPersistedToken() string {
	if c.tokenFile == "" {
		return ""
	}
	data, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// reconnect fetches the durable launch context for a worker that re-presented a
// persisted token, so a restart does not need to redeem a bootstrap ticket.
func (c *client) reconnect(ctx context.Context) (worker.BootstrapResponse, error) {
	var response worker.BootstrapResponse
	if err := c.doMethod(ctx, http.MethodGet, "/worker/reconnect", nil, &response); err != nil {
		return worker.BootstrapResponse{}, err
	}
	if response.SessionID == "" {
		return worker.BootstrapResponse{}, errors.New("control plane returned no session on reconnect")
	}
	return response, nil
}

func (c *client) heartbeat(ctx context.Context) (string, error) {
	request := worker.HeartbeatRequest{
		Version:      workerVersion,
		Capabilities: workerCapabilities,
	}
	if c.busyAccepted.Load() {
		if probe := c.busy.Load(); probe != nil {
			request.Busy = (*probe)()
		}
	}
	var response worker.HeartbeatResponse
	err := c.do(ctx, "/worker/heartbeat", request, &response)
	if err != nil && request.Busy && rejectedUnknownField(err) {
		// The control plane was rolled back to a release that rejects the
		// field. Drop it and retry once; the next response says whether to
		// send it again.
		c.busyAccepted.Store(false)
		request.Busy = false
		response = worker.HeartbeatResponse{}
		err = c.do(ctx, "/worker/heartbeat", request, &response)
	}
	if err != nil {
		return "", err
	}
	c.busyAccepted.Store(slices.Contains(response.Features, worker.HeartbeatFeatureBusy))
	if response.WorkerToken == "" {
		return "", errors.New("control plane returned no renewed worker token")
	}
	return response.WorkerToken, nil
}

// rejectedUnknownField reports a 400 from a control plane that refused a
// request field it does not know (its decoder disallows unknown fields).
func rejectedUnknownField(err error) bool {
	message := err.Error()
	return strings.Contains(message, " returned 400") && strings.Contains(message, "unknown field")
}

func (c *client) heartbeatLoop(ctx context.Context, logger *slog.Logger) error {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			renewed, err := c.heartbeat(ctx)
			if errors.Is(err, errStaleWorker) {
				return errors.New("worker credential was replaced; a newer worker owns this session")
			}
			if err != nil {
				failures++
				logger.Warn("heartbeat failed", "error", err, "consecutive_failures", failures)
				if failures >= maxHeartbeatFailures {
					return fmt.Errorf("heartbeat failed %d times: %w", failures, err)
				}
				continue
			}
			failures = 0
			if err := c.setToken(renewed); err != nil {
				return err
			}
		}
	}
}

func (c *client) ClaimTurn(ctx context.Context) (*worker.Turn, error) {
	var response worker.ClaimTurnResponse
	if err := c.do(ctx, "/worker/turns/claim", worker.ClaimTurnRequest{}, &response); err != nil {
		return nil, err
	}
	return response.Turn, nil
}

func (c *client) CreateChatApproval(ctx context.Context, request worker.ChatApproval) error {
	return c.do(ctx, "/worker/turns/"+url.PathEscape(request.TurnID)+"/approvals", request, nil)
}

func (c *client) PublishTurnCapabilities(ctx context.Context, turnID string, attempt int, steering bool) error {
	return c.do(ctx, "/worker/turns/"+url.PathEscape(turnID)+"/capabilities", map[string]any{
		"attempt": attempt, "steering": steering,
	}, nil)
}

func (c *client) ChatApprovalDecision(ctx context.Context, turnID string, attempt int, requestID string) (string, error) {
	var response struct {
		Decision string `json:"decision"`
	}
	path := "/worker/turns/" + url.PathEscape(turnID) + "/approvals/" + url.PathEscape(requestID) + "?attempt=" + strconv.Itoa(attempt)
	if err := c.doMethod(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", err
	}
	return response.Decision, nil
}

func (c *client) AgentSessionID(ctx context.Context) (string, error) {
	var response struct {
		AgentSessionID string `json:"agentSessionId"`
	}
	if err := c.doMethod(ctx, http.MethodGet, "/worker/session", nil, &response); err != nil {
		return "", err
	}
	return response.AgentSessionID, nil
}

func (c *client) EnsureAgentTerminal(ctx context.Context) (worker.AgentTerminalResponse, error) {
	return c.ensureAgentTerminal(ctx)
}

func (c *client) Credential(ctx context.Context) (worker.CredentialResponse, error) {
	if credential, ok := c.credentialPrefetch.take(ctx); ok {
		return credential, nil
	}
	return c.agentCredential(ctx, "")
}

func (c *client) reviewCredential(ctx context.Context, reviewRunID string) (worker.CredentialResponse, error) {
	return c.agentCredential(ctx, "?reviewRunId="+url.QueryEscape(reviewRunID))
}

func (c *client) agentCredential(ctx context.Context, query string) (worker.CredentialResponse, error) {
	var response worker.CredentialResponse
	err := c.doMethod(ctx, http.MethodGet, "/worker/credential"+query, nil, &response)
	if err != nil {
		return worker.CredentialResponse{}, err
	}
	if response.Provider == "" || response.CredentialType == "" || response.Secret == "" {
		return worker.CredentialResponse{}, errors.New("control plane returned an incomplete coding-agent credential")
	}
	return response, nil
}

// grantReuseMargin is how long a prefetched checkout token must stay valid to
// be used; a shorter-lived one is replaced so the fetch cannot outlive it.
const grantReuseMargin = 2 * time.Minute

func (c *client) checkoutGrant(ctx context.Context) (worker.CheckoutGrantResponse, error) {
	if grant, ok := c.grantPrefetch.take(ctx); ok &&
		(grant.Token == "" || time.Until(grant.ExpiresAt) > grantReuseMargin) {
		return grant, nil
	}
	return c.fetchCheckoutGrant(ctx)
}

func (c *client) fetchCheckoutGrant(ctx context.Context) (worker.CheckoutGrantResponse, error) {
	var response worker.CheckoutGrantResponse
	if err := c.do(ctx, "/worker/checkout-grant", struct{}{}, &response); err != nil {
		return worker.CheckoutGrantResponse{}, err
	}
	if response.CloneURL == "" ||
		(response.Token != "" && !response.ExpiresAt.After(time.Now())) {
		return worker.CheckoutGrantResponse{}, errors.New("control plane returned an invalid checkout grant")
	}
	return response, nil
}

func (c *client) pushGrant(ctx context.Context) (worker.CheckoutGrantResponse, error) {
	var response worker.CheckoutGrantResponse
	if err := c.do(ctx, "/worker/push-grant", struct{}{}, &response); err != nil {
		return worker.CheckoutGrantResponse{}, err
	}
	if response.CloneURL == "" || response.Token == "" || !response.ExpiresAt.After(time.Now()) {
		return worker.CheckoutGrantResponse{}, errors.New("control plane returned an invalid push grant")
	}
	return response, nil
}

func (c *client) raisePullRequest(
	ctx context.Context,
	input worker.RaisePullRequestRequest,
) (worker.RaisePullRequestResponse, error) {
	var response worker.RaisePullRequestResponse
	if err := c.do(ctx, "/worker/pull-requests", input, &response); err != nil {
		return worker.RaisePullRequestResponse{}, err
	}
	if response.HTMLURL == "" || response.Number <= 0 {
		return worker.RaisePullRequestResponse{}, errors.New("control plane returned an incomplete pull request response")
	}
	return response, nil
}

func (c *client) claimPullRequest(ctx context.Context, reference string) error {
	var response worker.ClaimPullRequestResponse
	if err := c.do(ctx, "/worker/pull-requests/claim", worker.ClaimPullRequestRequest{Reference: reference}, &response); err != nil {
		return err
	}
	if response.ID == "" {
		return errors.New("control plane did not confirm pull request claim")
	}
	return nil
}

func (c *client) reportGitRefs(ctx context.Context, workspace string) error {
	refs, err := localGitRefs(ctx, workspace)
	if err != nil {
		return err
	}
	return c.do(ctx, "/worker/pull-requests/refs", worker.ReportGitRefsRequest{Refs: refs}, nil)
}

func localGitRefs(ctx context.Context, workspace string) ([]worker.GitRef, error) {
	output, err := exec.CommandContext(ctx, "git", "-C", workspace, "for-each-ref", "--sort=-committerdate", "--count=128", "--format=%(refname:short)%09%(objectname)", "refs/heads").Output()
	if err != nil {
		return nil, err
	}
	refs := make([]worker.GitRef, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		branch, sha, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, errors.New("git returned a malformed branch head")
		}
		refs = append(refs, worker.GitRef{Branch: branch, SHA: sha})
	}
	return refs, nil
}

func (c *client) submitReview(
	ctx context.Context,
	reviewRunID string,
	input worker.SubmitReviewRequest,
) (worker.SubmitReviewResponse, error) {
	var response worker.SubmitReviewResponse
	if err := c.do(ctx, "/worker/reviews/"+url.PathEscape(reviewRunID)+"/submit", input, &response); err != nil {
		return worker.SubmitReviewResponse{}, err
	}
	if response.ID == "" {
		return worker.SubmitReviewResponse{}, errors.New("control plane returned an incomplete review response")
	}
	return response, nil
}

func anonymousCheckoutEnabled() bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(
		os.Getenv("AO_CLOUD_ALLOW_ANONYMOUS_GITHUB_CHECKOUT"),
	))
	return err == nil && enabled
}

// harnessWaitInterval and harnessWaitLimit pace and bound how long the worker
// waits for a supported coding-agent harness binary that is not installed yet,
// for a template that installs its tools after the Coder agent reports ready.
// The limit matches the default bring-your-own Coder startup budget.
var (
	harnessWaitInterval = 10 * time.Second
	harnessWaitLimit    = 20 * time.Minute
)

// waitForHarness returns nil once the harness binary is on PATH, checking
// again every harnessWaitInterval for at most harnessWaitLimit. It returns the
// availability error at once for an unknown harness, and the last one when the
// binary never appears.
func waitForHarness(ctx context.Context, logger *slog.Logger, harness string) error {
	err := verifyHarnessAvailable(harness)
	if err == nil {
		return nil
	}
	if _, supported := workerexec.SupportedHarness(harness); !supported {
		return err
	}
	logger.Info("coding-agent harness not installed yet; checking again", "harness", harness,
		"interval", harnessWaitInterval.String(), "limit", harnessWaitLimit.String())
	deadline := time.NewTimer(harnessWaitLimit)
	defer deadline.Stop()
	ticker := time.NewTicker(harnessWaitInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return err
		case <-ticker.C:
			if err = verifyHarnessAvailable(harness); err == nil {
				logger.Info("coding-agent harness found; starting the agent", "harness", harness)
				return nil
			}
		}
	}
}

func verifyHarnessAvailable(harness string) error {
	// Derive the expected binary from the harness registry (the same source
	// BuildInteractive launches from) so every registered harness is gated
	// consistently. A hardcoded switch here silently skipped opencode, leaving its
	// sessions with no agent terminal (no TUI).
	binary, ok := workerexec.SupportedHarness(harness)
	if !ok {
		return fmt.Errorf("unsupported coding-agent harness %q", harness)
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("%s harness binary %q is unavailable: %w", harness, binary, err)
	}
	return nil
}

func (c *client) PublishOutput(ctx context.Context, output worker.OutputEvent) error {
	if output.Activity != nil {
		return c.publishEvent(ctx, "chat.activity", output)
	}
	return c.publishEvent(ctx, "chat.assistant_delta", output)
}

func (c *client) PublishActivity(ctx context.Context, activity worker.ActivityEvent) error {
	return c.publishEvent(ctx, "agent.activity", activity)
}

func (c *client) ClaimTransport(ctx context.Context) (*worker.TransportRequest, error) {
	var response worker.ClaimTransportResponse
	if err := c.do(ctx, "/worker/transport/claim", struct{}{}, &response); err != nil {
		return nil, err
	}
	return response.Request, nil
}

// WaitForWork long-polls the control plane until a turn or transport request is
// enqueued for this session (or a short server-side timeout), replacing the old
// ~100ms busy-poll of the claim routes. It returns no work; the caller re-runs
// the claim RPCs. An older control plane without the endpoint returns an error,
// which the transport supervisor treats as a bounded back-off.
func (c *client) WaitForWork(ctx context.Context) error {
	return c.doMethod(ctx, http.MethodGet, "/worker/work/wait", nil, nil)
}

func (c *client) CompleteTransport(
	ctx context.Context,
	requestID string,
	attempt int,
	result any,
) error {
	return c.do(
		ctx,
		"/worker/transport/"+url.PathEscape(requestID)+"/complete",
		worker.CompleteTransportRequest{Attempt: attempt, Response: result},
		nil,
	)
}

func (c *client) FailTransport(
	ctx context.Context,
	requestID string,
	attempt int,
	code, message string,
) error {
	return c.do(
		ctx,
		"/worker/transport/"+url.PathEscape(requestID)+"/fail",
		worker.FailTransportRequest{Attempt: attempt, Code: code, Message: message},
		nil,
	)
}

func (c *client) PublishTerminalOutput(
	ctx context.Context,
	terminalID string,
	id int64,
	data []byte,
) error {
	return c.do(
		ctx,
		"/worker/terminals/"+url.PathEscape(terminalID)+"/output",
		worker.TerminalOutputRequest{ID: id, Data: data},
		nil,
	)
}

// DialTerminalStream opens the persistent duplex terminal stream. The token
// is presented at dial time; the socket then lives until the control plane
// retires it (worker epoch bump or terminal close).
func (c *client) DialTerminalStream(
	ctx context.Context,
	terminalID string,
) (*websocket.Conn, error) {
	streamURL := c.baseURL + "/worker/terminals/" + url.PathEscape(terminalID) + "/stream"
	if strings.HasPrefix(streamURL, "http") {
		streamURL = "ws" + strings.TrimPrefix(streamURL, "http")
	}
	header := http.Header{}
	if token := c.currentToken(); token != "" {
		header.Set("Authorization", "Worker "+token)
	}
	conn, _, err := websocket.Dial(ctx, streamURL, &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: header,
	})
	return conn, err
}

func (c *client) ensureAgentTerminal(
	ctx context.Context,
) (worker.AgentTerminalResponse, error) {
	var response worker.AgentTerminalResponse
	err := c.do(ctx, "/worker/terminals/agent", struct{}{}, &response)
	if err != nil {
		return worker.AgentTerminalResponse{}, err
	}
	if response.TerminalID == "" {
		return worker.AgentTerminalResponse{}, errors.New("control plane returned no agent terminal")
	}
	return response, nil
}

func (c *client) PublishTerminalExit(
	ctx context.Context,
	terminalID string,
	exitCode int,
	interfaceHandoff bool,
) error {
	return c.do(
		ctx,
		"/worker/terminals/"+url.PathEscape(terminalID)+"/exit",
		worker.TerminalExitRequest{
			ExitCode:         exitCode,
			InterfaceHandoff: interfaceHandoff,
		},
		nil,
	)
}

func (c *client) CancellationRequested(
	ctx context.Context,
	turnID string,
	attempt int,
) (bool, error) {
	var response worker.CancellationResponse
	path := "/worker/turns/" + url.PathEscape(turnID) +
		"/cancellation?attempt=" + url.QueryEscape(fmt.Sprint(attempt))
	if err := c.doMethod(ctx, http.MethodGet, path, nil, &response); err != nil {
		return false, err
	}
	return response.Requested, nil
}

func (c *client) CompleteTurn(
	ctx context.Context,
	turnID string,
	attempt int,
	cancelled bool,
) error {
	return c.do(
		ctx,
		"/worker/turns/"+url.PathEscape(turnID)+"/complete",
		worker.FinishTurnRequest{Attempt: attempt, Cancelled: cancelled},
		nil,
	)
}

func (c *client) FailTurn(
	ctx context.Context,
	turnID string,
	attempt int,
	message string,
) error {
	return c.do(
		ctx,
		"/worker/turns/"+url.PathEscape(turnID)+"/fail",
		worker.FailTurnRequest{Attempt: attempt, Error: message},
		nil,
	)
}

func (c *client) publishEvent(ctx context.Context, eventType string, payload any) error {
	return c.do(ctx, "/worker/events", worker.EventRequest{Type: eventType, Payload: payload}, nil)
}

func (c *client) publishNotification(ctx context.Context, event notificationoutbox.Event) error {
	var response worker.NotificationEventResponse
	if err := c.do(ctx, "/worker/notification-events", worker.NotificationEventRequest{
		EventID: event.EventID, Type: event.EventType, OccurredAt: event.OccurredAt, Payload: event.Payload,
	}, &response); err != nil {
		return err
	}
	if !response.Accepted || response.EventID != event.EventID {
		return errors.New("control plane returned an invalid notification acknowledgement")
	}
	return nil
}

func (c *client) do(ctx context.Context, path string, body any, out any) error {
	return c.doMethod(ctx, http.MethodPost, path, body, out)
}

func (c *client) doMethod(
	ctx context.Context,
	method, path string,
	body any,
	out any,
) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", path, err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token := c.currentToken(); token != "" {
		request.Header.Set("Authorization", "Worker "+token)
		request.Header.Set(worker.CapabilitiesHeader, strings.Join(workerCapabilities, ","))
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusUnauthorized &&
			bytes.Contains(snippet, []byte("STALE_WORKER_TOKEN")) {
			return errStaleWorker
		}
		if response.StatusCode == http.StatusForbidden &&
			bytes.Contains(snippet, []byte("CHECKOUT_NOT_AUTHORIZED")) {
			return errCheckoutForbidden
		}
		return fmt.Errorf("%s returned %d: %s", path, response.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

func (c *client) setToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("control plane returned an empty worker token")
	}
	// Held across the file write: the first heartbeat runs beside startup, so
	// two renewals may persist at once and would otherwise share the temp file.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokenFile != "" {
		temporary := c.tokenFile + ".tmp"
		if err := os.WriteFile(temporary, []byte(token), 0o600); err != nil {
			return fmt.Errorf("write rotating worker credential: %w", err)
		}
		if err := os.Rename(temporary, c.tokenFile); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace rotating worker credential: %w", err)
		}
	}
	c.token = token
	return nil
}

func (c *client) currentToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// workingDirectoryRetryExitCode is how the worker exits when a session that
// failed on its configured working directory is reopened, so a fresh worker
// resolves the directory again (EX_TEMPFAIL).
const workingDirectoryRetryExitCode = 75

// secretFileGuardCommand is the hook command that runs this binary's
// secret-file guard. A self-updated worker re-execs from its healed copy, so
// os.Executable names the binary that is actually current. Without it the
// Read/Edit deny rules still apply; only the shell guard is skipped.
func secretFileGuardCommand(logger *slog.Logger) string {
	self, err := os.Executable()
	if err != nil {
		logger.Warn("resolve worker executable for the secret file guard", "error", err)
		return ""
	}
	if strings.ContainsAny(self, " \t\n\"'\\$`") {
		logger.Warn("worker executable path cannot be used as a hook command", "path", self)
		return ""
	}
	return self + " " + workerexec.SecretFileGuardArg
}
