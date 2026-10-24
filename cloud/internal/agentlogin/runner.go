// Package agentlogin runs native provider login in an isolated, short-lived home.
package agentlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const maxCredentialBytes = 64 << 10
const maxOutputBytes = 256 << 10

type Config struct {
	BaseURL   string
	AttemptID string
	Token     string
	DataDir   string
}

type progress struct {
	Status           string `json:"status"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	UserCode         string `json:"userCode,omitempty"`
	RequiresCode     bool   `json:"requiresCode,omitempty"`
	CredentialType   string `json:"credentialType,omitempty"`
	Secret           string `json:"secret,omitempty"`
}

type runner struct {
	config       Config
	pollInterval time.Duration
	command      func(context.Context, string, ...string) *exec.Cmd
	http         *http.Client
	endpoint     string
}

func newRunner(config Config, allowLoopbackHTTP bool) (*runner, error) {
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid agent login control-plane URL")
	}
	loopback := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(allowLoopbackHTTP && u.Scheme == "http" && loopback != nil && loopback.IsLoopback()) {
		return nil, errors.New("agent login requires HTTPS")
	}
	if _, err := uuid.Parse(config.AttemptID); err != nil {
		return nil, errors.New("invalid agent login attempt")
	}
	if config.Token == "" || len(config.Token) > 4096 || strings.ContainsAny(config.Token, " \r\n\t") {
		return nil, errors.New("invalid agent login runner token")
	}
	if !filepath.IsAbs(config.DataDir) {
		return nil, errors.New("agent login requires an absolute private data directory")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &runner{config: config, pollInterval: time.Second, command: exec.CommandContext,
		http:     &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: strings.TrimRight(config.BaseURL, "/") + "/api/cloud/v1/agent-logins/" + config.AttemptID + "/runner"}, nil
}

// Run performs only native login. It never boots a normal worker or emits raw
// terminal output. The attempt bearer is used only by the control-plane client.
func Run(ctx context.Context, config Config) error {
	r, err := newRunner(config, false)
	if err != nil {
		return err
	}
	return r.run(ctx)
}

type attempt struct {
	Harness   string    `json:"harness"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expiresAt"`
	Code      string    `json:"code,omitempty"`
}

func (a attempt) active() bool {
	return (a.Status == "starting" || a.Status == "waiting" || a.Status == "connecting") && time.Now().Before(a.ExpiresAt)
}

func (r *runner) request(ctx context.Context, event *progress) (attempt, error) {
	method := http.MethodGet
	var body io.Reader
	if event != nil {
		method = http.MethodPost
		b, _ := json.Marshal(event)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.endpoint, body)
	if err != nil {
		return attempt{}, errors.New("could not create agent login request")
	}
	req.Header.Set("Authorization", "Bearer "+r.config.Token)
	if event != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := r.http.Do(req)
	if err != nil {
		return attempt{}, errors.New("agent login control plane is unavailable")
	}
	defer response.Body.Close()
	if event != nil && response.StatusCode == http.StatusNoContent {
		return attempt{}, nil
	}
	if event != nil || response.StatusCode != http.StatusOK {
		return attempt{}, errors.New("agent login attempt is no longer available")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCredentialBytes+1))
	if err != nil || len(data) > maxCredentialBytes {
		return attempt{}, errors.New("invalid agent login response")
	}
	var state attempt
	if json.Unmarshal(data, &state) != nil || (state.Harness != "codex" && state.Harness != "claude-code") || state.ExpiresAt.IsZero() {
		return attempt{}, errors.New("invalid agent login response")
	}
	return state, nil
}

func (r *runner) run(parent context.Context) (result error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	state, err := r.request(ctx, nil)
	if err != nil {
		return err
	}
	if !state.active() {
		return errors.New("agent login attempt has ended")
	}
	ctx, deadlineCancel := context.WithDeadline(ctx, state.ExpiresAt)
	defer deadlineCancel()
	// A fixed failure payload keeps CLI output, codes and credentials out of
	// public errors. Never report failure for an already cancelled/expired run.
	defer func() {
		if result != nil && ctx.Err() == nil {
			current, getErr := r.request(ctx, nil)
			if getErr == nil && current.active() {
				_, _ = r.request(ctx, &progress{Status: "failed"})
			}
		}
	}()
	if err := os.MkdirAll(r.config.DataDir, 0o700); err != nil {
		return errors.New("could not create private login directory")
	}
	if info, err := os.Lstat(r.config.DataDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid private login directory")
	}
	home, err := os.MkdirTemp(r.config.DataDir, "agent-login-")
	if err != nil {
		return errors.New("could not create private login home")
	}
	defer func() {
		if err := os.RemoveAll(home); err != nil && result == nil {
			result = errors.New("could not remove private login home")
		}
	}()
	for _, sub := range []string{".codex", ".claude", "tmp", ".config", ".cache", ".local/share"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o700); err != nil {
			return errors.New("could not prepare private login home")
		}
	}
	name, args := "codex", []string{"-c", `cli_auth_credentials_store="file"`, "login", "--device-auth"}
	if state.Harness == "claude-code" {
		name, args = "claude", []string{"setup-token"}
	}
	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()
	cmd := r.command(processCtx, name, args...)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex"), "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "TERM=xterm-256color", "LANG=C.UTF-8", "NO_COLOR=1", "FORCE_COLOR=0", "BROWSER=/bin/false"}
	cmd.Cancel = func() error { return stopProcessGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 4096})
	if err != nil {
		return errors.New("native login CLI could not start")
	}
	defer terminal.Close()
	defer stopProcessGroup(cmd)
	chunks := make(chan []byte, 8)
	readDone := make(chan struct{})
	go func() {
		defer close(chunks)
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, readErr := terminal.Read(buf)
			if n > 0 {
				select {
				case chunks <- bytes.Clone(buf[:n]):
				case <-processCtx.Done():
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	processDone := make(chan error, 1)
	processReaped := make(chan struct{})
	go func() {
		err := cmd.Wait()
		_ = stopProcessGroup(cmd)
		processDone <- err
		close(processReaped)
	}()
	defer func() { processCancel(); _ = stopProcessGroup(cmd); _ = terminal.Close(); <-readDone; <-processReaped }()
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	parser := outputParser{harness: state.Harness}
	var announced bool
	var exited bool
	var exitErr error
	var secret string
	kind := "oauth_token"
	if state.Harness == "codex" {
		kind = "auth_json"
	}
	for !exited || chunks != nil {
		select {
		case <-ctx.Done():
			return errors.New("agent login was cancelled or expired")
		case exitErr = <-processDone:
			exited = true
			processDone = nil
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
			} else if err := parser.add(chunk); err != nil {
				return err
			}
			if event, ok := parser.waiting(); ok && !announced {
				if _, err := r.request(ctx, &event); err != nil {
					return err
				}
				announced = true
			}
		case <-ticker.C:
			current, err := r.request(ctx, nil)
			if err != nil {
				return err
			}
			if !current.active() || current.Harness != state.Harness {
				return errors.New("agent login attempt has ended")
			}
			if current.Code != "" {
				if state.Harness != "claude-code" || !announced || !validInputCode(current.Code) {
					return errors.New("invalid provider login code")
				}
				if _, err := r.request(ctx, &progress{Status: "connecting"}); err != nil {
					return err
				}
				if _, err := io.WriteString(terminal, current.Code+"\r"); err != nil {
					return errors.New("native login code could not be delivered")
				}
			}
		}
		if exitErr != nil {
			return errors.New("native login did not complete")
		}
		if !announced {
			continue
		}
		// Both native CLIs can remain alive after producing their credential.
		// Read only the bounded private document or known complete token marker;
		// absent/partially written documents are retried while the CLI is alive.
		candidate := parser.token()
		if state.Harness == "codex" {
			candidate, _ = readCodexCredential(home)
		}
		if candidate != "" {
			// Prefer a native failure already observed by Wait over readiness.
			// Once a valid credential is ready, cleanup stops/reaps an idle CLI.
			select {
			case exitErr = <-processDone:
				exited = true
				processDone = nil
			default:
			}
			if exitErr != nil {
				return errors.New("native login did not complete")
			}
			secret = candidate
			break
		}
	}
	if exitErr != nil || !announced || ctx.Err() != nil {
		return errors.New("native login did not complete")
	}
	if secret == "" {
		return errors.New("native login did not produce a credential")
	}
	current, err := r.request(ctx, nil)
	if err != nil {
		return err
	}
	if !current.active() || current.Harness != state.Harness {
		return errors.New("agent login attempt has ended")
	}
	_, err = r.request(ctx, &progress{Status: "connected", CredentialType: kind, Secret: secret})
	return err
}

func validInputCode(code string) bool {
	if len(code) == 0 || len(code) > 4096 {
		return false
	}
	for _, char := range code {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func stopProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return cmd.Process.Kill()
}

func readCodexCredential(home string) (string, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return "", errors.New("could not open private login home")
	}
	defer root.Close()
	for _, path := range []string{".codex", ".codex/auth.json"} {
		info, err := root.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("invalid native credential file")
		}
	}
	file, err := openCodexAuthFile(root)
	if err != nil {
		return "", errors.New("could not open native credential file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCredentialBytes || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("invalid native credential file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || len(data) > maxCredentialBytes {
		return "", errors.New("native credential is too large")
	}
	var auth struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || strings.TrimSpace(auth.Tokens.AccessToken) == "" || strings.TrimSpace(auth.Tokens.RefreshToken) == "" {
		return "", errors.New("native credential has no login tokens")
	}
	return string(data), nil
}

func openCodexAuthFile(root *os.Root) (*os.File, error) {
	// Root.OpenFile resolves in-root symlinks even with O_NOFOLLOW. Start from
	// its trusted directory handle and open each fixed component ourselves so
	// a concurrently running CLI cannot swap either component for a symlink.
	home, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer home.Close()
	directory, err := unix.Openat(int(home.Fd()), ".codex", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(directory)
	fd, err := unix.Openat(directory, "auth.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "native-auth.json"), nil
}
