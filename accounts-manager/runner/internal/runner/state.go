package runner

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

const (
	configFileName    = "config.yaml"
	controlKeyName    = "control.key"
	managementKeyName = "management.key"
	routingKeyName    = "routing.key"
	authDirName       = "auth"
)

// State is the validated private runtime configuration owned by AO.
type State struct {
	Root          string
	ConfigPath    string
	ControlKey    string
	ManagementKey string
	RoutingKey    []byte
	Config        *sdkconfig.Config
}

// LoadState loads the AO-owned runner state without accepting paths outside the
// requested state root or files that can be redirected through symlinks.
func LoadState(root string) (*State, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("state directory must be absolute")
	}
	root = filepath.Clean(root)
	if err := requirePrivateDirectory(root); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}

	authDir := filepath.Join(root, authDirName)
	if err := requirePrivateDirectory(authDir); err != nil {
		return nil, fmt.Errorf("auth directory: %w", err)
	}

	controlPath := filepath.Join(root, controlKeyName)
	controlBytes, err := readPrivateRegularFile(controlPath)
	if err != nil {
		return nil, fmt.Errorf("control key: %w", err)
	}
	controlKey := strings.TrimSpace(string(controlBytes))
	if controlKey == "" {
		return nil, fmt.Errorf("control key is empty")
	}
	managementPath := filepath.Join(root, managementKeyName)
	managementBytes, err := readPrivateRegularFile(managementPath)
	if err != nil {
		return nil, fmt.Errorf("management key: %w", err)
	}
	managementKey := strings.TrimSpace(string(managementBytes))
	if managementKey == "" {
		return nil, fmt.Errorf("management key is empty")
	}
	routingBytes, err := readPrivateRegularFile(filepath.Join(root, routingKeyName))
	if err != nil {
		return nil, fmt.Errorf("routing key: %w", err)
	}
	routingKey, err := hex.DecodeString(strings.TrimSpace(string(routingBytes)))
	if err != nil || len(routingKey) != 32 {
		return nil, fmt.Errorf("routing key is invalid")
	}

	configPath := filepath.Join(root, configFileName)
	configBytes, err := readPrivateRegularFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("configuration: %w", err)
	}
	cfg, err := sdkconfig.ParseConfigBytes(configBytes)
	if err != nil {
		return nil, fmt.Errorf("load configuration: invalid private configuration")
	}
	if err := validateConfig(cfg, authDir); err != nil {
		return nil, err
	}

	return &State{
		Root:          root,
		ConfigPath:    configPath,
		ControlKey:    controlKey,
		ManagementKey: managementKey,
		RoutingKey:    routingKey,
		Config:        cfg,
	}, nil
}

func validateConfig(cfg *sdkconfig.Config, authDir string) error {
	if cfg == nil {
		return fmt.Errorf("configuration is empty")
	}
	if cfg.Host != "127.0.0.1" {
		return fmt.Errorf("configuration must use the IPv4 loopback host")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("configuration port is invalid")
	}
	if filepath.Clean(cfg.AuthDir) != filepath.Clean(authDir) {
		return fmt.Errorf("configuration auth directory must be inside the state directory")
	}
	if cfg.TLS.Enable {
		return fmt.Errorf("configuration must not enable TLS")
	}
	if cfg.RemoteManagement.AllowRemote || strings.TrimSpace(cfg.RemoteManagement.SecretKey) != "" || !cfg.RemoteManagement.DisableControlPanel || !cfg.RemoteManagement.DisableAutoUpdatePanel {
		return fmt.Errorf("configuration must keep remote management disabled")
	}
	if cfg.Plugins.Enabled {
		return fmt.Errorf("configuration must keep plugins disabled")
	}
	if cfg.Pprof.Enable {
		return fmt.Errorf("configuration must keep pprof disabled")
	}
	if cfg.Discovery.Enabled {
		return fmt.Errorf("configuration must keep discovery disabled")
	}
	if cfg.RequestLog {
		return fmt.Errorf("configuration must keep request logging disabled")
	}
	if cfg.LoggingToFile {
		return fmt.Errorf("configuration must keep file logging disabled")
	}
	if cfg.UsageStatisticsEnabled {
		return fmt.Errorf("configuration must keep usage statistics disabled")
	}
	if len(cfg.APIKeys) != 1 || strings.TrimSpace(cfg.APIKeys[0]) == "" {
		return fmt.Errorf("configuration must contain exactly one internal client key")
	}
	return nil
}

func requirePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("must be a regular directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("permissions must not grant group or other access")
	}
	return nil
}

func readPrivateRegularFile(path string) (data []byte, resultErr error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("permissions must not grant group or other access")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil && resultErr == nil {
			clear(data)
			data, resultErr = nil, errCredentialStorage
		}
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("file changed while it was inspected")
	}
	return io.ReadAll(file)
}
