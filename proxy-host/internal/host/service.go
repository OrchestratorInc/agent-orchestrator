package host

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	proxyapi "github.com/router-for-me/CLIProxyAPI/v8/sdk/api"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"gopkg.in/yaml.v3"
)

// Build writes the SDK's configuration under root and puts the boundary in front of the SDK; build is what /ao/status reports, stop what /ao/stop calls.
func Build(root string, port int, controlKey, inferenceKey, build string, routes *Routes, activity *Activity, stop func()) (*cliproxy.Service, error) {
	if !filepath.IsAbs(root) || port < 1 || port > 65535 || len(controlKey) < 32 || len(inferenceKey) < 32 || controlKey == inferenceKey {
		return nil, errors.New("invalid proxy host configuration")
	}
	path, authDir := filepath.Join(root, "config.yaml"), filepath.Join(root, "auth")
	err := os.MkdirAll(authDir, 0700)
	if err == nil {
		err = os.Chmod(authDir, 0700)
	}
	if err != nil {
		return nil, err
	}
	// Sign-ins a helper without its umask saved readable to others are the user's alone too.
	if entries, err := os.ReadDir(authDir); err == nil {
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				_ = os.Chmod(filepath.Join(authDir, entry.Name()), 0o600)
			}
		}
	}
	// A saved file is loaded strictly: the lenient load answers a bad file with an empty one, which would lose its API keys.
	cfg, err := config.LoadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg, err = config.LoadConfigOptional(path, true)
	}
	if err != nil {
		return nil, err
	}
	cfg.Host, cfg.Port, cfg.AuthDir, cfg.APIKeys = "127.0.0.1", port, authDir, []string{inferenceKey}
	cfg.CommercialMode, cfg.MaxRetryCredentials, cfg.WebsocketAuth = true, 1, true
	// AO names the exact account for every request; the SDK must not choose one itself.
	cfg.Routing.Strategy, cfg.Routing.SessionAffinity = "fill-first", false
	// The SDK's management stays off, including a secret an earlier helper saved: it comes with a Redis listener on this port
	// whose failed sign-ins ban 127.0.0.1, the daemon's address. The boundary serves AO's management calls itself.
	cfg.RemoteManagement.DisableControlPanel, cfg.RemoteManagement.DisableAutoUpdatePanel, cfg.RemoteManagement.SecretKey = true, true, ""
	data, err := yaml.Marshal(cfg)
	if err == nil {
		err = writePrivate(path, data)
	}
	if err != nil {
		return nil, err
	}
	b := Boundary{Routes: routes, ControlKey: controlKey, InferenceKey: inferenceKey, ConfigPath: path, BuildID: build, Stop: stop, Logins: newLogins(authDir), Activity: activity}
	coreusage.RegisterNamedPlugin("ao-activity", activity)
	// The SDK runs an added middleware after its logger, which gives a request the id that bind goes by.
	return cliproxy.NewBuilder().WithConfig(cfg).WithConfigPath(path).WithResultPolicy(coreauth.ResultPolicyFunc(requestRefusal)).
		WithServerOptions(proxyapi.WithEngineConfigurator(func(e *gin.Engine) { e.Use(b.Middleware) }), proxyapi.WithMiddleware(activity.bind), proxyapi.WithRouterConfigurator(b.Configure)).Build()
}

// replacedCheck is how often a helper looks whether its identity is still the one the daemon keeps.
var replacedCheck = 10 * time.Second

// replaced reports that the daemon's identity file names another port or control key than this helper's.
// None, or one cut short, replaces nothing: the daemon that writes the file decides.
func replaced(root string, port int, controlKey string) bool {
	var id struct {
		Port       int    `json:"port"`
		ControlKey string `json:"control_key"`
	}
	data, err := os.ReadFile(filepath.Join(root, "run", "host.json"))
	if err != nil || json.Unmarshal(data, &id) != nil || id.Port == 0 || id.ControlKey == "" {
		return false
	}
	return id.Port != port || subtle.ConstantTimeCompare([]byte(id.ControlKey), []byte(controlKey)) != 1
}

// Run serves until ctx ends or the daemon asks the helper to stop; either is a clean end.
func Run(ctx context.Context, root string, port int, controlKey, inferenceKey, build string) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	activity, routes := OpenActivity(filepath.Join(root, "run", "activity.json")), OpenRoutes(filepath.Join(root, "run", "routes.json"))
	service, err := Build(root, port, controlKey, inferenceKey, build, routes, activity, stop)
	if err != nil {
		return err
	}
	// Saved every half minute when something changed, and once when the service stops.
	defer activity.save()
	go func() {
		for range time.Tick(30 * time.Second) {
			activity.save()
		}
	}()
	// A helper whose identity a daemon replaced serves nobody AO controls: it stops once idle, so two never share root.
	every := replacedCheck
	go func() {
		for range time.Tick(every) {
			if replaced(root, port, controlKey) && !routes.Busy() {
				stop()
				return
			}
		}
	}()
	if err = service.Run(ctx); ctx.Err() != nil {
		return nil
	}
	return err
}
func tagLogin(ctx context.Context, a *coreauth.Auth) error {
	if info := coreauth.GetRequestInfo(ctx); info != nil && info.Headers.Get("X-AO-Login-ID") != "" && len(info.Headers.Get("X-AO-Login-ID")) <= 128 {
		if a.Metadata == nil {
			a.Metadata = map[string]any{}
		}
		a.Metadata["ao_login_id"] = info.Headers.Get("X-AO-Login-ID")
	}
	return nil
}
