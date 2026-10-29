package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserchrome"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
)

// chromeMode wires the Chrome extension runtime: its broker, the per-session
// target store, and the native-host installer.
type chromeMode struct {
	broker    *browserruntime.Broker
	token     string
	targets   *browsersvc.TargetStore
	installer browserchrome.Installer
	log       *slog.Logger
}

// newChromeMode prepares Chrome mode. It returns nil (and logs) when the
// daemon cannot support it; the panel runtime keeps working either way.
func newChromeMode(cfg config.Config, log *slog.Logger) *chromeMode {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		log.Warn("browser chrome: executable unknown; Chrome mode disabled", "err", err)
		return nil
	}
	targets, err := browsersvc.OpenTargetStore(filepath.Join(cfg.DataDir, "browser-targets.json"))
	if err != nil {
		log.Warn("browser chrome: target store unavailable; Chrome mode disabled", "err", err)
		return nil
	}
	token, err := browserruntime.NewToken()
	if err != nil {
		log.Warn("browser chrome: token unavailable; Chrome mode disabled", "err", err)
		return nil
	}
	broker := browserruntime.New(log, token)
	broker.SetTransport(browserchrome.Transport)
	broker.SetExclusive(true)
	broker.SetPeerVerifier(browserchrome.PeerVerifier(executable))
	mode := &chromeMode{
		broker:  broker,
		token:   token,
		targets: targets,
		installer: browserchrome.Installer{
			Paths:           browserchrome.NewPaths(cfg.DataDir),
			Executable:      executable,
			ExtensionSource: browserchrome.DefaultExtensionSource(executable),
		},
		log: log,
	}
	return mode
}

// serve listens for the extension's native host and publishes the endpoint
// file the host reads. It returns once the listener is up.
func (m *chromeMode) serve(ctx context.Context, runFilePath string) error {
	ln, addr, err := browserruntime.ListenNamed(runFilePath, "chrome")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	endpointPath := m.installer.Paths.Endpoint()
	if err := browserchrome.WriteEndpoint(endpointPath, browserchrome.Endpoint{
		Address:  addr,
		Token:    m.token,
		PID:      os.Getpid(),
		Protocol: browserruntime.ProtocolVersion,
	}); err != nil {
		_ = ln.Close()
		return fmt.Errorf("publish endpoint: %w", err)
	}
	m.log.Info("browser chrome: listening", "addr", addr)
	go func() {
		defer browserchrome.RemoveEndpoint(endpointPath, os.Getpid())
		if err := m.broker.Serve(ctx, ln); err != nil {
			m.log.Warn("browser chrome: serve stopped with error", "err", err)
		}
	}()
	// Keep an enabled registration pointing at this daemon's binary and the
	// extension bundled with it, so app updates and moves stay connected.
	go func() {
		if err := m.installer.Refresh(); err != nil {
			m.log.Warn("browser chrome: refresh registration", "err", err)
		}
	}()
	return nil
}

// lifecycle tears down sessions in both runtimes.
func (m *chromeMode) lifecycle(panel *browserruntime.Broker) browsersvc.Lifecycle {
	return browsersvc.Lifecycle{Panel: panel, Chrome: m.broker, Targets: m.targets}
}

// ChromeStatus implements controllers.BrowserChromeService.
func (m *chromeMode) ChromeStatus() controllers.BrowserChromeStatusResponse {
	return m.response(m.installer.Status())
}

// SetupChrome implements controllers.BrowserChromeService.
func (m *chromeMode) SetupChrome() (controllers.BrowserChromeStatusResponse, error) {
	status, err := m.installer.Setup()
	if err == nil {
		m.log.Info("browser chrome: enabled", "extensionPath", status.ExtensionPath)
	}
	return m.response(status), err
}

// RemoveChrome implements controllers.BrowserChromeService.
func (m *chromeMode) RemoveChrome() (controllers.BrowserChromeStatusResponse, error) {
	status, err := m.installer.Remove()
	if err == nil {
		m.log.Info("browser chrome: disabled")
	}
	return m.response(status), err
}

func (m *chromeMode) response(status browserchrome.Status) controllers.BrowserChromeStatusResponse {
	runtime := m.broker.Status()
	channels := make([]controllers.BrowserChromeChannel, 0, len(status.Channels))
	for _, channel := range status.Channels {
		channels = append(channels, controllers.BrowserChromeChannel(channel))
	}
	return controllers.BrowserChromeStatusResponse{
		Enabled:          status.Enabled,
		Connected:        runtime.Connected,
		ExtensionID:      status.ExtensionID,
		ExtensionPath:    status.ExtensionPath,
		ExtensionBundled: status.ExtensionBundled,
		ExtensionVersion: status.ExtensionVersion,
		ClientVersion:    runtime.ClientVersion,
		HostPath:         status.HostPath,
		Channels:         channels,
	}
}
