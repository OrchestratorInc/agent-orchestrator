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

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
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
type remoteBrowserConfig struct {
	baseURL string
	token   string
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
	baseURL := strings.TrimSpace(getenv(envAgentBrowserURL))
	if baseURL == "" {
		return nil, nil, nil
	}
	// Same rule as the daemon's config: the bearer token must never leave
	// this machine, so only a loopback http URL is accepted.
	validated, err := config.ParseAgentBrowserURL(baseURL)
	if err != nil {
		return nil, nil, err
	}
	token := strings.TrimSpace(getenv(envAgentBrowserToken))
	if token == "" {
		return nil, nil, fmt.Errorf("%s is set but %s is empty: the remote browser refuses every request without its token", envAgentBrowserURL, envAgentBrowserToken)
	}
	return &remoteBrowserConfig{baseURL: validated, token: token}, args[1:], nil
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
	if err := writeRemoteBrowserFiles(deps.Err, args, out.Files); err != nil {
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

// remoteBrowserFileInputCommands are the commands whose path arguments are
// local files the service must receive. Every other command sends its
// arguments only as text: a path typed into a page (`fill @e1 /etc/hosts`)
// must never upload that file.
var remoteBrowserFileInputCommands = map[string]bool{"upload": true}

// remoteBrowserCommand returns the first non-flag argument: the command name.
func remoteBrowserCommand(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// remoteBrowserUploadArgs inlines the local files named by a file-input
// command (see remoteBrowserFileInputCommands), mirroring the agent-browser
// service client contract. Other commands upload nothing.
func remoteBrowserUploadArgs(args []string) (map[string]string, error) {
	command := remoteBrowserCommand(args)
	if !remoteBrowserFileInputCommands[command] {
		return nil, nil
	}
	var files map[string]string
	seenCommand := false
	for _, arg := range args {
		if !seenCommand {
			seenCommand = arg == command
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		info, err := os.Stat(arg)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(arg) // #nosec G304 -- the user named this file for upload.
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

// remoteBrowserFilesDir is where files the service returns are saved when the
// command did not name their path: <AO state dir>/browser-files.
func remoteBrowserFilesDir() (string, error) {
	stateDir, _, _, err := config.ResolvePaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "browser-files"), nil
}

// remoteBrowserSavePath decides where a file the service returned is written.
// The service chooses the reported path, so it is trusted only when the
// command named that exact path as an argument (`screenshot /tmp/page.png`).
// Any other file is saved under remoteBrowserFilesDir by its base name, so a
// response can never write outside the paths the user asked for.
func remoteBrowserSavePath(args []string, reported string) (string, bool, error) {
	if reported != "" {
		clean := filepath.Clean(reported)
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") || arg == "" {
				continue
			}
			if filepath.Clean(arg) == clean {
				return arg, false, nil
			}
		}
	}
	base := filepath.Base(filepath.Clean(reported))
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		base = "file"
	}
	dir, err := remoteBrowserFilesDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(dir, base), true, nil
}

// writeRemoteBrowserFiles saves the files the service wrote (screenshots,
// downloads). A file lands at its reported path only when the command named
// that path; otherwise it is confined to remoteBrowserFilesDir and stderr says
// where it went.
func writeRemoteBrowserFiles(errOut io.Writer, args []string, files map[string]string) error {
	for reported, encoded := range files {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("decode remote browser file %s: %w", reported, err)
		}
		path, moved, err := remoteBrowserSavePath(args, reported)
		if err != nil {
			return fmt.Errorf("choose where to save remote browser file %s: %w", reported, err)
		}
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return fmt.Errorf("create directory for remote browser file %s: %w", path, err)
			}
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("write remote browser file %s: %w", path, err)
		}
		if moved {
			if _, err := fmt.Fprintf(errOut, "remote browser file %s saved to %s\n", reported, path); err != nil {
				return err
			}
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
