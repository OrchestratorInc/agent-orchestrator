package opencodev2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

const (
	projectConfigFileName = "opencode.json"
	serverStateFileName   = "opencode-v2-server.json"
	serverStartTimeout    = 20 * time.Second
	serverProbeTimeout    = 2 * time.Second
)

// sharedServer is one OpenCode 2 server that AO owns and reuses for every
// worker launch. It runs with AO's isolated OpenCode 2 data home, so sessions,
// credentials, and worktrees stay separate from the user's own OpenCode data.
type sharedServer struct {
	url      string
	password string
}

type serverHandle struct {
	server sharedServer
	cmd    *exec.Cmd
	done   chan struct{}
}

type persistedServer struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

var sharedServerRegistry = struct {
	mu    sync.Mutex
	byDir map[string]*serverHandle
}{byDir: map[string]*serverHandle{}}

// prepareSharedServer writes the session's project config and returns the AO
// server that launch should attach to. OpenCode 2 only resolves per-session
// config from the project directory when a client attaches to a shared server,
// so the config has to live in the worktree instead of the launch environment.
func prepareSharedServer(ctx context.Context, binary string, cfg ports.LaunchConfig, dataHome, content string) (sharedServer, error) {
	if cfg.DataDir == "" || cfg.WorkspacePath == "" {
		return sharedServer{}, errors.New("opencode-v2: shared server needs a data dir and workspace")
	}
	created, err := writeProjectConfig(cfg.WorkspacePath, content)
	if err != nil {
		return sharedServer{}, err
	}
	server, err := ensureSharedServer(ctx, binary, cfg.DataDir, dataHome)
	if err != nil {
		if created {
			removeProjectConfig(cfg.WorkspacePath, content)
		}
		return sharedServer{}, err
	}
	return server, nil
}

// providerEnvDrift reports whether the launch's provider credentials differ
// from the daemon environment. A shared server inherits the daemon environment
// and the attaching client forwards only its password, so a session whose keys
// differ must keep a private server that starts with cfg.Env.
func providerEnvDrift(cfgEnv map[string]string) bool {
	for name, value := range cfgEnv {
		if providerkeys.Checkable(name) && os.Getenv(name) != value {
			return true
		}
	}
	return false
}

func sharedServerEnv(dataHome string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "XDG_DATA_HOME", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CONFIG_DIR", "OPENCODE_PASSWORD", "OPENCODE_SERVER_PASSWORD":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "XDG_DATA_HOME="+dataHome)
}

func ensureSharedServer(ctx context.Context, binary, dataDir, dataHome string) (sharedServer, error) {
	if err := ctx.Err(); err != nil {
		return sharedServer{}, err
	}
	key := dataDir
	if key == "" {
		key = dataHome
	}

	sharedServerRegistry.mu.Lock()
	defer sharedServerRegistry.mu.Unlock()

	if handle, ok := sharedServerRegistry.byDir[key]; ok {
		if serverHealthy(handle.server.url, handle.server.password) {
			return handle.server, nil
		}
		if handle.cmd != nil {
			// This process still owns a server for the data dir. Keep the handle
			// so a later launch can reuse it if it recovers, and never start a
			// second server while the first one runs: both would write to the
			// same data home. This launch falls back to a private server.
			return sharedServer{}, errors.New("opencode-v2: shared server is running but not healthy")
		}
		delete(sharedServerRegistry.byDir, key)
	}
	if dataDir != "" {
		if persisted, ok := readPersistedServer(dataDir); ok && serverHealthy(persisted.URL, persisted.Password) {
			server := sharedServer{url: persisted.URL, password: persisted.Password}
			sharedServerRegistry.byDir[key] = &serverHandle{server: server}
			return server, nil
		}
	}

	port, err := freePort()
	if err != nil {
		return sharedServer{}, fmt.Errorf("opencode-v2: pick server port: %w", err)
	}
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	output := &serverOutput{}
	cmd := aoprocess.Command(binary, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Env = sharedServerEnv(dataHome)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return sharedServer{}, fmt.Errorf("opencode-v2: start shared server: %w", err)
	}
	done := make(chan struct{})
	handle := &serverHandle{cmd: cmd, done: done}
	go func() {
		_ = cmd.Wait()
		close(done)
		sharedServerRegistry.mu.Lock()
		if sharedServerRegistry.byDir[key] == handle {
			delete(sharedServerRegistry.byDir, key)
		}
		sharedServerRegistry.mu.Unlock()
	}()

	password, err := waitForSharedServer(ctx, url, output, done)
	if err != nil {
		_ = cmd.Process.Kill()
		return sharedServer{}, err
	}
	server := sharedServer{url: url, password: password}
	handle.server = server
	sharedServerRegistry.byDir[key] = handle
	if dataDir != "" {
		persistServer(dataDir, server)
	}
	return server, nil
}

func waitForSharedServer(ctx context.Context, url string, output *serverOutput, done <-chan struct{}) (string, error) {
	deadline := time.Now().Add(serverStartTimeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-done:
			return "", errors.New("opencode-v2: shared server exited during startup")
		case <-ticker.C:
			if password := output.Password(); password != "" && serverHealthy(url, password) {
				return password, nil
			}
			if time.Now().After(deadline) {
				return "", errors.New("opencode-v2: shared server did not become ready")
			}
		}
	}
}

func serverHealthy(url, password string) bool {
	client := &http.Client{Timeout: serverProbeTimeout}
	req, err := http.NewRequest(http.MethodGet, url+"/api/info", http.NoBody)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", password)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	return resp.StatusCode == http.StatusOK
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("opencode-v2: unexpected listener address")
	}
	return address.Port, nil
}

// serverOutput keeps the server's startup password line. Everything else the
// server prints is discarded so a long-lived process cannot grow a buffer.
type serverOutput struct {
	mu       sync.Mutex
	line     strings.Builder
	password string
}

func (o *serverOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			o.consumeLocked(o.line.String())
			o.line.Reset()
			continue
		}
		if o.line.Len() < 4096 {
			o.line.WriteByte(b)
		}
	}
	return len(p), nil
}

func (o *serverOutput) consumeLocked(line string) {
	password, ok := strings.CutPrefix(strings.TrimSpace(line), "server password ")
	if !ok {
		return
	}
	if password = strings.TrimSpace(password); password != "" {
		o.password = password
	}
}

func (o *serverOutput) Password() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.password
}

func readPersistedServer(dataDir string) (persistedServer, bool) {
	data, err := os.ReadFile(filepath.Join(dataDir, serverStateFileName))
	if err != nil {
		return persistedServer{}, false
	}
	var persisted persistedServer
	if err := json.Unmarshal(data, &persisted); err != nil {
		return persistedServer{}, false
	}
	if strings.TrimSpace(persisted.URL) == "" || strings.TrimSpace(persisted.Password) == "" {
		return persistedServer{}, false
	}
	return persisted, true
}

func persistServer(dataDir string, server sharedServer) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return
	}
	//nolint:gosec // G117: this 0600 file exists so the daemon can reattach to its own loopback server; the value is never logged.
	data, err := json.Marshal(persistedServer{URL: server.url, Password: server.password})
	if err != nil {
		return
	}
	_ = hookutil.AtomicWriteFile(filepath.Join(dataDir, serverStateFileName), data, 0o600)
}

// writeProjectConfig writes the same config document the private-server path
// passes inline. It never replaces a project config AO did not write itself:
// a conflicting file makes the launch fall back to a private server instead.
func writeProjectConfig(workspacePath, content string) (bool, error) {
	if content == "" {
		return false, nil
	}
	dir := filepath.Join(workspacePath, ".opencode")
	path := filepath.Join(dir, projectConfigFileName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("opencode-v2: create project config dir: %w", err)
	}
	// O_EXCL creates the file without replacing one a concurrent writer added
	// after the absence check. Such a file is still compared, so an identical
	// config can be shared and a differing one falls back to a private server.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, fmt.Errorf("opencode-v2: read project config: %w", readErr)
		}
		if string(existing) == content {
			return false, nil
		}
		return false, errors.New("opencode-v2: workspace already has .opencode/opencode.json")
	}
	if err != nil {
		return false, fmt.Errorf("opencode-v2: create project config: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return false, fmt.Errorf("opencode-v2: write project config: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("opencode-v2: write project config: %w", err)
	}
	return true, nil
}

// removeProjectConfig deletes the config only while it still holds exactly the
// content this startup attempt wrote. A file the user or another tool replaced
// or edited during the startup wait is left in place.
func removeProjectConfig(workspacePath, content string) {
	path := filepath.Join(workspacePath, ".opencode", projectConfigFileName)
	existing, err := os.ReadFile(path)
	if err != nil || string(existing) != content {
		return
	}
	_ = os.Remove(path)
}
