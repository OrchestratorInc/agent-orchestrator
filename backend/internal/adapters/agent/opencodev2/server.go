package opencodev2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
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
)

const (
	projectConfigFileName    = "opencode.json"
	projectConfigTempPattern = ".ao-tmp-*"
	projectConfigTempProbe   = ".ao-tmp-probe"
	serverStateFileName      = "opencode-v2-server.json"
	serverStartTimeout       = 20 * time.Second
	serverProbeTimeout       = 2 * time.Second
	serverDialTimeout        = 500 * time.Millisecond
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
	if cfg.DataDir == "" || cfg.WorkspacePath == "" || cfg.SessionID == "" {
		return sharedServer{}, errors.New("opencode-v2: shared server needs a data dir, workspace, and session id")
	}
	// Serialize preparations for one worktree so two launches cannot publish
	// different config documents or inspect each other's half-written state.
	release := lockWorkspacePreparation(cfg.WorkspacePath)
	defer release()
	writeConfig, err := checkProjectConfig(cfg.WorkspacePath, content)
	if err != nil {
		return sharedServer{}, err
	}
	if writeConfig {
		// Refuse before a server starts, so the launch can still fall back to a
		// private server without leaving a file Git would show.
		if err := projectConfigPublishable(ctx, cfg.WorkspacePath); err != nil {
			return sharedServer{}, err
		}
	}
	// Start the server before publishing the config. A failed startup then
	// leaves no AO-owned file behind, so no rollback has to decide whether a
	// file still belongs to this attempt.
	server, err := ensureSharedServer(ctx, binary, cfg.DataDir, dataHome)
	if err != nil {
		return sharedServer{}, err
	}
	if content == "" {
		return server, nil
	}
	// Startup can take seconds. Re-read the config afterwards: an editor, a
	// branch switch, or another checkout may have changed the file or the
	// ignore rules the early check saw. The server now owns the data home, so a
	// conflict has to fail the launch instead of falling back privately.
	if err := revalidateProjectConfig(ctx, cfg.WorkspacePath, content); err != nil {
		return sharedServer{}, fmt.Errorf("%w: %w", errSharedServerConfig, err)
	}
	return server, nil
}

// revalidateProjectConfig makes the worktree config match content after the
// server start, when the first check may be stale. It reuses an identical file,
// publishes an absent one only while Git still ignores an untracked path, and
// fails on a conflicting file.
func revalidateProjectConfig(ctx context.Context, workspacePath, content string) error {
	writeConfig, err := checkProjectConfig(workspacePath, content)
	if err != nil {
		return err
	}
	if !writeConfig {
		return nil
	}
	if err := projectConfigPublishable(ctx, workspacePath); err != nil {
		return err
	}
	_, err = writeProjectConfig(workspacePath, content)
	return err
}

var (
	// errSharedServerUnavailable means a server may still own the data home but
	// cannot serve this launch. Falling back to a private server would start a
	// second process against the same data home, so the launch must fail.
	errSharedServerUnavailable = errors.New("opencode-v2: shared server is running but not healthy")
	// errSharedServerConfig means the shared server is running but this attempt
	// could not publish its project config. A private fallback would again share
	// the data home, so the launch must fail.
	errSharedServerConfig = errors.New("opencode-v2: shared server project config could not be published")
)

// checkProjectConfig reports whether content needs to be written. A different
// existing file is refused before a server starts, so the launch can safely
// fall back to a private server without touching the file.
func checkProjectConfig(workspacePath, content string) (bool, error) {
	if content == "" {
		return false, nil
	}
	path := filepath.Join(workspacePath, ".opencode", projectConfigFileName)
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("opencode-v2: read project config: %w", err)
	}
	if string(existing) == content {
		return false, nil
	}
	return false, errors.New("opencode-v2: workspace already has .opencode/opencode.json")
}

// sharedServerEnvDrift reports whether the launch environment differs from the
// daemon environment in a way the server can observe. A shared server inherits
// the daemon environment and the attaching client forwards only its password,
// so a session with any other override must keep a private server that starts
// with cfg.Env. AO-owned variables and the shell plumbing AO pins for its hooks
// do not change OpenCode's provider resolution and are excluded.
func sharedServerEnvDrift(cfgEnv map[string]string) bool {
	for name, value := range cfgEnv {
		if !sharedServerRelevantEnv(name) {
			continue
		}
		if os.Getenv(name) != value {
			return true
		}
	}
	return false
}

func sharedServerRelevantEnv(name string) bool {
	if strings.HasPrefix(name, "AO_") {
		return false
	}
	switch name {
	case "PATH", "ZDOTDIR", "BASH_ENV", "PROMPT_COMMAND":
		return false
	}
	return true
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
			return sharedServer{}, errSharedServerUnavailable
		}
		// A reattached handle has no process to inspect. Only replace it after
		// the endpoint itself stops listening; a listening but unhealthy server
		// may still own the data home and must not get a duplicate.
		if serverListening(handle.server.url) {
			return sharedServer{}, fmt.Errorf("%w: endpoint is listening but not healthy", errSharedServerUnavailable)
		}
		delete(sharedServerRegistry.byDir, key)
	}
	if dataDir != "" {
		if persisted, ok := readPersistedServer(dataDir); ok {
			server := sharedServer{url: persisted.URL, password: persisted.Password}
			if serverHealthy(server.url, server.password) {
				sharedServerRegistry.byDir[key] = &serverHandle{server: server}
				return server, nil
			}
			if serverListening(server.url) {
				sharedServerRegistry.byDir[key] = &serverHandle{server: server}
				return sharedServer{}, fmt.Errorf("%w: persisted endpoint is listening but not healthy", errSharedServerUnavailable)
			}
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

// serverListening reports whether something accepts TCP connections at the
// server URL. A timeout counts as listening: it cannot prove the process is
// gone, and starting a second server is worse than falling back privately.
func serverListening(rawURL string) bool {
	parsed, err := neturl.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, serverDialTimeout)
	if err != nil {
		var netErr net.Error
		return errors.As(err, &netErr) && netErr.Timeout()
	}
	_ = conn.Close()
	return true
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
// passes inline. It stages the content under an ignored temp name and publishes
// it with a hard link, so the config appears complete rather than partial and
// the link fails instead of replacing another writer's file.
func writeProjectConfig(workspacePath, content string) (bool, error) {
	if content == "" {
		return false, nil
	}
	dir := filepath.Join(workspacePath, ".opencode")
	path := filepath.Join(dir, projectConfigFileName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("opencode-v2: create project config dir: %w", err)
	}
	// projectConfigPublishable proved the temp pattern is ignored, so a
	// concurrent `git add -A` cannot stage the staging file.
	file, err := os.CreateTemp(dir, projectConfigTempPattern)
	if err != nil {
		return false, fmt.Errorf("opencode-v2: create project config temp file: %w", err)
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("opencode-v2: write project config: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("opencode-v2: sync project config: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("opencode-v2: close project config: %w", err)
	}
	if err := os.Link(temp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(path) //nolint:gosec // path is rooted in the caller-owned worktree
			if readErr != nil {
				return false, fmt.Errorf("opencode-v2: read project config: %w", readErr)
			}
			if string(existing) == content {
				return false, nil
			}
			return false, errors.New("opencode-v2: workspace already has .opencode/opencode.json")
		}
		return false, fmt.Errorf("opencode-v2: publish project config: %w", err)
	}
	return true, nil
}

// projectConfigPublishable reports whether Git ignores the config and the temp
// name it is staged under, and does not track the config. check-ignore
// --no-index evaluates the ignore rules even for a path that does not exist
// yet; ls-files then rejects a tracked path, because Git does not apply ignore
// rules to tracked files and recreating one would surface AO's content as a
// modification.
func projectConfigPublishable(ctx context.Context, workspacePath string) error {
	rel := filepath.ToSlash(filepath.Join(".opencode", projectConfigFileName))
	if err := requireIgnored(ctx, workspacePath, rel); err != nil {
		return err
	}
	if err := requireIgnored(ctx, workspacePath, filepath.ToSlash(filepath.Join(".opencode", projectConfigTempProbe))); err != nil {
		return err
	}
	// Any output means the path is tracked. A Git failure must not read as
	// "untracked": with a corrupt index ls-files exits non-zero and says
	// nothing about the path, so publication fails closed instead.
	tracked := aoprocess.CommandContext(ctx, "git", "-C", workspacePath, "ls-files", "-z", "--", rel)
	var trackedOut strings.Builder
	tracked.Stdout = &trackedOut
	tracked.Stderr = io.Discard
	if err := tracked.Run(); err != nil {
		return fmt.Errorf("opencode-v2: list tracked project config: %w", err)
	}
	if trackedOut.Len() > 0 {
		return errors.New("opencode-v2: workspace tracks .opencode/opencode.json")
	}
	return nil
}

func requireIgnored(ctx context.Context, workspacePath, rel string) error {
	ignore := aoprocess.CommandContext(ctx, "git", "-C", workspacePath, "check-ignore", "-q", "--no-index", "--", rel)
	if ignore.Run() != nil {
		return fmt.Errorf("opencode-v2: workspace does not ignore %s", rel)
	}
	return nil
}

type workspacePreparation struct {
	mu   sync.Mutex
	refs int
}

var workspacePreparations = struct {
	mu    sync.Mutex
	byDir map[string]*workspacePreparation
}{byDir: map[string]*workspacePreparation{}}

func lockWorkspacePreparation(workspacePath string) func() {
	key := filepath.Clean(workspacePath)
	workspacePreparations.mu.Lock()
	preparation := workspacePreparations.byDir[key]
	if preparation == nil {
		preparation = &workspacePreparation{}
		workspacePreparations.byDir[key] = preparation
	}
	preparation.refs++
	workspacePreparations.mu.Unlock()

	preparation.mu.Lock()
	return func() {
		preparation.mu.Unlock()
		workspacePreparations.mu.Lock()
		preparation.refs--
		if preparation.refs == 0 {
			delete(workspacePreparations.byDir, key)
		}
		workspacePreparations.mu.Unlock()
	}
}
