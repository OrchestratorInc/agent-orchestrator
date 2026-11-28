package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	envAgentBrowserURL   = "AO_AGENT_BROWSER_URL"
	envAgentBrowserToken = "AO_AGENT_BROWSER_TOKEN"
	remoteBrowserTimeout = 2 * time.Minute
	// remoteBrowserMaxResponseBytes bounds one command response. The service
	// itself allows 64 MiB per file; base64 inflates that by 4/3 (~85.4 MiB),
	// and one response can carry several files plus output text, so the cap
	// must exceed the encoded size of a maximum single file.
	remoteBrowserMaxResponseBytes = 256 << 20
)

// remoteBrowserConfig carries the self-hosted agent-browser service endpoint.
// viaDaemon means the command goes to this host's daemon, which runs the
// approval gate and holds the service token; AO sessions get only the URL.
type remoteBrowserConfig struct {
	baseURL   string
	token     string
	viaDaemon bool
}

// remoteBrowserForward reports whether args select `ao browser` while a
// self-hosted agent browser is configured in this process's environment. The
// returned rest holds the arguments after "browser". A non-nil error means the
// environment names a remote browser incompletely and the command must fail
// rather than silently fall back to the desktop bridge.
func remoteBrowserForward(getenv func(string) string, args []string) (*remoteBrowserConfig, []string, error) {
	if len(args) == 0 || args[0] != "browser" {
		return nil, nil, nil
	}
	// The remote service has no batch command: AO runs the batch itself and
	// forwards each step on its own.
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "-") {
			if arg == "batch" {
				return nil, nil, nil
			}
			break
		}
	}
	baseURL := strings.TrimSpace(getenv(envAgentBrowserURL))
	if baseURL == "" {
		return nil, nil, nil
	}
	if strings.TrimSpace(getenv("AO_BROWSER_CAPABILITY")) != "" {
		// An AO session: its daemon gates and forwards the command. This wins
		// over a service token, which a session started before the host
		// upgraded may still carry: that token must not bypass the gate.
		return &remoteBrowserConfig{baseURL: strings.TrimRight(baseURL, "/"), viaDaemon: true}, args[1:], nil
	}
	token := strings.TrimSpace(getenv(envAgentBrowserToken))
	if token == "" {
		return nil, nil, fmt.Errorf("%s is set but %s is empty: the remote browser refuses every request without its token", envAgentBrowserURL, envAgentBrowserToken)
	}
	return &remoteBrowserConfig{baseURL: strings.TrimRight(baseURL, "/"), token: token}, args[1:], nil
}

// remoteBrowserRequest mirrors the agent-browser service command contract:
// raw agent-browser args plus file contents that must travel with the command.
type remoteBrowserRequest struct {
	Args  []string          `json:"args"`
	Files map[string]string `json:"files,omitempty"`
}

// remoteBrowserResponse is the service's reply: command output, an optional
// error string, and files it wrote (screenshots and other saved artifacts).
type remoteBrowserResponse struct {
	Output string            `json:"output"`
	Error  string            `json:"error,omitempty"`
	Files  map[string]string `json:"files,omitempty"`
}

// runRemoteBrowserCommand forwards one `ao browser <args...>` invocation to
// the configured agent-browser service and relays its output. It exists so a
// self-hosted remote host gets the remote command interface everywhere,
// including inside AO agent sessions whose PATH resolves to this binary first.
func runRemoteBrowserCommand(ctx context.Context, deps Deps, cfg remoteBrowserConfig, args []string) error {
	files, err := remoteBrowserUploadArgs(args)
	if err != nil {
		return err
	}
	if cfg.viaDaemon {
		return runGatedRemoteBrowserCommand(ctx, deps, args, files)
	}
	body, err := json.Marshal(remoteBrowserRequest{Args: args, Files: files})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteBrowserTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.baseURL+"/agent", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.token)
	resp, err := remoteBrowserHTTPClient(deps.HTTPClient).Do(req)
	if err != nil {
		return fmt.Errorf("remote browser service at %s is unreachable: %w", cfg.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("remote browser service rejected the credential (unauthorized): check " + envAgentBrowserToken)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote browser service at %s answered %s", cfg.baseURL, resp.Status)
	}
	var out remoteBrowserResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, remoteBrowserMaxResponseBytes)).Decode(&out); err != nil {
		return fmt.Errorf("decode remote browser response: %w", err)
	}
	return relayRemoteBrowserResponse(deps, out)
}

// relayRemoteBrowserResponse writes returned files and prints the output.
func relayRemoteBrowserResponse(deps Deps, out remoteBrowserResponse) error {
	if err := writeRemoteBrowserFiles(out.Files); err != nil {
		return err
	}
	if out.Output != "" {
		if _, err := io.WriteString(deps.Out, out.Output); err != nil {
			return err
		}
		if !strings.HasSuffix(out.Output, "\n") {
			if _, err := io.WriteString(deps.Out, "\n"); err != nil {
				return err
			}
		}
	}
	if out.Error != "" {
		if _, err := fmt.Fprintln(deps.Err, out.Error); err != nil {
			return err
		}
		return errors.New(out.Error)
	}
	return nil
}

// runGatedRemoteBrowserCommand sends the command to this host's daemon, which
// asks the user before a consequential action and then forwards it to the
// agent browser. There is no fallback to the service: if the daemon cannot be
// reached, the command does not run.
func runGatedRemoteBrowserCommand(ctx context.Context, deps Deps, args []string, files map[string]string) error {
	sessionID, capability, err := currentBrowserIdentity()
	if err != nil {
		return err
	}
	c := &commandContext{deps: deps}
	var out remoteBrowserResponse
	// Only the daemon can read a raw agent-browser command, so any one may be
	// waiting for the user; a quick one finishes before the hint shows.
	stderr := deps.Err
	if stderr == nil {
		stderr = io.Discard
	}
	err = withApprovalHint(stderr, func() error {
		return c.doJSONPathWithHeadersAndTimeout(
			ctx,
			http.MethodPost,
			"/api/v1/browser/remote-commands",
			map[string]any{"sessionId": sessionID, "args": args, "files": files},
			&out,
			map[string]string{browserCapabilityHeader: capability},
			browserCommandTimeout,
		)
	})
	if err != nil {
		return err
	}
	return relayRemoteBrowserResponse(deps, out)
}

// remoteBrowserUploadArgs inlines local files named by the command, mirroring
// the agent-browser service client contract: an absolute path that exists is
// file content, and relative paths travel only for the upload command (a word
// typed into a page, like README.md, is not a file to copy).
func remoteBrowserUploadArgs(args []string) (map[string]string, error) {
	upload := false
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			upload = arg == "upload"
			break
		}
	}
	var files map[string]string
	for _, arg := range args {
		if !upload && !filepath.IsAbs(arg) {
			continue
		}
		info, err := os.Stat(arg)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(arg)
		if err != nil {
			return nil, fmt.Errorf("read %s for upload to the remote browser: %w", arg, err)
		}
		if files == nil {
			files = make(map[string]string)
		}
		files[arg] = base64.StdEncoding.EncodeToString(data)
	}
	return files, nil
}

// writeRemoteBrowserFiles saves the files the service wrote (screenshots,
// downloads) at their reported paths.
func writeRemoteBrowserFiles(files map[string]string) error {
	for path, encoded := range files {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("decode remote browser file %s: %w", path, err)
		}
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return fmt.Errorf("create directory for remote browser file %s: %w", path, err)
			}
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("write remote browser file %s: %w", path, err)
		}
	}
	return nil
}

// remoteBrowserHTTPClient returns the client used for one remote browser
// command. The CLI's default HTTP client carries a 2-second probe timeout,
// which cannot cover healthy navigation or waits; a context deadline cannot
// extend a client timeout, so a shorter client timeout is raised to
// remoteBrowserTimeout. A client with no timeout is left alone, and a client
// already allowing longer than remoteBrowserTimeout keeps its own budget.
func remoteBrowserHTTPClient(in *http.Client) *http.Client {
	if in == nil {
		return &http.Client{Timeout: remoteBrowserTimeout}
	}
	if in.Timeout == 0 || in.Timeout >= remoteBrowserTimeout {
		return in
	}
	raised := *in
	raised.Timeout = remoteBrowserTimeout
	return &raised
}
