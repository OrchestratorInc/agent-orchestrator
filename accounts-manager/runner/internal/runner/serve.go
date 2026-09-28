package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/api"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	sdklogging "github.com/router-for-me/CLIProxyAPI/v7/sdk/logging"
	log "github.com/sirupsen/logrus"
)

const leaseDuration = 45 * time.Second

// Serve starts the embedded upstream engine and blocks until AO's context is
// cancelled, the engine exits, or the daemon lease expires.
func Serve(ctx context.Context, stateDir string) error {
	return serve(ctx, stateDir, nil)
}

func serve(ctx context.Context, stateDir string, beforeRoutes func(*coreauth.Manager)) (resultErr error) {
	previousOutput := log.StandardLogger().Out
	log.SetOutput(io.Discard)
	defer log.SetOutput(previousOutput)
	state, err := LoadState(stateDir)
	if err != nil {
		return err
	}
	vault, err := openCredentialVault(state.Root)
	if err != nil {
		return err
	}
	defer func() {
		if err := vault.Close(); err != nil {
			resultErr = errors.Join(resultErr, errCredentialStorage)
		}
	}()
	if err := migrateDraftCredentials(ctx, state, vault); err != nil {
		return err
	}
	if err := requireEncryptedCredentialConfig(state); err != nil {
		return err
	}
	models, err := loadCredentialModels()
	if err != nil {
		return err
	}
	previousStore := sdkauth.GetTokenStore()
	sdkauth.RegisterTokenStore(vault)
	defer sdkauth.RegisterTokenStore(previousStore)
	instanceID, err := NewInstanceID()
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	if err := WriteRuntimeRecord(state.Root, RuntimeRecord{
		PID:             os.Getpid(),
		Port:            state.Config.Port,
		InstanceID:      instanceID,
		RunnerVersion:   Version,
		UpstreamVersion: UpstreamVersion,
		StartedAt:       startedAt,
	}); err != nil {
		return err
	}

	lease := NewLease(leaseDuration)
	control := NewControlHandler(ControlIdentity{
		InstanceID:    instanceID,
		RunnerVersion: Version,
		EngineVersion: UpstreamVersion,
	}, state.ControlKey, lease)
	oauth := newOAuthCoordinator(
		fmt.Sprintf("http://127.0.0.1:%d", state.Config.Port),
		state.ManagementKey,
		&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		nil,
	)
	defer oauth.Close()
	routeCapability, err := newRouteCapability(state.RoutingKey)
	if err != nil {
		return err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", state.Config.Port)
	exactSelector := newExactRouteSelector(routeCapability)
	exactSelector.admit = vault.admitVerified
	coreManager := coreauth.NewManager(vault, exactSelector, nil)
	coreManager.SetConfig(state.Config)
	coreManager.SetOAuthModelAlias(state.Config.OAuthModelAlias)
	coreManager.SetRetryConfig(state.Config.RequestRetry, time.Duration(state.Config.MaxRetryInterval)*time.Second, state.Config.MaxRetryCredentials)
	credentials := &credentialRuntime{vault: vault, manager: coreManager, models: models, context: ctx}
	defer credentials.Close()
	if err := credentials.Reload(ctx); err != nil {
		return err
	}
	oauth.useCredentials(ctx, credentials, state.Config)
	oauth.startCodexDevice = newCodexDeviceProcessStarter(state.Root)
	credentialHandler := &credentialHTTP{key: state.ManagementKey, runtime: credentials}
	accessManager := sdkaccess.NewManager()
	sdkaccess.RegisterProvider(routeAccessProviderType, routeCapability)
	defer sdkaccess.UnregisterProvider(routeAccessProviderType)
	credentialAlive := func(provider, authIndex string) bool {
		if coreManager == nil {
			return false
		}
		for _, auth := range coreManager.List() {
			if auth != nil && auth.Index == authIndex && strings.EqualFold(auth.Provider, provider) && vault.admitVerified(ctx, auth) && !auth.Unavailable && auth.Status == coreauth.StatusActive {
				return true
			}
		}
		return false
	}
	routeHandler := newRouteTokenHandler(state.ManagementKey, baseURL, routeCapability, credentialAlive)

	previousPassword, passwordWasSet := os.LookupEnv("MANAGEMENT_PASSWORD")
	if err = os.Setenv("MANAGEMENT_PASSWORD", state.ManagementKey); err != nil {
		return fmt.Errorf("configure private management access: %w", err)
	}
	defer func() {
		if passwordWasSet {
			_ = os.Setenv("MANAGEMENT_PASSWORD", previousPassword)
		} else {
			_ = os.Unsetenv("MANAGEMENT_PASSWORD")
		}
	}()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var setupErr error
	runtimeReady := false
	service, err := cliproxy.NewBuilder().
		WithConfig(state.Config).
		WithConfigPath(state.ConfigPath).
		// SDK model refreshes derive API-key catalogs from plaintext config. The
		// vault-backed request manager owns its catalog independently.
		WithCoreAuthManager(coreauth.NewManager(nil, nil, nil)).
		WithWatcherFactory(func(string, string, func(*sdkconfig.Config)) (*cliproxy.WatcherWrapper, error) {
			return &cliproxy.WatcherWrapper{}, nil
		}).
		WithRequestAccessManager(accessManager).
		WithLocalManagementPassword(state.ManagementKey).
		WithServerOptions(sdkapi.WithRequestLoggerFactory(func(*sdkconfig.Config, string) sdklogging.RequestLogger {
			// Disabled request logging still writes error bodies in the default logger.
			return nil
		})).
		WithServerOptions(sdkapi.WithMiddleware(runnerPolicy(state.ManagementKey, routeCapability, func() bool {
			return runtimeReady && coreManager.Selector() == exactSelector
		}, credentialAlive))).
		WithServerOptions(sdkapi.WithRouterConfigurator(func(router *gin.Engine, handler *handlers.BaseAPIHandler, _ *sdkconfig.Config) {
			if beforeRoutes != nil {
				beforeRoutes(handler.AuthManager)
			}
			if setupErr = credentials.attachExecutors(handler.AuthManager); setupErr != nil {
				cancel()
				return
			}
			handler.AuthManager = coreManager
			credentials.startAutoRefresh(runCtx)
			runtimeReady = true
			router.GET("/ao/internal/identity", gin.WrapH(control))
			router.POST("/ao/internal/lease", gin.WrapH(control))
			router.POST("/ao/internal/oauth/start", gin.WrapH(oauth))
			router.GET("/ao/internal/oauth/status", gin.WrapH(oauth))
			router.GET("/ao/internal/oauth/events", gin.WrapH(oauth))
			router.DELETE("/ao/internal/oauth/session", gin.WrapH(oauth))
			router.POST("/ao/internal/routes/token", gin.WrapH(routeHandler))
			router.PUT("/ao/internal/routes/bindings", gin.WrapH(routeHandler))
			router.Any(credentialPath, gin.WrapH(credentialHandler))
			router.Any(credentialPath+"/:action", gin.WrapH(credentialHandler))
		})).
		Build()
	if err != nil {
		return err
	}

	go cancelWhenLeaseExpires(runCtx, cancel, lease)
	err = service.Run(runCtx)
	if setupErr != nil {
		return setupErr
	}
	if errors.Is(err, context.Canceled) || runCtx.Err() != nil {
		return nil
	}
	return err
}

func cancelWhenLeaseExpires(ctx context.Context, cancel context.CancelFunc, lease *Lease) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if lease.Expired(now) {
				cancel()
				return
			}
		}
	}
}

var _ http.Handler = (*controlHandler)(nil)

func requireEncryptedCredentialConfig(state *State) error {
	cfg := state.Config
	if len(cfg.CodexKey) != 0 || len(cfg.ClaudeKey) != 0 || len(cfg.GeminiKey) != 0 || len(cfg.InteractionsKey) != 0 || len(cfg.OpenAICompatibility) != 0 || len(cfg.VertexCompatAPIKey) != 0 || len(cfg.XAIKey) != 0 || len(cfg.MetaKey) != 0 {
		return errors.New("legacy configured credentials require explicit import into encrypted storage")
	}
	entries, err := os.ReadDir(cfg.AuthDir)
	if err != nil || len(entries) != 0 {
		return errors.New("legacy credential files require explicit import into encrypted storage")
	}
	if cfg.SaveCooldownStatus || cfg.Home.Enabled {
		return errors.New("credential side-channel persistence must be disabled")
	}
	cfg.AuthAutoRefreshWorkers = 4
	return nil
}
