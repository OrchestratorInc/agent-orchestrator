package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	claudecodeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agentaccount"
	chatdriveracp "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/dockeragentacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	chatdriverregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/cuadriver"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/systemexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry/policyauthority"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/autoreview"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
	"github.com/aoagents/agent-orchestrator/backend/internal/codexops"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemon/supervisor"
	"github.com/aoagents/agent-orchestrator/backend/internal/datadirlock"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecyclelog"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/notify"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	usagepipeline "github.com/aoagents/agent-orchestrator/backend/internal/observe/usage"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/presence"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/previewserver"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
	"github.com/aoagents/agent-orchestrator/backend/internal/push"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
	awakesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/awake"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/computeruse"
	cuesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/cue"
	devimportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/devimport"
	fsbrowsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/fsbrowser"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/githubpat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/hookprocess"
	importsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/importer"
	linkpreviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/linkpreview"
	notificationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/notification"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/panes"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellagent"
	shelltermsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systemcheck"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/terminaltitle"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

const usageReconcileTick = 3 * time.Minute

const chatHibernateSweepTick = 30 * time.Second

const workspaceCleanupSweepTick = 10 * time.Minute

func sentryEnvironment(version string) string {
	v := strings.ToLower(strings.TrimSpace(version))
	switch {
	case v == "":
		return "unknown"
	case strings.Contains(v, "nightly"):
		return "nightly"
	case strings.Contains(v, "edge") || strings.Contains(v, "pr"):
		return "development"
	default:
		return "stable"
	}
}

var publishedVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-nightly\.\d{12})?$`)

func publishedSentryDSN(dsn, version string) string {
	if publishedVersion.MatchString(strings.TrimSpace(version)) {
		return dsn
	}
	return ""
}

var _ interface {
	sessionmanager.TerminalInputGate
	sessionmanager.TerminalInputObserver
	sessionmanager.TerminalInputFencer
	sessionmanager.TerminalInputForgetter
} = (*terminal.Manager)(nil)

func daemonShutdownContext() (ctx context.Context, cancelWorkers, stopSignals context.CancelFunc) {
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	ctx, cancelWorkers = context.WithCancel(signalCtx)
	return ctx, cancelWorkers, stopSignals
}

func Run(buildVersion string) (retErr error) {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Telemetry.AppVersion = telemetryAppVersion(cfg.Telemetry.AppVersion, buildVersion)
	config.ScrubTelemetryEnv()
	_ = os.Unsetenv(caller.ShellTerminalEnv)
	if cwd, err := os.Getwd(); err == nil {
		cfg.StartupWorkingDirectory = cwd
	}
	if err := stabilizeWorkingDirectory(cfg.DataDir); err != nil {
		return err
	}
	ignoreBrokenPipeSignal()

	logRoot := cfg.DataDir
	if cfg.RunFilePath != "" {
		logRoot = filepath.Dir(cfg.RunFilePath)
	}
	lifecycle := lifecyclelog.Open(logRoot, cfg.Telemetry.AppVersion)
	startedAt := time.Now()
	startFields := lifecyclelog.ProcessContext()
	startFields["owner"] = os.Getenv("AO_OWNER")
	startFields["port"] = cfg.Port
	if err := lifecycle.InstallCrashOutput(); err != nil {
		startFields["crashOutput"] = "unavailable"
	}
	lifecycle.Record("daemon_start", startFields)
	defer func() {
		fields := map[string]any{"uptimeMs": time.Since(startedAt).Milliseconds()}
		if recovered := recover(); recovered != nil {
			fields["panic"] = true
			lifecycle.RecordError("daemon_exit", fmt.Errorf("panic: %v", recovered), fields)
			panic(recovered)
		}
		if retErr != nil {
			lifecycle.RecordError("daemon_exit", retErr, fields)
			return
		}
		lifecycle.Record("daemon_exit", fields)
	}()

	log, openLogFile := newDaemonLogger()
	var browserRuntimeToken string
	if os.Getenv(browserruntime.RuntimeTokenStdinEnv) == "1" {
		browserRuntimeToken, err = browserruntime.ReadRuntimeToken(os.Stdin)
		if err != nil {
			return err
		}
	}
	if browserRuntimeToken == "" {
		browserRuntimeToken, err = browserruntime.NewToken()
		if err != nil {
			return err
		}
	}
	browserAuthority := browsersvc.NewAuthority()
	browserBroker := browserruntime.New(log, browserRuntimeToken)
	chrome := newChromeMode(cfg, log)
	var browserLifecycle sessionmanager.BrowserLifecycle = browserBroker
	if chrome != nil {
		browserLifecycle = chrome.lifecycle(browserBroker)
	}
	browserStreamBroker := browserstream.New(log, browserRuntimeToken)

	if live, err := runfile.CheckStale(cfg.RunFilePath); err != nil {
		return fmt.Errorf("inspect run-file: %w", err)
	} else if live != nil && runFileOwnerServing(&http.Client{Timeout: staleProbeTimeout}, config.LoopbackHost, live) {
		return fmt.Errorf("daemon already running (pid %d, port %d); refusing to start", live.PID, live.Port)
	}

	installerShutdownTimeout := max(cfg.ShutdownTimeout, cmp.Or(cfg.InstallerShutdownTimeout, systeminstall.DefaultShutdownTimeout()))
	dataDirLock, err := lockDataDir(context.Background(), cfg.DataDir, dataDirTakeoverWait(cfg.ShutdownTimeout, installerShutdownTimeout), log)
	if err != nil {
		return err
	}
	openLogFile(cfg.DataDir)
	defer func() { _ = dataDirLock.Release() }()

	store, err := sqlite.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.RequeueClaimedReports(context.Background()); err != nil {
		return fmt.Errorf("recover report delivery claims: %w", err)
	}
	if err := skillassets.Install(cfg.DataDir); err != nil {
		log.Warn("install AO skills", "err", err)
	}
	if err := agentlaunch.UseStableExecutable(cfg.DataDir, os.Getenv); err != nil {
		log.Warn("link a stable ao for sessions of this AppImage", "err", err)
	}

	telemetryPolicy := policyauthority.New(filepath.Join(cfg.DataDir, policyauthority.FileName))
	sentryobs.SetPolicyCheck(func(ctx context.Context) bool {
		return telemetryPolicy.EventsAllowed(ctx, cfg.Telemetry.Events, cfg.Telemetry.EventsExplicit)
	})
	defer sentryobs.SetPolicyCheck(nil)
	telemetrySink, telemetryIdentity, remoteTelemetryActive := newTelemetrySink(cfg, store, log)
	configureUsageRecording(cfg, store, telemetryIdentity)
	recordSlowWriteLocks(store, telemetrySink)
	defer func() { _ = telemetrySink.Close(context.Background()) }()
	installID := ""
	if telemetryIdentity != nil {
		installID = telemetryIdentity.Snapshot().InstallID
	}
	if err := sentryobs.Init(sentryobs.Config{
		DSN:         publishedSentryDSN(cfg.Telemetry.SentryDSN, cfg.Telemetry.AppVersion),
		InstallID:   installID,
		Release:     cfg.Telemetry.AppVersion,
		Environment: sentryEnvironment(cfg.Telemetry.AppVersion),
	}); err != nil {
		log.Warn("daemon sentry disabled", "err", err)
	}
	defer sentryobs.Flush(2 * time.Second)
	telemetrySink.Emit(context.Background(), ports.TelemetryEvent{
		Name:       "ao.daemon.started",
		Source:     "daemon",
		OccurredAt: time.Now().UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload: map[string]any{
			"port":  cfg.Port,
			"agent": cfg.Agent,
		},
	})
	emitInstallLifecycleEvents(cfg, telemetryIdentity, telemetrySink, storeHasPriorState(store), log)

	ctx, stop, stopSignals := daemonShutdownContext()
	defer stopSignals()
	defer stop()
	var usage *usageTelemetryFlusher
	if remoteTelemetryActive && telemetryIdentity != nil {
		usage = startUsageTelemetry(ctx, store, telemetrySink, telemetryIdentity, installMilestones(cfg), log)
		defer func() {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			usage.flush(flushCtx, false)
		}()
	}
	startGitHubLoginResolver(ctx, cfg, telemetryIdentity, log)
	cdcPipe, err := startCDC(ctx, store, log)
	if err != nil {
		return err
	}

	runtimeAdapter := runtimeselect.New(log, cfg.RunFilePath)
	managedPreview := previewserver.New(log, cfg.DataDir)
	termMgr := terminal.NewManager(runtimeAdapter, cdcPipe.Broadcaster, log)
	paneBroker := panes.NewBroker()
	termMgr.SetPanes(paneSource{paneBroker})
	defer termMgr.Close()

	lifecycleMessenger := newModeAwareMessenger()
	notificationHub := notify.NewHub()
	notificationBarrier := &sync.Mutex{}
	notifier := notificationsvc.New(notificationsvc.Deps{
		Store: store, Publisher: notificationHub, Barrier: notificationBarrier, Logger: log,
	})
	notificationWriter := notify.New(notify.Deps{Store: store, Publisher: notificationHub, Barrier: notificationBarrier})
	if err := notificationWriter.Reconcile(ctx); err != nil {
		log.Warn("notification resolution reconcile failed", "err", err)
	}

	defaultAgent := cfg.Agent
	if defaultAgent == "" {
		defaultAgent = config.DefaultAgent
	}
	agents, err := buildAgentResolver(defaultAgent, log)
	if err != nil {
		stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("wire agent resolver: %w", err)
	}
	messenger := newSessionMessenger(store, runtimeAdapter, log, agents)

	lcStack := startLifecycle(ctx, cfg.DataDir, store, runtimeAdapter, lifecycleMessenger, notificationWriter, telemetrySink, agents, log)
	lcStack.LCM.DeliveryTelemetry().SetInstallID(func() string {
		if telemetryIdentity == nil || telemetryIdentity.OptedOut() || telemetryIdentity.ExportSuppressed() {
			return ""
		}
		return telemetryIdentity.Snapshot().InstallID
	})

	var agentSvc *agentsvc.Service
	var agentAccountSvc *agentaccounts.Service
	chatDrivers := chatdriverregistry.Build(log, func() {
		if agentSvc == nil {
			return
		}
		agentSvc.InvalidateAgentAuthentication(string(domain.HarnessClaudeCode))
		agentSvc.RecheckAgent(string(domain.HarnessClaudeCode))
	})

	settingsSvc := settingssvc.New(
		settingsStore{store: store},
		chatDrivers,
		settingssvc.OfferingFromConfig(cfg),
		func() time.Time { return time.Now().UTC() },
	)

	var sessMgr sessionLifecycle
	recheckSessionAccount := func(sessionID domain.SessionID, harness domain.AgentHarness) {
		if accounts := agentAccountSvc; accounts != nil {
			go func() {
				defer sentryobs.RecoverBackground()
				accounts.RecheckSessionAccount(ctx, sessionID, harness)
			}()
		}
	}
	reviewRefresher := &scmReviewRefresher{}
	chatSvc := chatsvc.New(chatsvc.Options{
		PRChanged:           reviewRefresher.RequestSessionRefresh,
		Store:               store,
		Sessions:            store,
		Renders:             attachmentstore.New(cfg.DataDir),
		DataDir:             cfg.DataDir,
		ReconcileOutputType: lcStack.LCM.ReconcileSessionOutputType,
		HibernationEnabled:  settingsSvc.ChatHibernationEnabled,
		VersionRefresh: func() (time.Duration, bool) {
			return cfg.VersionRefreshIdle, cfg.VersionRefresh && settingsSvc.VersionRefreshEnabled(ctx)
		},
		AppRunID: cfg.AppRunID,
		StopProviderHost: func(ctx context.Context, id domain.SessionID) error {
			return persistenthost.Shutdown(ctx, cfg.DataDir, string(id))
		},
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := store.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation:                     rows.Conversation,
				ActiveBranch:                     rows.ActiveBranch,
				EditFloorSequence:                rows.EditFloorSequence,
				NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
				Turns:                            rows.Turns,
				Messages:                         rows.Messages,
				Activities:                       rows.Activities,
				BranchPoints:                     rows.BranchPoints,
				BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
				BatchedDeliveries:                rows.BatchedDeliveries,
			}, nil
		}),
		PageReader: chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, conversationID string, beforeSequence, afterSequence, limit int64) (chatsvc.ConversationRows, error) {
			rows, err := store.LoadConversationSnapshotRange(ctx, conversationID, beforeSequence, afterSequence, limit)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation:                     rows.Conversation,
				ActiveBranch:                     rows.ActiveBranch,
				EditFloorSequence:                rows.EditFloorSequence,
				NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
				Turns:                            rows.Turns,
				Messages:                         rows.Messages,
				Activities:                       rows.Activities,
				BranchPoints:                     rows.BranchPoints,
				BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
				BatchedDeliveries:                rows.BatchedDeliveries,
				OldestSequence:                   rows.OldestSequence,
				HasMoreBefore:                    rows.HasMoreBefore,
				AfterSequence:                    rows.AfterSequence,
			}, nil
		}),
		Drivers:  chatDrivers,
		Activity: lcStack.LCM,
		Log:      log,
		NewID:    uuid.NewString,
		OnAccountChanged: func(sessionID domain.SessionID, generation string, harness domain.AgentHarness) {
			recheckSessionAccount(sessionID, harness)
			if harness != domain.HarnessCodex || agentSvc == nil || agentSvc.CodexAccountSwitchInProgress() {
				return
			}
			rec, ok, readErr := store.GetSession(ctx, sessionID)
			if readErr == nil && ok && rec.Harness == domain.HarnessCodex && rec.Metadata.ControllerGeneration == generation {
				agentSvc.InvalidateCodexAccountAuthentication()
			}
		},
		RecheckAccount: recheckSessionAccount,
		OnCodexCapacityChanged: func(sessionID domain.SessionID, generation string, observation ports.CodexCapacityObservation) {
			if agentSvc == nil || agentSvc.CodexAccountSwitchInProgress() {
				return
			}
			rec, ok, readErr := store.GetSession(ctx, sessionID)
			if readErr != nil || !ok || rec.Harness != domain.HarnessCodex || rec.Metadata.ControllerGeneration != generation {
				return
			}
			agentSvc.ObserveActiveCodexAccountCapacity(observation)
		},
		OnModelChanged: func(sessionID domain.SessionID, model string) {
			if sessMgr == nil {
				return
			}
			if err := sessMgr.PersistChatModel(ctx, sessionID, model); err != nil {
				log.Warn("persist ChatUI model on session failed; a TUI rebuild may resume with a different model",
					"sessionID", sessionID, "model", model, "error", err)
			}
		},
	})
	chatSvc.SetRenderCheck(renderViaDesktop(browserBroker, "__render-check"))
	chatSvc.SetRenderMeasure(renderViaDesktop(browserBroker, "__render-measure"))

	if err := chatSvc.InitializeSideChats(ctx); err != nil {
		log.Warn("side chat initialization failed", "error", err)
	}
	codexModelPlugin := codexagent.New()
	codexModelDriver := codexappserver.New(codexModelPlugin, log)
	modelDiscoverer := modelcatalog.Discoverer{
		CodexModels: func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error) {
			return codexModelDriver.DiscoverModels(listCtx, request.WorkingDir, request.Env)
		},
		ACPOptions: map[string]modelcatalog.ACPOptionListFunc{
			"cline": func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				return chatdriveracp.DiscoverConfigOptions(listCtx, chatdriveracp.Launch{
					Command: request.Binary,
					Args:    []string{"--acp"},
					Env:     request.Env,
				}, request.WorkingDir, log)
			},
			"deepseek-harness": func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				return chatdriveracp.DiscoverConfigOptions(listCtx, chatdriveracp.Launch{
					Command: request.Binary,
					Args:    []string{"--profile", "acp"},
					Env:     request.Env,
				}, request.WorkingDir, log)
			},
			"docker-agent": func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				return dockeragentacp.DiscoverConfigOptions(listCtx, request.Binary, cfg.DataDir, request.Env, log)
			},
			"devin": func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				return chatdriveracp.DiscoverConfigOptions(listCtx, chatdriveracp.Launch{
					Command: request.Binary,
					Args:    []string{"acp"},
					Env:     request.Env,
				}, request.WorkingDir, log)
			},
			"hermes": func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error) {
				return chatdriveracp.DiscoverConfigOptionsForHarness(listCtx, chatdriveracp.Launch{
					Command: request.Binary,
					Args:    []string{"acp"},
					Env:     request.Env,
				}, request.WorkingDir, domain.HarnessHermes, log)
			},
		},
		ClaudeModels: func(listCtx context.Context, request ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
			return claudecodeagent.ProviderModels(listCtx, request.Binary, request.WorkingDir, request.Env)
		},
		ClaudeFingerprint: func(fingerprintCtx context.Context, request ports.AgentModelDiscoveryRequest) string {
			return claudecodeagent.ProviderCatalogFingerprint(fingerprintCtx, request.Binary, request.WorkingDir, request.Env)
		},
	}
	tracker := newMultiTracker(cfg.GitLab, log)
	codexPlugin := codexagent.New()
	codexHome, err := codexPlugin.NativeSessionConfigDir(ctx, nil)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("resolve device-global Codex home: %w", err)
	}
	codexOperationGate := codexops.NewGate()
	agentDeps := agentsvc.Deps{
		DataDir: cfg.DataDir,
		Cache:   store, Discoverer: modelDiscoverer, Sessions: store, Context: ctx, Logger: log,
		ModelDiscoveryDir:      filepath.Join(cfg.DataDir, "model-discovery"),
		CodexAccountRoot:       filepath.Join(cfg.StateDir, "harnesses", "codex", "accounts"),
		CodexPendingRoot:       filepath.Join(cfg.StateDir, "harnesses", "codex", "pending-accounts"),
		CodexSwitchStagingRoot: filepath.Join(cfg.StateDir, "harnesses", "codex", "switch-staging"),
		CodexGlobalHome:        codexHome,
		CodexAccountSwitches:   store,
		CodexAccounts: codexappserver.NewAccountFactoryWithResolver(func(resolveCtx context.Context) (string, error) {
			return codexagent.New().ResolveBinary(resolveCtx)
		}, log),
		CodexOperationGate: codexOperationGate,
		ModelGate:          claudeModelGate,
	}
	claudecodeagent.ConfigureModelVersionRequirements(
		filepath.Join(cfg.StateDir, "harnesses", "claude-code", "model-versions.json"))
	agentSvc = agentsvc.NewWithDeps(agentDeps)

	persistentHostsReconciled := make(chan struct{})
	reviewerChatsRecovered := make(chan struct{})
	reviewers, err := reviewer.NewResolver()
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("reviewer resolver: %w", err)
	}
	sessionSvc, reviewSvc, wiredSessMgr, err := startSession(ctx, cfg, runtimeAdapter, store, lcStack.LCM, lcStack.ArtifactVersions, messenger, telemetrySink, notificationWriter, agents, agentSvc, managedPreview, browserLifecycle, browserAuthority, chatLauncher{svc: chatSvc, persistentHostReconcileDone: persistentHostsReconciled}, reviewerChatsRecovered, settingsSvc, tracker, codexOperationGate, reviewers, reviewRefresher, log)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("wire session service: %w", err)
	}
	lcStack.StartActivityObserver(wiredSessMgr.SessionRuntimeEnv)
	if usage != nil {
		sessionSvc.SetUsageArchiveFlush(func(context.Context) { usage.requestArchiveFlush() })
	}
	sessionSvc.SetChatProviderPreserver(chatSvc.PreservesProviderOnRestart)
	memoryReader := usagesvc.NewMemoryReader(usagesvc.MemoryReaderDeps{
		Store: store, Runtime: runtimeAdapter, Reviewers: store, CacheTTL: 2 * time.Second,
		ChatHostPID: func(id domain.SessionID) (int, bool) {
			return persistenthost.HostPID(cfg.DataDir, string(id))
		},
		AppRootPIDs: func() []int {
			roots := []int{os.Getpid()}
			if p := os.Getppid(); p > 1 && os.Getenv("AO_OWNER") == "app" {
				roots = append(roots, p)
			}
			if pid, ok := runtimeAdapter.ServerPID(context.Background()); ok {
				roots = append(roots, pid)
			}
			return roots
		},
	})
	sessMgr = wiredSessMgr
	if switcher, ok := wiredSessMgr.(chatsvc.TerminalProviderSwitcher); ok {
		chatSvc.SetTerminalProviderSwitcher(switcher)
	}
	agentAccountSvc = newAgentAccounts(cfg, store, codexHome, agentDeps.CodexAccounts, log)
	agentAccountSvc.SetLimitLog(store)
	go agentAccountSvc.KeepReadingLimits(ctx, time.Minute)
	sessionForkSvc := wireSessionFork(store, sessionSvc, wiredSessMgr, chatSvc, chatDrivers, agentAccountSvc, lcStack.ArtifactVersions, lifecycleMessenger, log)
	chatSvc.SetUsageLimitAccounts(agentAccountSvc)
	if mover, ok := wiredSessMgr.(chatsvc.AccountHandoffMover); ok {
		chatSvc.SetAccountHandoff(settingsSvc.AccountHandoffEnabled, agentAccountSvc, mover)
	}
	if wired, ok := sessMgr.(interface {
		SetAgentAccounts(ports.AgentAccountLaunchEnv)
	}); ok {
		wired.SetAgentAccounts(agentAccountSvc)
	}
	if stopper, ok := sessMgr.(interface {
		ExitAgentsOnAccount(context.Context, string) error
	}); ok {
		agentAccountSvc.SetAgentStopper(stopper.ExitAgentsOnAccount)
	}
	sessionSvc.SetComputerLoginCheck(agentAccountSvc.RunsOnComputerLogin)
	agentAccountSvc.SetLoginConfirmed(func(account domain.AgentAccount, at time.Time) {
		go func() {
			defer sentryobs.RecoverBackground()
			waiting, err := store.ConversationsAwaitingLogin(ctx)
			if err != nil {
				log.Warn("list chats awaiting sign-in", "error", err)
				return
			}
			for _, conversation := range waiting {
				id, err := agentAccountSvc.SessionAccountID(ctx, conversation.SessionID, conversation.Harness)
				if err != nil || id != account.ID {
					continue
				}
				if _, err := store.ConfirmConversationLogin(ctx, conversation.ConversationID, at, time.Now().UTC()); err != nil {
					log.Warn("clear chat sign-in demand", "conversation", conversation.ConversationID, "error", err)
				}
			}
		}()
	})
	if agentSvc != nil {
		agentSvc.SetSavedDefaultSignIn(agentAccountSvc.DefaultSignIn)
		agentSvc.SetSavedDefaultLaunchEnv(agentAccountSvc.DefaultLaunchEnv)
		agentAccountSvc.SetDefaultChanged(func(p domain.AgentAccountProvider) {
			harness := map[domain.AgentAccountProvider]domain.AgentHarness{
				domain.AgentAccountProviderClaude: domain.HarnessClaudeCode,
				domain.AgentAccountProviderCodex:  domain.HarnessCodex,
			}[p]
			if harness != "" {
				agentSvc.InvalidateAgentAuthentication(string(harness))
			}
		})
		agentSvc.WarmModelCatalogs(ctx)
	}
	if runner, ok := sessMgr.(interface {
		RunPromptSuggestionTask(context.Context, domain.SessionID, string, string, string) (string, error)
	}); ok {
		chatSvc.SetPromptSuggestionRunner(func(requestCtx context.Context, id domain.SessionID, turnID, systemPrompt, prompt string) (string, error) {
			taskCtx, cancel := context.WithCancel(requestCtx)
			stopOnShutdown := context.AfterFunc(ctx, cancel)
			defer stopOnShutdown()
			defer cancel()
			return runner.RunPromptSuggestionTask(taskCtx, id, turnID, systemPrompt, prompt)
		})
	}
	if fenced, ok := sessMgr.(interface{ SetPersistentHostReconcileDone(<-chan struct{}) }); ok {
		fenced.SetPersistentHostReconcileDone(persistentHostsReconciled)
	}
	if wake, ok := sessMgr.(interface {
		WakeHibernatedChat(context.Context, domain.SessionID) error
	}); ok {
		chatSvc.SetWakeCallback(func(requestCtx context.Context, id domain.SessionID) error {
			wakeCtx, cancel := context.WithCancel(requestCtx)
			stopOnShutdown := context.AfterFunc(ctx, cancel)
			defer stopOnShutdown()
			defer cancel()
			return wake.WakeHibernatedChat(wakeCtx, id)
		})
	}
	if tunable, ok := sessMgr.(interface {
		SetModelCatalog(interface {
			Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error)
		})
	}); ok {
		tunable.SetModelCatalog(agentSvc)
	}

	wireManagedPreviewExit(managedPreview, sessionSvc, log)
	sessMgr.SetTerminalInputGate(termMgr)
	sessionImportSvc := sessionimport.New(store, cfg.DataDir, chatDrivers.SupportsChat)
	sessMgr.SetImportArchiveRefresher(sessionImportSvc)
	lifecycleMessenger.Bind(sessionLifecycleMessenger{sessMgr})
	lcStack.LCM.SetCompletionTerminator(sessMgr)
	lcStack.LCM.SetSessionOperationGate(sessMgr)
	termMgr.SetSessionInputLease(sessMgr)
	githubRepos := newGitHubRepoResolver(log)
	if githubRepos != nil {
		sessionSvc.SetGitHubRepositoryResolver(githubRepos)
	}
	projectSvc := projectsvc.NewWithDeps(projectsvc.Deps{Store: store, Sessions: sessionSvc, DefaultHarness: domain.AgentHarness(cfg.Agent), Telemetry: telemetrySink, Milestones: installMilestones(cfg), Logger: log, GitHubRepos: githubRepos})
	go projectSvc.WarmCanonicalRepos(ctx)
	reportSessions, ok := sessMgr.(reportSemanticSession)
	if !ok {
		return errors.New("wire report delivery: session manager lacks semantic send support")
	}
	var reportCoordinator *reportsvc.Coordinator
	reportSvc := reportsvc.New(reportsvc.Deps{Store: store, Artifacts: chatSvc, OnCreated: func(domain.ReportRecord) {
		if reportCoordinator != nil {
			reportCoordinator.Wake()
		}
	}})
	reportCoordinator = reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{
		Store:    store,
		Delivery: reportSemanticDelivery{chat: chatSvc, sessions: reportSessions, store: store},
	})
	chatSvc.SetReportCoordinator(reportCoordinator)
	reportDeliveryDone := reportCoordinator.Start(ctx)
	defer func() {
		stop()
		<-reportDeliveryDone
	}()
	lcStack.trackerDone = startTrackerIntake(ctx, cfg, store, sessionSvc, tracker, log)

	hostCommands := systemexec.New(cfg.DataDir)
	systemChecks := systemcheck.NewWithCommandRunner(agentSvc, hostCommands, hostCommands)
	privateNPMPrefixes := map[systeminstall.Target]string{}
	if prefix, err := opencode.V2NPMPrefix(); err == nil {
		privateNPMPrefixes[systeminstall.TargetOpencodeV2] = prefix
	}
	privateInstallerHomes := map[systeminstall.Target]string{}
	if home, err := opencode.V2InstallerHome(); err == nil {
		privateInstallerHomes[systeminstall.TargetOpencodeV2] = home
	}
	systemInstall := systeminstall.NewWithDeps(hostCommands, hostCommands, systeminstall.Deps{
		JobStore:                  store,
		Verifier:                  systeminstall.NewVerifier(agents, hostCommands),
		Sessions:                  store,
		Reviewers:                 store,
		DisableUpdateChecks:       !cfg.HarnessUpdateChecks,
		DisableUpdateWhileRunning: !cfg.UpdateWhileRunning,
		UpdateWhenIdle:            cfg.UpdateWhenIdle,
		ManagedRoot:               managedHarnessRoot(cfg),
		Releases:                  store,
		PrivateNPMPrefixes:        privateNPMPrefixes,
		PrivateInstallerHomes:     privateInstallerHomes,
	})
	if err := systemInstall.Recover(ctx); err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("recover harness install jobs: %w", err)
	}
	sessMgr.SetHarnessUseGate(systemInstall)
	systemInstall.SetHarnessUpdateObserver(sessMgr)
	systemInstall.SetIdleUpdateCoordinator(sessMgr)
	binaryutil.SetManagedBinDir(systemInstall.ManagedBinDir())
	binaryCaches := []harnessBinaryCache{
		resolverBinaryCache{agents: agents},
		chatDrivers,
		reviewers,
		pluginBinaryCache{harness: domain.HarnessCodex, plugin: codexModelPlugin},
	}
	systemInstall.SetBinaryInvalidator(func(target systeminstall.Target) {
		if harness, ok := installedAgentHarness(target); ok {
			invalidateHarnessBinary(agentSvc, harness, binaryCaches...)
		}
	})
	cuaDriver := cuadriver.New()
	systemInstall.SetSystemTargetResolver(systeminstall.TargetCuaDriver, cuaDriver.Locate)
	computerUse := computeruse.New(cuaDriver, cfg.DataDir)
	computerUse.SetEnabled(settingsSvc.ComputerUseEnabled)
	settingsSvc.SetCuaDriverInstalled(computerUse.Installed)
	systemInstall.SetTelemetry(telemetrySink)
	sessMgr.SetVersionTelemetry(telemetrySink)
	sessMgr.SetComputerUseEnabled(settingsSvc.ComputerUseEnabled)
	sessMgr.SetVersionRefreshPolicy(func() sessionmanager.VersionRefreshPolicy {
		return versionRefreshPolicy(ctx, cfg, settingsSvc)
	})
	chatSvc.SetVersionSwitchObserver(sessMgr.ObserveChatVersionSwitch)
	systemInstall.SetOnSucceeded(func(target systeminstall.Target) {
		harness, ok := installedAgentHarness(target)
		if !ok {
			return
		}
		invalidateHarnessBinary(agentSvc, harness, binaryCaches...)
		agentSvc.RecheckAgent(harness)
	})

	accessDevices, accessErr := mobilebridge.LoadAccessDevices(mobilebridge.AccessDevicesPath(cfg.DataDir))
	if accessErr != nil {
		log.Warn("load access device registry failed; paired devices must pair again", "err", accessErr)
	}
	pairingCodes := mobilebridge.NewPairingCodes(nil)
	bs := &controllers.BridgeService{
		ConfigPath:  mobilebridge.Path(cfg.DataDir),
		DefaultPort: mobilebridge.DefaultPort,
		Devices:     accessDevices,
		Codes:       pairingCodes,
	}
	mc := &controllers.MobileController{Bridge: bs}
	sessionSearchSvc := startSessionSearch(ctx, store, cfg.StateDir, log)
	browserService := browsersvc.New(sessionSvc, browserBroker, browserAuthority)
	var browserChrome controllers.BrowserChromeService
	if chrome != nil {
		browserService.UseChrome(chrome.broker, chrome.targets, log)
		browserChrome = chrome
	}
	browserLive := httpd.NewBrowserLiveHub(browserStreamBroker, sessionSvc, func() bool {
		state, err := mobilebridge.Load(bs.ConfigPath)
		return err == nil && state.Enabled && state.BrowserRemoteAccess
	}, log)
	bs.OnBrowserRemoteAccessDisabled = browserLive.CloseAll

	shellTermSvc := startShellTerminals(ctx, cfg, runtimeAdapter, store, projectSvc, sessionSvc, termMgr, log)
	var shellTrackers []shelltermsvc.TerminalTracker
	if rt, ok := runtimeAdapter.(shellagent.Runtime); ok {
		shellAgents := shellagent.NewManager(ctx, rt, agents, log)
		shellTrackers = append(shellTrackers, shellAgents)
		termMgr.SetShellAgents(shellAgents)
	}
	if rt, ok := runtimeAdapter.(ports.TerminalTitleWatcher); ok {
		titles := terminaltitle.NewManager(ctx, rt, store, log)
		shellTrackers = append(shellTrackers, titles)
		go titles.Run(ctx, cdcPipe.Broadcaster)
	}
	shellTermSvc.SetTerminalTrackers(ctx, shellTrackers...)
	systemChecks.SetGitHubAuthTerminalOpener(shellTermSvc)
	agentAuthSvc := agentauth.NewWithAgentResolver(hostCommands, agentSvc, shellTermSvc, cfg.DataDir)
	defer agentAuthSvc.Close()
	agentSvc.SetCodexAccountLoginTerminalOpener(shellTermSvc)
	agentAccountSvc.SetTerminals(shellTermSvc)
	sessMgr.SetShellTerminalCloser(shellTermSvc)
	var (
		usageCollector *usagesvc.Collector
		usagePipeline  *usagepipeline.Pipeline
		usagePricing   *usagePricingRuntime
	)
	if pricingRuntime, pricingErr := newUsagePricingRuntime(usagePricingRuntimeConfig{
		DataDir: cfg.DataDir,
		Store:   store,
		Logger:  log,
	}); pricingErr != nil {
		log.Warn("usage pricing disabled", "err", pricingErr)
	} else {
		usagePricing = pricingRuntime
		if pricingErr := pricingRuntime.Start(ctx); pricingErr != nil {
			log.Warn("usage pricing startup failed; continuing without catalog enrichment", "err", pricingErr)
		}
		defer func() {
			stop()
			usagePricing.Wait()
		}()
	}
	if roots, rootsErr := usagesvc.DefaultSourceRoots(ctx, cfg.DataDir); rootsErr != nil {
		log.Warn("usage collection disabled", "err", rootsErr)
	} else {
		usageCollector = usagesvc.NewCollector(store, roots, func(reconcile bool) {
			if usagePipeline == nil {
				return
			}
			if reconcile {
				usagePipeline.NotifySourcesChanged()
			} else {
				usagePipeline.NotifyInventoryChanged()
			}
		})
		if usagePricing != nil {
			usageCollector.OnRouteResolved(usagePricing.RepairLegacyAttribution)
		}
		ingestorConfig := usagepipeline.IngestorConfig{}
		if usagePricing != nil {
			ingestorConfig.Pricing = usagePricing.Manager()
			ingestorConfig.OnPricingError = func(err error) {
				log.Warn("usage event pricing failed", "err", err)
			}
			ingestorConfig.RequestAttributionRepair = usagePricing.RepairLegacyAttribution
		}
		ingestor := usagepipeline.NewIngestor(store, ingestorConfig)
		usagePipeline = usagepipeline.NewPipeline(store, ingestor, usagePipelineWatchRoots(roots), usagepipeline.CoordinatorConfig{
			Logger:     log,
			Initialize: usageCollector.BackfillActive,
			WatchRoots: func(watchCtx context.Context) ([]string, error) {
				qwenRoots, err := usageCollector.QwenWatchRoots(watchCtx)
				if err != nil {
					return nil, err
				}
				return append(usagePipelineWatchRoots(roots), qwenRoots...), nil
			},
			Reconcile: func(reconcileCtx context.Context) error {
				return usageCollector.ReconcileSources(reconcileCtx, 0)
			},
			ReconcilePath: usageCollector.ReconcilePath,
		})
		go func() {
			defer sentryobs.RecoverBackground()
			ticker := time.NewTicker(usageReconcileTick)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					usagePipeline.NotifySourcesChanged()
				}
			}
		}()
		lcStack.LCM.SetUsageFinalizer(usageCollector)
		if wired, ok := sessMgr.(interface {
			SetBackgroundTaskUsage(sessionmanager.BackgroundTaskUsage)
		}); ok {
			wired.SetBackgroundTaskUsage(usageCollector)
		}
		if wired, ok := sessMgr.(interface {
			SetUsageFlusher(sessionmanager.UsageFlusher)
		}); ok {
			wired.SetUsageFlusher(usagePipeline)
		}
	}
	lcStack.scmDone = startSCMObserver(ctx, store, lcStack.LCM, sessionSvc, cfg.GitLab, reviewRefresher, log)
	var prActions prsvc.ActionManager
	prReader := newMultiSCMProvider(cfg.GitLab, log)
	prMerger := newMultiSCMMerger(cfg.GitLab, log)
	if prReader != nil && prMerger != nil {
		prActions = prsvc.NewActionService(prsvc.ActionDeps{
			Store:         store,
			Merger:        prMerger,
			Reader:        prReader,
			Resolver:      prReader,
			Writer:        store,
			MergeWriter:   store,
			ThreadWriter:  store,
			StateChanger:  prReader,
			StateWriter:   store,
			Commenter:     prReader,
			CommentWriter: store,
			Refresher:     reviewRefresher,
			Attachments:   prReader,
			Stacker:       prReader,
			HeadChecks:    prReader,
			Details:       store,
			Lifecycle:     lcStack.LCM,
			Logger:        log,
		})
	} else {
		log.Warn("pr action service disabled: no usable SCM provider")
	}

	if reconcileErr := agentSvc.ReconcileCodexAccountSwitches(ctx); reconcileErr != nil {
		log.Warn("Codex account switch recovery deferred", "err", reconcileErr)
	}

	if reconcileErr := sessMgr.ReconcileStartupSafety(ctx); reconcileErr != nil {
		stop()
		managedPreview.Close()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("reconcile sessions on boot: %w", reconcileErr)
	}
	agentSvc.WarmCodexAccounts()
	automationSvc, automationDone := startAutomations(ctx, store, sessionSvc, telemetrySink, log)
	lcStack.automationDone = automationDone
	autoReview := autoreview.New(store, reviewSvc, autoreview.Config{Logger: log})
	lcStack.autoReviewDone = autoReview.Start(ctx)
	var (
		pushRegistry controllers.PushRegistry
		pushDevices  *mobilebridge.DeviceRegistry
		deviceRoster controllers.DeviceRoster
	)
	if reg, regErr := mobilebridge.LoadRegistry(mobilebridge.PushDevicesPath(cfg.DataDir)); regErr != nil {
		log.Warn("load push device registry failed; push notifications disabled", "err", regErr)
	} else {
		pushRegistry = reg
		pushDevices = reg
		deviceRoster = reg
		bs.PushDevices = reg
	}

	presenceTracker := presence.NewTracker()

	hostIdentity, identityErr := mobilebridge.EnsureLocalIdentity(cfg.DataDir)
	if identityErr != nil {
		log.Warn("could not establish host identity; /api/v1/identity will report unimplemented", "error", identityErr)
	}

	tunnelPID := mobilebridge.TunnelPIDPath(cfg.DataDir)
	if reapErr := mobilebridge.ReapStaleTunnel(tunnelPID, mobilebridge.IsLiveCloudflared, mobilebridge.KillProcess); reapErr != nil {
		log.Warn("could not reap a stale mobile tunnel", "error", reapErr)
	}
	managedTunnel := func(res mobilebridge.CloudflaredResolution) controllers.TunnelController {
		log.Info("mobile remote access available", "cloudflared", res.Path, "source", res.Source)
		deps := mobilebridge.ManagedTunnelDeps{Binary: res.Path, PIDPath: tunnelPID, Log: log}
		if cfg.CloudControlPlaneURL != "" && hostIdentity.HostID != "" {
			deps.Fixed = mobilebridge.NewFixedTunnelSource(cfg.CloudControlPlaneURL, cfg.DataDir)
			deps.HostID = hostIdentity.HostID
		}
		return mobilebridge.NewManagedTunnel(deps)
	}
	bs.ConnectorInstalling = systemInstall.CloudflaredInstalling
	bs.ResolveTunnel = func() controllers.TunnelController {
		res := mobilebridge.ResolveCloudflared(mobilebridge.LocalCloudflaredLookup(cfg.DataDir, systeminstall.PinnedCloudflared))
		if res.NeedsInstall {
			if st, err := mobilebridge.Load(bs.ConfigPath); err == nil && st.AccountTokenHash != "" {
				systemInstall.DownloadCloudflaredInBackground(bs.ConnectorInstalled)
			}
			return nil
		}
		return managedTunnel(res)
	}
	bs.ResolveTunnelInBackground(func() controllers.TunnelController {
		res := mobilebridge.ResolveCloudflared(mobilebridge.LocalCloudflaredLookup(cfg.DataDir, systeminstall.PinnedCloudflared))
		if res.NeedsInstall {
			log.Info("mobile remote access unavailable; cloudflared not installed",
				"rejectedSystemPath", res.SystemPath)
			return nil
		}
		return managedTunnel(res)
	})

	bs.HostID = hostIdentity.HostID
	if pushDevices != nil && hostIdentity.HostID != "" {
		dispatcher := push.NewDispatcher(notificationHub, pushDevices, push.NewExpoClient(os.Getenv("EXPO_ACCESS_TOKEN")), hostIdentity.HostID, log)
		go func() {
			defer sentryobs.RecoverBackground()
			dispatcher.Run(ctx)
		}()
	}
	if mobilebridge.KeepAwakeSupported() {
		ka := mobilebridge.NewKeepAwake(os.Getpid())
		ka.SetLidController(mobilebridge.NewLidAwake(os.Getpid(), os.Getenv("AO_WAKE_HELPER"), cfg.DataDir))
		bs.KeepAwake = ka
	}
	awakeHost := awakesvc.NewHost(os.Getpid(), os.Getenv("AO_WAKE_HELPER"), cfg.DataDir)
	awakeSvc, stopAwake := startAwake(ctx, store, awakeHost, awakesvc.BindingFuncs{
		Session: func(ctx context.Context, id string) (bool, error) {
			row, found, err := store.GetSession(ctx, domain.SessionID(id))
			return found && !row.IsTerminated, err
		},
		Agent:       store.HasAwakeAgentActivity,
		Interrupted: chatSvc.InterruptedAwakeAgents,
		Reconnect:   sessMgr.ReconnectAwakeAgent,
		App:         awakeHost.AppAlive,
	}, log)
	defer stopAwake()
	var nativeSessions ports.AgentNativeSessionResolver
	if codewhaleAgent, ok := agents.Agent(domain.HarnessCodewhale); ok {
		nativeSessions, _ = codewhaleAgent.(ports.AgentNativeSessionResolver)
	}

	hookActivity := hookprocess.New(lcStack.LCM, store, runtimeAdapter, nil, log)
	srv, err := httpd.NewWithDeps(cfg, log, termMgr, httpd.APIDeps{
		SessionForks:       sessionForkSvc,
		Awake:              awakeSvc,
		Projects:           projectSvc,
		ArtifactVersions:   lcStack.ArtifactVersions,
		HostID:             hostIdentity.HostID,
		Endpoints:          bs,
		Agents:             agentSvc,
		CodexAccounts:      agentSvc,
		AgentAccounts:      freshAccountUsage{agentAccountSvc, usagePipeline},
		SystemChecks:       systemChecks,
		Installer:          systemInstall,
		ComputerUse:        computerUse,
		Sessions:           sessionSvc,
		Automations:        automationSvc,
		DesktopWorkspaces:  sessionSvc,
		PRs:                prActions,
		Reviews:            reviewSvc,
		Notifications:      notifier,
		Reports:            reportSvc,
		NotificationStream: notificationHub,
		Push:               pushRegistry,
		Presence:           presenceTracker,
		ClientTimeZone:     clienttz.NewRecorder(ctx, cfg.DataDir),
		DeviceRoster:       deviceRoster,
		DeviceLive:         presenceTracker,
		DeviceAccess:       bs,
		AccessDevices:      accessDevices,
		Import:             importsvc.New(importsvc.Deps{Store: store}),
		SessionImport:      sessionImportSvc,
		Directories:        fsbrowsersvc.New(),
		ShellTerminals:     shellTermSvc,
		PaneTerminals:      shellTermSvc,
		Cues:               cuesvc.New(cuesvc.Deps{Store: store, Sessions: sessionSvc, Terminals: shellTermSvc}),
		AgentAuth:          agentAuthSvc,
		GitHub:             newGitHubStatusService(githubpat.New(cfg.DataDir), prReader),
		Conversations:      chatSvc,
		ConversationSearch: store,
		Settings:           settingsSvc,
		CDC:                store,
		Events:             cdcPipe.Broadcaster,
		Activity:           hookActivity,
		PRRefresh:          reviewRefresher,
		NativeSessions:     nativeSessions,
		UsageHooks:         usageCollector,
		UsageSummary:       freshUsageSummary{usagesvc.NewSummaryReader(store), usagePipeline},
		UsageReport:        freshUsageReport{usageReportReader(store, usagePricing), usagePipeline},
		UsageLimitHistory:  usagesvc.NewLimitHistoryReader(store, agentAccountSvc),
		UsageLimitSetting:  store,
		UsagePrices:        usagePrices(usagePricing),
		SessionMemory:      memoryReader,
		SessionSteps:       lcStack.LCM,
		Telemetry:          telemetrySink,
		TelemetryIdentity:  telemetryIdentity,
		LifetimeSessions:   store.LifetimeSessions,
		UsageFlush: func(ctx context.Context) {
			if usage != nil {
				usage.flush(ctx, false)
			}
		},
		Mobile:      mc,
		BrowserLive: browserLive,
		DevImport: devimportsvc.New(devimportsvc.Deps{
			Store:         store,
			TargetDataDir: cfg.DataDir,
			OpenSource: func(ctx context.Context, dataDir string) (devimportsvc.SourceStore, error) {
				return sqlite.OpenReadOnly(ctx, dataDir)
			},
		}),
		Browser:                  browserService,
		BrowserChrome:            browserChrome,
		Panes:                    paneBroker,
		LinkPreview:              linkpreviewsvc.New(nil),
		SessionSearch:            sessionSearchSvc,
		PreviewServer:            managedPreview,
		SessionCapabilities:      browserAuthority,
		ShellPreviewCapabilities: shellTermSvc,
	})
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return err
	}
	browserLive.SetLoopbackBaseURL("http://" + srv.Addr().String())
	previewDone := preview.NewPoller(store, sessionSvc, "http://"+srv.Addr().String(), preview.PollerConfig{Logger: log}).Start(ctx)
	_ = os.Unsetenv(browserruntime.RuntimeAddressEnv)
	if ln, addr, err := browserruntime.Listen(cfg.RunFilePath); err != nil {
		log.Warn("browser runtime: listener unavailable; agent browser control disabled", "err", err)
	} else {
		if err := os.Setenv(browserruntime.RuntimeAddressEnv, addr); err != nil {
			_ = ln.Close()
			return fmt.Errorf("publish browser runtime address: %w", err)
		}
		log.Info("browser runtime: listening", "addr", addr)
		go func() {
			defer sentryobs.RecoverBackground()
			if err := browserBroker.Serve(ctx, ln); err != nil {
				log.Warn("browser runtime: serve stopped with error", "err", err)
			}
		}()
	}
	if chrome != nil {
		if err := chrome.serve(ctx, cfg.RunFilePath); err != nil {
			log.Warn("browser chrome: listener unavailable; Chrome mode disabled", "err", err)
		}
	}
	_ = os.Unsetenv(browserstream.RuntimeAddressEnv)
	if ln, addr, err := browserstream.Listen(cfg.RunFilePath); err != nil {
		log.Warn("browser stream: listener unavailable; mobile live browser disabled", "err", err)
	} else {
		if err := os.Setenv(browserstream.RuntimeAddressEnv, addr); err != nil {
			_ = ln.Close()
			return fmt.Errorf("publish browser stream address: %w", err)
		}
		log.Info("browser stream: listening", "addr", addr)
		go func() {
			defer sentryobs.RecoverBackground()
			if err := browserStreamBroker.Serve(ctx, ln); err != nil {
				log.Warn("browser stream: serve stopped with error", "err", err)
			}
		}()
	}
	var usageDone <-chan struct{}

	lan := httpd.NewMobileLAN(srv.Handler(), hostIdentity.HostID, mobilebridge.DefaultPort, log, telemetrySink)
	lan.UseDeviceCredentials(accessDevices, pairingCodes)
	bs.LAN = lan

	if err := restoreMobileOnBoot(mobilebridge.Path(cfg.DataDir), bs); err != nil {
		log.Warn("restore mobile and awake settings on boot failed", "err", err)
	}
	go func() {
		defer sentryobs.RecoverBackground()
		runRemoteHostAddressPublisher(ctx, bs, cfg.CloudControlPlaneURL, log)
	}()

	if usagePipeline != nil {
		usageDone = usagePipeline.Start(ctx)
	}
	const supervisorGrace = 5 * time.Second

	if ln, addr, err := supervisor.Listen(cfg.RunFilePath); err != nil {
		log.Warn("supervisor: listener unavailable; frontend-death auto-stop disabled", "err", err)
	} else {
		log.Info("supervisor: listening", "addr", addr)
		sup := supervisor.New(supervisorGrace, func() {
			lifecycle.Record("supervisor_link_lost", map[string]any{"graceMs": supervisorGrace.Milliseconds()})
			srv.RequestShutdownAfterDesktopExit()
		}, log)
		go func() {
			defer sentryobs.RecoverBackground()
			if err := sup.Serve(ctx, ln); err != nil {
				log.Warn("supervisor: serve stopped with error", "err", err)
			}
		}()
	}

	var startupReconcileDone <-chan struct{}
	var usageLimitResumeDone <-chan struct{}
	runErr := srv.RunWithReady(ctx, func() {
		readinessDone := agentSvc.WarmReadiness()
		if cfg.HarnessUpdateChecks {
			go func() {
				defer sentryobs.RecoverBackground()
				watchInstalledHarnessUpdates(ctx, readinessDone, cfg.HarnessUpdateInterval, agentSvc.EnsureInstallationReadiness, func(ctx context.Context, target systeminstall.Target, refresh bool) (systeminstall.UpdateAdvisory, error) {
					return systemInstall.UpdateAdvisory(ctx, target, refresh)
				}, log)
			}()
		}
		done := make(chan struct{})
		startupReconcileDone = done
		go func() {
			defer sentryobs.RecoverBackground()
			defer close(done)
			defer lcStack.StartReaperLoop()
			if reconcileErr := reconcilePersistentChatHosts(ctx, cfg.DataDir, store, cfg.AppRunID); reconcileErr != nil {
				log.Error("persistent chat host reconciliation on boot failed", "err", reconcileErr)
				sentryobs.CaptureBackground(ctx, reconcileErr, "chat", "reconcile", map[string]string{"reason": "chat_host"})
			}
			close(persistentHostsReconciled)
			if reconcileErr := reviewSvc.RecoverChatReviewers(ctx); reconcileErr != nil {
				log.Warn("reviewer chat recovery deferred", "err", reconcileErr)
			}
			close(reviewerChatsRecovered)
			if reconcileErr := sessMgr.ReconcileBackground(ctx); reconcileErr != nil {
				log.Error("background session reconciliation on boot failed", "err", reconcileErr)
			}
			if reconcileErr := lcStack.ReconcileRuntime(ctx); reconcileErr != nil {
				log.Error("background agent-process reconciliation on boot failed", "err", reconcileErr)
				sentryobs.CaptureBackground(ctx, reconcileErr, "lifecycle", "reconcile", map[string]string{"reason": "runtime"})
			}
		}()
		go func() {
			defer sentryobs.RecoverBackground()
			ticker := time.NewTicker(chatHibernateSweepTick)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if !settingsSvc.ChatHibernationEnabled() {
						continue
					}
					if err := sessMgr.HibernateIdleChats(ctx); err != nil && ctx.Err() == nil {
						log.Warn("idle Chat hibernation failed", "err", err)
					}
				}
			}
		}()
		go func() {
			defer sentryobs.RecoverBackground()
			select {
			case <-done:
			case <-ctx.Done():
				return
			}
			ticker := time.NewTicker(workspaceCleanupSweepTick)
			defer ticker.Stop()
			for {
				if err := sessMgr.ReconcileWorkspaceCleanup(ctx); err != nil && ctx.Err() == nil {
					log.Warn("workspace cleanup reconcile failed", "err", err)
				}
				reapForkSeeds(ctx, store, log)
				if settings, err := settingsSvc.Get(ctx); err != nil {
					log.Warn("read session retention", "err", err)
				} else if err := sessionSvc.DeleteExpired(ctx, settings.SessionRetentionDays); err != nil && ctx.Err() == nil {
					log.Warn("delete expired sessions failed", "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		resumeDone := make(chan struct{})
		usageLimitResumeDone = resumeDone
		go func() {
			defer sentryobs.RecoverBackground()
			defer close(resumeDone)
			var sweeps sync.WaitGroup
			defer sweeps.Wait()
			select {
			case <-done:
			case <-ctx.Done():
				return
			}
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				sweeps.Go(func() {
					defer sentryobs.RecoverBackground()
					if err := chatSvc.ResumeUsageLimitedTurns(ctx); err != nil && ctx.Err() == nil {
						log.Warn("usage-limit auto-resume failed", "err", err)
					}
				})
				sweeps.Go(func() {
					defer sentryobs.RecoverBackground()
					if err := chatSvc.HandoffUsageLimitedTurns(ctx); err != nil && ctx.Err() == nil {
						log.Warn("usage-limit account handoff failed", "err", err)
					}
				})
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		go func() {
			defer sentryobs.RecoverBackground()
			runVersionRefreshSweep(ctx, cfg, settingsSvc, sessMgr, systemInstall, log)
		}()
	})

	shutdownFields := map[string]any{"stopAgents": srv.StopAgentsRequested()}
	if cause := context.Cause(ctx); cause != nil && ctx.Err() != nil {
		shutdownFields["cause"] = "signal"
		shutdownFields["signal"] = cause.Error()
	} else if runErr != nil {
		shutdownFields["cause"] = "server_error"
	} else {
		shutdownFields["cause"] = "requested"
	}
	lifecycle.RecordError("daemon_shutdown_begin", runErr, shutdownFields)

	stop()
	if usageLimitResumeDone != nil {
		<-usageLimitResumeDone
	}
	installStopCtx, installStopCancel := context.WithTimeout(context.Background(), installerShutdownTimeout)
	if err := systemInstall.Close(installStopCtx); err != nil {
		log.Error("harness installer shutdown", "err", err)
	}
	installStopCancel()
	computerUseStopCtx, computerUseStopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	if err := computerUse.Close(computerUseStopCtx); err != nil {
		log.Error("computer use shutdown", "err", err)
	}
	computerUseStopCancel()
	if startupReconcileDone != nil {
		<-startupReconcileDone
	}
	backgroundStopCtx, backgroundStopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	if err := sessMgr.WaitBackgroundWorkers(backgroundStopCtx); err != nil {
		log.Error("session background worker shutdown", "err", err)
	}
	backgroundStopCancel()
	switchStopCtx, switchCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	if err := sessMgr.WaitAgentSwitchWorkers(switchStopCtx); err != nil {
		log.Error("interface recovery worker shutdown", "err", err)
	}
	switchCancel()
	codexSwitchStopCtx, codexSwitchCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	if err := agentSvc.WaitCodexAccountSwitchWorkers(codexSwitchStopCtx); err != nil {
		log.Error("Codex account switch worker shutdown", "err", err)
	}
	codexSwitchCancel()
	managedPreview.Close()
	<-previewDone
	chatStopTimeout := cfg.ShutdownTimeout
	if srv.StopAgentsRequested() {
		chatStopTimeout = max(chatStopTimeout, 30*time.Second)
	}
	chatStopCtx, chatCancel := context.WithTimeout(context.Background(), chatStopTimeout)
	if srv.StopAgentsRequested() {
		if err := sessMgr.StopAgentsForQuit(chatStopCtx); err != nil {
			log.Error("desktop quit agent shutdown", "err", err)
		}
		if err := chatSvc.TerminateAll(chatStopCtx); err != nil {
			log.Error("desktop quit chat shutdown", "err", err)
		}
	} else {
		chatSvc.StopAll(chatStopCtx)
	}
	chatCancel()
	if usageDone != nil {
		<-usageDone
	}
	if usagePricing != nil {
		usagePricing.Wait()
	}
	lcStack.Stop()
	bs.ShutdownServe()
	bs.ShutdownTunnel()
	bs.ShutdownKeepAwake()
	lanStopCtx, lanCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer lanCancel()
	if err := lan.Stop(lanStopCtx); err != nil {
		log.Error("mobile LAN listener shutdown", "err", err)
	}
	if err := cdcPipe.Stop(); err != nil {
		log.Error("cdc pipeline shutdown", "err", err)
	}
	return runErr
}

func telemetryAppVersion(configured, buildVersion string) string {
	if configured != "" {
		return configured
	}
	buildVersion = strings.TrimSpace(buildVersion)
	if buildVersion == "dev" {
		return ""
	}
	return buildVersion
}

type reportSemanticSession interface {
	SendSemantic(context.Context, domain.SessionID, string, string) error
	InterruptTUI(context.Context, domain.SessionID) error
}

type reportSemanticDelivery struct {
	chat     *chatsvc.Service
	sessions reportSemanticSession
	store    interface {
		GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	}
}

func (d reportSemanticDelivery) Submit(ctx context.Context, id domain.SessionID, message, idempotencyKey string) error {
	if d.sessions == nil {
		return reportsvc.ErrReportDeliveryModeUnsupported
	}
	err := d.sessions.SendSemantic(ctx, id, message, idempotencyKey)
	if errors.Is(err, sessionmanager.ErrComposerBusy) {
		return fmt.Errorf("%w: %w", reportsvc.ErrOrchestratorBusy, err)
	}
	return err
}

func (d reportSemanticDelivery) Interrupt(ctx context.Context, id domain.SessionID) error {
	if d.store == nil {
		return reportsvc.ErrReportDeliveryModeUnsupported
	}
	rec, ok, err := d.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return sessionmanager.ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		if d.sessions == nil {
			return reportsvc.ErrReportDeliveryModeUnsupported
		}
		return d.sessions.InterruptTUI(ctx, id)
	}
	if d.chat == nil {
		return reportsvc.ErrReportDeliveryModeUnsupported
	}
	err = d.chat.Interrupt(ctx, id)
	if errors.Is(err, chatsvc.ErrNoActiveTurn) {
		return nil
	}
	return err
}

func installedAgentHarness(target systeminstall.Target) (string, bool) {
	if target == systeminstall.TargetClaude {
		return "claude-code", true
	}
	if systeminstall.IsAgentTarget(target) {
		return string(target), true
	}
	return "", false
}

type harnessBinaryCache interface {
	InvalidateBinaryResolution(harness domain.AgentHarness)
}

type resolverBinaryCache struct{ agents ports.AgentResolver }

func (c resolverBinaryCache) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if agent, ok := c.agents.Agent(harness); ok {
		if invalidator, ok := agent.(ports.AgentBinaryResolutionInvalidator); ok {
			invalidator.InvalidateBinaryResolution()
		}
	}
}

type pluginBinaryCache struct {
	harness domain.AgentHarness
	plugin  ports.AgentBinaryResolutionInvalidator
}

func (c pluginBinaryCache) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if harness == c.harness {
		c.plugin.InvalidateBinaryResolution()
	}
}

func invalidateHarnessBinary(readiness ports.AgentReadinessProvider, harness string, caches ...harnessBinaryCache) {
	readiness.InvalidateAgentInstallation(harness)
	for _, cache := range caches {
		cache.InvalidateBinaryResolution(domain.AgentHarness(harness))
	}
}

func usagePipelineWatchRoots(roots usagesvc.SourceRoots) []string {
	return []string{
		roots.ClaudeProjects,
		roots.CodexSessions,
		roots.CodexArchived,
		roots.KimiHome,
		roots.PiSessions,
		roots.PiAgentSessions,
		roots.QwenUsage,
		roots.OMPSessions,
		roots.KimchiSessions,
		roots.PrimeAgentSessions,
		roots.GooseSessions,
		roots.OpenCodeData,
		roots.KiloData,
		roots.GrokSessions,
		roots.DeepSeekSessions,
		roots.AgyCLIConversations,
		roots.AgyACPConversations,
	}
}

func newDaemonLogger() (*slog.Logger, func(dataDir string)) {
	file := &deferredLogFile{}
	log := slog.New(slog.NewTextHandler(teeLogWriter{stderr: os.Stderr, file: file}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log, func(dataDir string) {
		rotating, err := openRotatingLogFile(filepath.Join(dataDir, daemonLogName), maxDaemonLogBytes)
		if err != nil {
			log.Warn("daemon log file unavailable; logging to stderr only", "error", err)
			return
		}
		file.attach(rotating)
	}
}

func dataDirTakeoverWait(shutdownTimeout, installerShutdownTimeout time.Duration) time.Duration {
	return 8*shutdownTimeout + max(shutdownTimeout, installerShutdownTimeout) + 18*time.Second
}

func lockDataDir(ctx context.Context, dataDir string, wait time.Duration, log *slog.Logger) (*datadirlock.Lock, error) {
	lock, err := datadirlock.Acquire(ctx, dataDir, 0)
	if errors.Is(err, datadirlock.ErrUnsupported) {
		log.Warn("data dir file system cannot lock files; starting without the single-daemon guard", "err", err)
		return nil, nil
	}
	if !errors.Is(err, datadirlock.ErrLocked) {
		return lock, err
	}
	log.Warn("waiting for another AO process to release the data dir", "err", err, "wait", wait)
	start := time.Now()
	lock, err = datadirlock.Acquire(ctx, dataDir, wait)
	if errors.Is(err, datadirlock.ErrLocked) {
		return nil, fmt.Errorf("%w; waited %s for it to exit, refusing to start", err, wait)
	}
	if err != nil {
		return nil, err
	}
	log.Info("data dir lock acquired", "waited", time.Since(start).Round(time.Millisecond))
	return lock, nil
}

func stabilizeWorkingDirectory(dataDir string) error {
	if dataDir == "" {
		return fmt.Errorf("daemon working directory: data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("daemon working directory: create %s: %w", dataDir, err)
	}
	if err := os.Chdir(dataDir); err != nil {
		return fmt.Errorf("daemon working directory: chdir %s: %w", dataDir, err)
	}
	return nil
}

func newAgentAccounts(cfg config.Config, store *sqlite.Store, codexHome string, codexAccounts ports.CodexAccountClientFactory, log *slog.Logger) *agentaccounts.Service {
	claudeHome, claudeConfig := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")), ""
	if claudeHome != "" {
		claudeConfig = filepath.Join(claudeHome, ".claude.json")
	} else if home, err := os.UserHomeDir(); err == nil {
		claudeHome, claudeConfig = filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")
	}
	claudePlugin := claudecodeagent.New()
	providers := []ports.AgentAccountProviderCLI{
		&agentaccount.Claude{
			Binary: claudePlugin.ResolveBinary, DeviceHome: claudeHome, DeviceConfig: claudeConfig,
			DeviceEnv: func(ctx context.Context) map[string]string {
				return claudePlugin.RuntimeCredentialEnv(cfg.DataDir, providerkeys.HoldBack(ctx, nil, cfg.DataDir, "claude-code"))
			},
		},
		&agentaccount.Codex{Binary: codexagent.New().ResolveBinary, Accounts: codexAccounts, DeviceHome: codexHome},
	}
	return agentaccounts.New(store, providers, filepath.Join(cfg.StateDir, "agent-accounts"), nil, log)
}

func claudeModelGate(ctx context.Context, agentID string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog {
	if agentID != string(domain.HarnessClaudeCode) {
		return catalog
	}
	binary := catalog.Metadata["binary"]
	if binary == "" {
		resolved, err := claudecodeagent.New().ResolveBinary(ctx)
		if err != nil {
			return catalog
		}
		binary = resolved
	}
	return claudecodeagent.GateModelCatalog(ctx, binary, catalog)
}
