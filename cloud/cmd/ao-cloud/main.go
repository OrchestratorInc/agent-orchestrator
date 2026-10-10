package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/auth"
	"github.com/aoagents/agent-orchestrator/cloud/internal/cifeedback"
	"github.com/aoagents/agent-orchestrator/cloud/internal/config"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/httpapi"
	"github.com/aoagents/agent-orchestrator/cloud/internal/idlepause"
	"github.com/aoagents/agent-orchestrator/cloud/internal/interfacereconcile"
	"github.com/aoagents/agent-orchestrator/cloud/internal/notification"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/reconcile"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	coderprovider "github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/coder"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/createos"
	dockerprovider "github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/docker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandboxresolve"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// readSSHPubKeys loads the operator SSH keys authorized on every sandbox. They
// are a debugging affordance, not part of the worker's trust path.
func readSSHPubKeys(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sandbox SSH public keys %s: %w", path, err)
	}
	var keys []string
	for _, line := range strings.Split(string(raw), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			keys = append(keys, trimmed)
		}
	}
	return keys, nil
}

// provisioningDefaults is the plan every new session in this deployment is
// stamped with. It is resolved once at startup so a request never reads
// configuration, and so a misconfigured deployment fails at boot rather than on
// a user's first session.
func provisioningDefaults(cfg config.Config) sandbox.ProvisioningDefaults {
	return sandbox.ProvisioningDefaults{
		Provider: cfg.SandboxProvider,
		Release:  cfg.Release,
		NodeOps: sandbox.NodeOpsConfig{
			BaseURL:          cfg.NodeOpsBaseURL,
			APIKey:           cfg.NodeOpsAPIKey,
			DefaultShape:     cfg.NodeOpsDefaultShape,
			DefaultRootFS:    cfg.NodeOpsDefaultRootFS,
			RootFSByHarness:  cfg.NodeOpsRootFSByHarness,
			Ingress:          cfg.NodeOpsIngress,
			SSHKeyPath:       cfg.NodeOpsSSHKeyPath,
			WorkerTokenTTL:   cfg.NodeOpsWorkerTokenTTL,
			AutoPauseSeconds: cfg.NodeOpsAutoPauseSeconds,
		},
		Docker: sandbox.DockerConfig{
			Host:           cfg.DockerHost,
			WorkerImage:    cfg.DockerWorkerImage,
			Network:        cfg.DockerNetwork,
			Namespace:      cfg.DockerNamespace,
			WorkerTokenTTL: cfg.DockerWorkerTokenTTL,
		},
		Coder: sandbox.CoderConfig{
			BaseURL:        cfg.CoderURL,
			Owner:          cfg.CoderOwner,
			TemplateID:     cfg.CoderTemplateID,
			AgentName:      cfg.CoderAgentName,
			Parameters:     cfg.CoderParameters,
			DurableRoot:    cfg.CoderDurableRoot,
			WorkerTokenTTL: cfg.CoderWorkerTokenTTL,
		},
	}
}

// newSandboxReconciler builds the reconciler for a deployment that provisions
// sandboxes. It returns nil when this deployment does not: a control plane with
// no sandbox provider still serves the API, and the worker routes report 404
// rather than failing open.
func newSandboxReconciler(
	cfg config.Config,
	store *postgres.Store,
	providerCipher *secrets.Cipher,
	logger *slog.Logger,
) (*reconcile.Reconciler, error) {
	// Build every provider this control plane offers, not just the default, so a
	// single CP can serve more than one provider and a client can pick per
	// session. AvailableSandboxProviders always contains the default, and is
	// exactly that default for a single-provider deployment.
	var (
		nodeOpsProvider sandbox.Provider
		dockerProvider  sandbox.Provider
		coderProvider   sandbox.Provider
		buildsProvider  bool
	)
	for _, provider := range cfg.AvailableSandboxProviders {
		switch provider {
		case sandbox.ProviderNodeOps, sandbox.ProviderDocker, sandbox.ProviderCoder:
			buildsProvider = true
		}
	}
	if !buildsProvider {
		return nil, nil
	}
	workerBinary, workerHelperBinary, workerBuilds, err := loadWorkerBinaries(cfg, logger)
	if err != nil {
		return nil, err
	}
	for _, provider := range cfg.AvailableSandboxProviders {
		switch provider {
		case sandbox.ProviderNodeOps:
			sshPubKeys, err := readSSHPubKeys(cfg.NodeOpsSSHKeyPath)
			if err != nil {
				return nil, err
			}
			nodeOpsProvider = createos.New(createos.Config{
				BaseURL:      cfg.NodeOpsBaseURL,
				APIKey:       cfg.NodeOpsAPIKey,
				DefaultShape: cfg.NodeOpsDefaultShape,
				DefaultRoot:  cfg.NodeOpsDefaultRootFS,
				Region:       cfg.NodeOpsRegion,
				SSHPubKeys:   sshPubKeys,
			})
		case sandbox.ProviderDocker:
			provider, err := dockerprovider.New(dockerprovider.Config{
				Host:        cfg.DockerHost,
				WorkerImage: cfg.DockerWorkerImage,
				Network:     cfg.DockerNetwork,
				Namespace:   cfg.DockerNamespace,
			})
			if err != nil {
				return nil, err
			}
			dockerProvider = provider
		case sandbox.ProviderCoder:
			provider, err := coderprovider.New(coderprovider.Config{
				BaseURL:    cfg.CoderURL,
				Token:      cfg.CoderAPIToken,
				Owner:      cfg.CoderOwner,
				TemplateID: cfg.CoderTemplateID,
				AgentName:  cfg.CoderAgentName,
				Parameters: cfg.CoderParameters,
			})
			if err != nil {
				return nil, err
			}
			coderProvider = provider
		}
	}
	return reconcile.New(store, sandboxresolve.New(nodeOpsProvider, dockerProvider, coderProvider, store, providerCipher), reconcile.Options{
		PublicURL:              cfg.PublicURL,
		TerminalStreamEnabled:  cfg.TerminalStreamEnabled,
		WorkerBinary:           workerBinary,
		WorkerHelperBinary:     workerHelperBinary,
		WorkerBuilds:           workerBuilds,
		Interval:               cfg.ReconcileInterval,
		StartupTimeout:         cfg.SandboxStartupTimeout,
		HeartbeatTimeout:       cfg.WorkerHeartbeatTimeout,
		AllowAnonymousCheckout: cfg.AllowAnonymousCheckout,
		KeepWarm:               cfg.IdlePauseDisabled(),
		Logger:                 logger,
	}), nil
}

// loadWorkerBinaries reads the worker and helper binaries once at startup, but
// only where a provider that runs hosted workers (nodeops or coder) is offered.
// Docker-only deployments bake the worker into their image and need neither.
// Both the reconciler (to advertise the expected hashes) and the API server (to
// serve the content-addressed self-update endpoint) read the same bytes.
//
// The configured paths hold the linux/amd64 build every provider installs. The
// control-plane image also ships a linux/arm64 build beside them
// (<path>-linux-arm64) for Coder workspaces on arm64 hosts; it is optional so a
// deployment without it keeps working, with arm64 workspaces reported as
// unsupported.
func loadWorkerBinaries(cfg config.Config, logger *slog.Logger) (
	workerBinary, workerHelperBinary []byte,
	builds map[string]sandbox.WorkerBuild,
	err error,
) {
	needs := false
	for _, provider := range cfg.AvailableSandboxProviders {
		if provider == sandbox.ProviderNodeOps || provider == sandbox.ProviderCoder {
			needs = true
		}
	}
	if !needs {
		return nil, nil, nil, nil
	}
	workerBinary, err = readRequiredBinary(cfg.WorkerBinaryPath, "worker")
	if err != nil {
		return nil, nil, nil, err
	}
	workerHelperBinary, err = readRequiredBinary(cfg.WorkerHelperBinaryPath, "worker helper")
	if err != nil {
		return nil, nil, nil, err
	}
	builds = map[string]sandbox.WorkerBuild{}
	arm64Worker, workerErr := readRequiredBinary(cfg.WorkerBinaryPath+"-linux-arm64", "arm64 worker")
	arm64Helper, helperErr := readRequiredBinary(cfg.WorkerHelperBinaryPath+"-linux-arm64", "arm64 worker helper")
	if workerErr == nil && helperErr == nil {
		builds[sandbox.ArchARM64] = sandbox.WorkerBuild{Binary: arm64Worker, HelperBinary: arm64Helper}
	} else if logger != nil {
		logger.Warn("arm64 worker build unavailable; arm64 Coder workspaces will be reported as unsupported",
			"worker_error", errorString(workerErr), "helper_error", errorString(helperErr))
	}
	return workerBinary, workerHelperBinary, builds, nil
}

func readRequiredBinary(path, name string) ([]byte, error) {
	binary, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s binary %s: %w", name, path, err)
	}
	if len(binary) == 0 {
		return nil, fmt.Errorf("%s binary %s is empty", name, path)
	}
	return binary, nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("ao-cloud stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer cancel()

	if cfg.MigrateOnStartup {
		err := func() error {
			migrationContext, cancelMigration := context.WithTimeout(
				ctx,
				cfg.MigrationTimeout,
			)
			defer cancelMigration()
			return postgres.Migrate(migrationContext, cfg.MigrationDatabaseURL)
		}()
		if err != nil {
			return err
		}
	}
	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if cfg.Hosted() {
		if err := store.ValidateRuntimeRole(ctx); err != nil {
			return err
		}
	}
	notificationProcessor := notification.NewService(store, notification.Config{Logger: logger})

	var workosVerifier auth.WorkOSVerifier
	if cfg.WorkOSIssuer != "" {
		workosVerifier, err = newWorkOSVerifier(
			ctx,
			cfg.WorkOSIssuer,
			cfg.WorkOSClientID,
			cfg.WorkOSAPIKey,
			cfg.WorkOSJWKSURL,
		)
		if err != nil {
			return err
		}
	}
	if cfg.WorkOSLegacyIssuer != "" {
		legacyVerifier, err := newWorkOSVerifier(
			ctx,
			cfg.WorkOSLegacyIssuer,
			cfg.WorkOSLegacyClientID,
			cfg.WorkOSLegacyAPIKey,
			cfg.WorkOSLegacyJWKSURL,
		)
		if err != nil {
			return err
		}
		workosVerifier, err = auth.NewFallbackWorkOSVerifier(workosVerifier, legacyVerifier)
		if err != nil {
			return err
		}
		logger.Info("accepting legacy WorkOS tokens", "legacy_client_id", cfg.WorkOSLegacyClientID)
	}
	var providerCipher *secrets.Cipher
	if len(cfg.ProviderSecretKey) > 0 {
		providerCipher, err = secrets.New(cfg.ProviderSecretKey)
		if err != nil {
			return err
		}
	}

	var githubService *githubapp.Service
	if cfg.GitHub.Enabled() {
		githubClient, err := githubapp.New(githubapp.Config{
			AppID:         cfg.GitHub.AppID,
			AppSlug:       cfg.GitHub.AppSlug,
			ClientID:      cfg.GitHub.ClientID,
			ClientSecret:  cfg.GitHub.ClientSecret,
			PrivateKeyPEM: cfg.GitHub.PrivateKeyPEM,
			PublicURL:     cfg.GitHub.PublicURL,
		}, nil)
		if err != nil {
			return err
		}
		githubService, err = githubapp.NewService(
			store,
			githubClient,
			cfg.GitHub.StateKey,
			cfg.ProviderSecretKey,
			cfg.GitHub.WebhookSecret,
			cfg.GitHub.InstallTTL,
			logger,
		)
		if err != nil {
			return err
		}
		go githubService.Run(ctx)
		go githubService.RunAutomaticReviews(ctx, store)
	}
	var checkoutBroker httpapi.CheckoutBroker
	if githubService != nil {
		checkoutBroker = githubService
	} else if cfg.RepositoryBrokerURL != "" {
		checkoutBroker, err = githubapp.NewRemoteCheckoutBroker(
			store,
			providerCipher,
			cfg.RepositoryBrokerURL,
			cfg.Environment,
			cfg.RepositoryBrokerToken,
			nil,
		)
		if err != nil {
			return err
		}
	}
	reviewService := githubService
	if reviewService == nil {
		reviewService = githubapp.NewReviewService(store, logger)
	}
	// PAT write fallback: a REST-only GitHub client plus the record store lets a
	// worker's configured personal access token open and claim pull requests
	// even where the checkout broker is read-only (staging reaches GitHub through
	// the remote capability broker, whose write methods are stubbed). The PAT is
	// decrypted per request in the handler via providerCipher; this only needs
	// the REST client and the store. Constructed whenever PAT decryption is
	// possible so PAT-first writes behave consistently with PAT-first reads.
	var patWrites *githubapp.PATWriteService
	if providerCipher != nil {
		// Empty base URL defaults to https://api.github.com, matching the App
		// client; a GitHub Enterprise host would need a config field here.
		patWrites = githubapp.NewPATWriteService(
			githubapp.NewRESTClient("", nil), store,
		)
	}
	reconciler, err := newSandboxReconciler(cfg, store, providerCipher, logger)
	if err != nil {
		return err
	}
	// The scanner only has anything to do where sandboxes exist to pause, and
	// only when idle-pause is enabled. With AO_CLOUD_IDLE_PAUSE_THRESHOLD=0
	// (keep-warm) it never runs, so no session is ever paused for idleness.
	var idlePauseScanner *idlepause.Scanner
	if reconciler != nil && !cfg.IdlePauseDisabled() {
		idlePauseScanner = idlepause.New(store, idlepause.Options{
			Interval:      cfg.IdlePauseInterval,
			IdleThreshold: cfg.IdlePauseThreshold,
			Logger:        logger,
		})
	}
	// Worker tokens are only issued where sandboxes are provisioned. Leaving
	// this nil elsewhere is what makes the worker routes 404 instead of
	// accepting credentials no sandbox could have been given.
	var workerTokens httpapi.WorkerTokens
	if reconciler != nil {
		workerTokens = worker.NewTokenManager([]byte(cfg.WorkerSigningKey))
	}

	// The API server serves the content-addressed worker binaries so a worker
	// with a stale baked copy can self-update; it reads the same startup build
	// whose hashes the reconciler advertises.
	apiWorkerBinary, apiWorkerHelperBinary, apiWorkerBuilds, err := loadWorkerBinaries(cfg, nil)
	if err != nil {
		return err
	}
	// A read-only Coder client backs the template picker endpoint. Built only
	// when the deployment offers coder; otherwise the picker just shows "Default".
	var coderTemplates httpapi.CoderTemplateLister
	for _, provider := range cfg.AvailableSandboxProviders {
		if provider == sandbox.ProviderCoder {
			templateClient, err := coderprovider.New(coderprovider.Config{
				BaseURL:    cfg.CoderURL,
				Token:      cfg.CoderAPIToken,
				Owner:      cfg.CoderOwner,
				TemplateID: cfg.CoderTemplateID,
				AgentName:  cfg.CoderAgentName,
				Parameters: cfg.CoderParameters,
			})
			if err != nil {
				return fmt.Errorf("build coder template lister: %w", err)
			}
			coderTemplates = templateClient
			break
		}
	}
	apiOptions := httpapi.Options{
		Store:                     store,
		CoderTemplates:            coderTemplates,
		Transcripts:               store.SessionTranscripts(),
		WorkOS:                    workosVerifier,
		LocalAuthEnabled:          cfg.LocalAuthEnabled,
		LocalSessionTTL:           cfg.LocalSessionTTL,
		SandboxProvider:           cfg.SandboxProvider,
		AvailableSandboxProviders: cfg.AvailableSandboxProviders,
		CapabilityGatedProviders:  cfg.CapabilityGatedProviders,
		Provisioning:              provisioningDefaults(cfg),
		WorkerTokens:              workerTokens,
		WorkerTokenTTL:            cfg.WorkerTokenTTL(),
		WorkerBinary:              apiWorkerBinary,
		WorkerHelperBinary:        apiWorkerHelperBinary,
		WorkerBuilds:              apiWorkerBuilds,
		MaxSandboxes:              cfg.MaxSandboxesPerOrg,
		Environment:               cfg.Environment,
		Release:                   cfg.Release,
		Logger:                    logger,
		GitHub:                    githubService,
		ReviewService:             reviewService,
		CheckoutBroker:            checkoutBroker,
		PATWrites:                 patWrites,
		BrokerAuthToken:           cfg.RepositoryBrokerToken,
		EnvironmentControlToken:   cfg.EnvironmentControlToken,
		SecretCipher:              providerCipher,
		WebhookMaxBody:            cfg.GitHub.WebhookMaxBody,
		TerminalStreamEnabled:     cfg.TerminalStreamEnabled,
		TerminalRelayEnabled:      cfg.TerminalRelayEnabled,
		NotificationWake:          notificationProcessor.Wake,
	}
	if cfg.Environment == "development" &&
		os.Getenv("AO_CLOUD_DEVELOPMENT_SKIP_CREDENTIAL_VALIDATION") == "true" {
		logger.Warn("coding-agent credential validation is disabled for development")
		apiOptions.CredentialValidator = developmentCredentialValidator{}
	}
	api := httpapi.New(apiOptions)
	go notificationProcessor.Run(ctx)
	feedbackDispatcher := cifeedback.New(store, cifeedback.Config{Logger: logger})
	go feedbackDispatcher.Run(ctx)
	if cfg.TerminalRelayEnabled {
		logger.Info("experimental terminal relay enabled",
			"terminal_stream_enabled", cfg.TerminalStreamEnabled,
			"mode", "local_same_replica")
	}
	// The work-wait long-poll and terminal streaming both ride a Postgres NOTIFY
	// listener. Run it wherever workers connect so WaitForWork can be woken on
	// enqueue; register the terminal channels only when that feature is on.
	if reconciler != nil {
		notifyListener := postgres.NewListener(cfg.DatabaseURL, logger)
		notifyListener.Handle("ao_worker_work", api.HandleWorkerWorkNotify)
		notifyListener.Handle("ao_notification_event", api.HandleNotificationEventNotify)
		if cfg.TerminalStreamEnabled {
			notifyListener.Handle("ao_terminal_output", api.HandleTerminalOutputNotify)
			notifyListener.Handle("ao_terminal_input", api.HandleTerminalInputNotify)
		}
		go func() { _ = notifyListener.Run(ctx) }()
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		logger.Info("ao-cloud listening", "config", cfg.String())
		result <- server.ListenAndServe()
	}()

	if reconciler != nil {
		go func() {
			logger.Info("sandbox reconciler started",
				"provider", cfg.SandboxProvider,
				"interval", cfg.ReconcileInterval,
				"startup_timeout", cfg.SandboxStartupTimeout,
				"heartbeat_timeout", cfg.WorkerHeartbeatTimeout,
			)
			if err := reconciler.Run(ctx); err != nil {
				logger.Error("sandbox reconciler stopped", "error", err)
			}
		}()
	}

	if idlePauseScanner != nil {
		go func() {
			logger.Info("idle-pause scanner started",
				"interval", cfg.IdlePauseInterval,
				"idle_threshold", cfg.IdlePauseThreshold,
			)
			if err := idlePauseScanner.Run(ctx); err != nil {
				logger.Error("idle-pause scanner stopped", "error", err)
			}
		}()
	}

	transitionDriver := interfacereconcile.NewTransportDriver(store, "interface-coordinator", 45*time.Second, logger)
	transitionCoordinator := interfacereconcile.New(store, transitionDriver, interfacereconcile.Options{
		Interval: cfg.InterfaceHandoffInterval,
		Logger:   logger,
	})
	go func() {
		logger.Info("interface-transition coordinator started", "interval", cfg.InterfaceHandoffInterval)
		if err := transitionCoordinator.Run(ctx); err != nil {
			logger.Error("interface-transition coordinator stopped", "error", err)
		}
	}()

	select {
	case <-ctx.Done():
		api.SetDraining(true)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type developmentCredentialValidator struct{}

func (developmentCredentialValidator) Validate(
	context.Context,
	string,
	string,
	[]byte,
) error {
	return nil
}

func newWorkOSVerifier(
	ctx context.Context,
	issuer string,
	clientID string,
	apiKey string,
	jwksURL string,
) (auth.WorkOSVerifier, error) {
	profiles, err := auth.NewWorkOSProfileResolver(apiKey, nil)
	if err != nil {
		return nil, err
	}
	organizations, err := auth.NewWorkOSOrganizationResolver(apiKey, nil)
	if err != nil {
		return nil, err
	}
	return auth.NewOIDCVerifier(ctx, issuer, clientID, jwksURL, profiles, organizations)
}
