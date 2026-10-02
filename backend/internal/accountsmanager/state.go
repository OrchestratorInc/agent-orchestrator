package accountsmanager

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	stateDirectoryName    = "accounts-manager"
	configFileName        = "config.yaml"
	controlKeyFileName    = "control.key"
	managementKeyFileName = "management.key"
	routingKeyFileName    = "routing.key"
	runtimeFileName       = "runtime.json"
)

type privateState struct {
	Root          string
	ConfigPath    string
	ControlKey    string
	ClientKey     string
	ManagementKey string
	RoutingKey    string
	Port          int
}

type engineConfig struct {
	Host       string   `yaml:"host"`
	Port       int      `yaml:"port"`
	AuthDir    string   `yaml:"auth-dir"`
	APIKeys    []string `yaml:"api-keys"`
	RequestLog bool     `yaml:"request-log"`

	LoggingToFile         bool `yaml:"logging-to-file"`
	UsageStatisticsEnable bool `yaml:"usage-statistics-enabled"`

	RemoteManagement struct {
		AllowRemote            bool   `yaml:"allow-remote"`
		SecretKey              string `yaml:"secret-key"`
		DisableControlPanel    bool   `yaml:"disable-control-panel"`
		DisableAutoUpdatePanel bool   `yaml:"disable-auto-update-panel"`
	} `yaml:"remote-management"`
	Plugins struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"plugins"`
	Pprof struct {
		Enable bool `yaml:"enable"`
	} `yaml:"pprof"`
	Discovery struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"discovery"`
}

// RuntimeRecord intentionally mirrors the runner's non-secret rendezvous file.
type RuntimeRecord struct {
	PID             int    `json:"pid"`
	Port            int    `json:"port"`
	InstanceID      string `json:"instanceId"`
	RunnerVersion   string `json:"runnerVersion"`
	UpstreamVersion string `json:"upstreamVersion"`
}

func ensureState(stateDir string) (privateState, error) {
	if !filepath.IsAbs(stateDir) {
		return privateState{}, fmt.Errorf("AO state directory must be absolute")
	}
	root := filepath.Join(filepath.Clean(stateDir), stateDirectoryName)
	if err := ensurePrivateDirectory(root); err != nil {
		return privateState{}, fmt.Errorf("accounts manager state: %w", err)
	}
	authDir := filepath.Join(root, "auth")
	if err := ensurePrivateDirectory(authDir); err != nil {
		return privateState{}, fmt.Errorf("accounts manager auth directory: %w", err)
	}

	controlKey, err := ensurePrivateKey(filepath.Join(root, controlKeyFileName))
	if err != nil {
		return privateState{}, fmt.Errorf("accounts manager control key: %w", err)
	}
	managementKey, err := ensurePrivateKey(filepath.Join(root, managementKeyFileName))
	if err != nil {
		return privateState{}, fmt.Errorf("accounts manager management key: %w", err)
	}
	routingKey, err := ensurePrivateKey(filepath.Join(root, routingKeyFileName))
	if err != nil {
		return privateState{}, fmt.Errorf("accounts manager routing key: %w", err)
	}
	configPath := filepath.Join(root, configFileName)
	cfg, err := loadOrCreateConfig(configPath, authDir)
	if err != nil {
		return privateState{}, err
	}
	return privateState{
		Root:          root,
		ConfigPath:    configPath,
		ControlKey:    controlKey,
		ClientKey:     cfg.APIKeys[0],
		ManagementKey: managementKey,
		RoutingKey:    routingKey,
		Port:          cfg.Port,
	}, nil
}

func loadOrCreateConfig(path, authDir string) (engineConfig, error) {
	b, err := readPrivateFile(path)
	if os.IsNotExist(err) {
		port, portErr := selectAvailablePort(0)
		if portErr != nil {
			return engineConfig{}, fmt.Errorf("select accounts manager port: %w", portErr)
		}
		clientKey, keyErr := randomKey()
		if keyErr != nil {
			return engineConfig{}, keyErr
		}
		cfg := newEngineConfig(authDir, port, clientKey)
		encoded, marshalErr := yaml.Marshal(cfg) // #nosec G117 -- Private runner configuration is written owner-only below.
		if marshalErr != nil {
			return engineConfig{}, fmt.Errorf("encode accounts manager configuration: %w", marshalErr)
		}
		if writeErr := writePrivateAtomic(path, encoded); writeErr != nil {
			return engineConfig{}, fmt.Errorf("write accounts manager configuration: %w", writeErr)
		}
		return cfg, nil
	}
	if err != nil {
		return engineConfig{}, fmt.Errorf("read accounts manager configuration: %w", err)
	}
	var cfg engineConfig
	if err = yaml.Unmarshal(b, &cfg); err != nil {
		return engineConfig{}, fmt.Errorf("parse accounts manager configuration: %w", err)
	}
	if err := validateEngineConfig(cfg, authDir); err != nil {
		return engineConfig{}, err
	}
	return cfg, nil
}

func newEngineConfig(authDir string, port int, clientKey string) engineConfig {
	cfg := engineConfig{
		Host:       "127.0.0.1",
		Port:       port,
		AuthDir:    authDir,
		APIKeys:    []string{clientKey},
		RequestLog: false,
	}
	cfg.RemoteManagement.DisableControlPanel = true
	cfg.RemoteManagement.DisableAutoUpdatePanel = true
	return cfg
}

func validateEngineConfig(cfg engineConfig, authDir string) error {
	if cfg.Host != "127.0.0.1" || cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("accounts manager configuration is not loopback-only")
	}
	if filepath.Clean(cfg.AuthDir) != filepath.Clean(authDir) {
		return fmt.Errorf("accounts manager configuration has an invalid auth directory")
	}
	if len(cfg.APIKeys) != 1 || strings.TrimSpace(cfg.APIKeys[0]) == "" {
		return fmt.Errorf("accounts manager configuration has an invalid client key")
	}
	if cfg.RequestLog || cfg.LoggingToFile || cfg.UsageStatisticsEnable {
		return fmt.Errorf("accounts manager diagnostic logging must remain disabled")
	}
	if cfg.RemoteManagement.AllowRemote || cfg.RemoteManagement.SecretKey != "" || !cfg.RemoteManagement.DisableControlPanel || !cfg.RemoteManagement.DisableAutoUpdatePanel {
		return fmt.Errorf("accounts manager remote management must remain disabled")
	}
	if cfg.Plugins.Enabled || cfg.Pprof.Enable || cfg.Discovery.Enabled {
		return fmt.Errorf("accounts manager optional services must remain disabled")
	}
	return nil
}

func updateConfigPort(path string, port int) error {
	b, err := readPrivateFile(path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := yaml.Unmarshal(b, &document); err != nil {
		return err
	}
	document["port"] = port
	b, err = yaml.Marshal(document)
	if err != nil {
		return err
	}
	return writePrivateAtomic(path, b)
}

func readRuntimeRecord(root string) (RuntimeRecord, error) {
	b, err := readPrivateFile(filepath.Join(root, runtimeFileName))
	if err != nil {
		return RuntimeRecord{}, err
	}
	var record RuntimeRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return RuntimeRecord{}, err
	}
	if record.Port < 1 || record.Port > 65535 || strings.TrimSpace(record.InstanceID) == "" {
		return RuntimeRecord{}, fmt.Errorf("invalid runtime record")
	}
	return record, nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		return nil
	}
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

func ensurePrivateKey(path string) (string, error) {
	b, err := readPrivateFile(path)
	if err == nil {
		key := strings.TrimSpace(string(b))
		if key == "" {
			return "", fmt.Errorf("key is empty")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	key, err := randomKey()
	if err != nil {
		return "", err
	}
	if err = writePrivateExclusive(path, []byte(key+"\n")); err != nil {
		if os.IsExist(err) {
			return ensurePrivateKey(path)
		}
		return "", err
	}
	return key, nil
}

func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate private key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func readPrivateFile(path string) ([]byte, error) {
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
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("file changed while it was inspected")
	}
	return io.ReadAll(file)
}

func writePrivateExclusive(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writePrivateAtomic(path string, b []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("target must be a regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("target permissions must be private")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".accounts-manager-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func selectAvailablePort(preferred int) (int, error) {
	if preferred > 0 {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(preferred)))
		if err == nil {
			if err := listener.Close(); err != nil {
				return 0, fmt.Errorf("release preferred port: %w", err)
			}
			return preferred, nil
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("release available port: %w", err)
	}
	if !ok {
		return 0, fmt.Errorf("available port has an unexpected address type")
	}
	return address.Port, nil
}
