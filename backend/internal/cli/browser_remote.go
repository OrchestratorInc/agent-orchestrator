package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

// remoteBrowserBoolFlags are agent-browser global flags that take no value,
// so the token after one is the command rather than a flag value.
var remoteBrowserBoolFlags = map[string]bool{
	"--json": true, "--headed": true, "--debug": true, "--help": true, "-h": true, "--version": true, "-V": true,
}

// remoteBrowserCommandIndex returns the index of the command in args, or -1.
// agent-browser puts global flags before the command, and a flag written as
// "--flag value" consumes the next token, so a non-flag token right after a
// value-taking flag is always that flag's value, even when it reads "upload"
// (`--session upload fill …` is a fill). This fails closed: a value-less flag
// missing from remoteBrowserBoolFlags makes the next token look like a value,
// so a file is not sent, rather than sent when it should not be.
func remoteBrowserCommandIndex(args []string) int {
	expectValue := false
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			expectValue = !strings.Contains(arg, "=") && !remoteBrowserBoolFlags[arg]
			continue
		}
		if expectValue {
			expectValue = false
			continue
		}
		return i
	}
	return -1
}

// remoteBrowserUploadArgs inlines the local files named by a file-input
// command (see remoteBrowserFileInputCommands), mirroring the agent-browser
// service client contract. Other commands upload nothing.
func remoteBrowserUploadArgs(args []string) (map[string]string, error) {
	at := remoteBrowserCommandIndex(args)
	if at < 0 || !remoteBrowserFileInputCommands[args[at]] {
		return nil, nil
	}
	var files map[string]string
	for _, arg := range args[at+1:] {
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

// remoteBrowserNamedPath returns the argument that names the reported path,
// if the command named it as an operand (`screenshot /tmp/page.png`). Only
// tokens after the subcommand count, so neither the subcommand itself nor a
// global flag's value (`--session work`) can authorize a write.
func remoteBrowserNamedPath(args []string, reported string) (string, bool) {
	at := remoteBrowserCommandIndex(args)
	if reported == "" || at < 0 {
		return "", false
	}
	clean := filepath.Clean(reported)
	for _, arg := range args[at+1:] {
		if strings.HasPrefix(arg, "-") || arg == "" {
			continue
		}
		if filepath.Clean(arg) == clean {
			return arg, true
		}
	}
	return "", false
}

// remoteBrowserPathKey normalizes a destination so named and confined paths
// that point at the same file compare equal.
func remoteBrowserPathKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// remoteBrowserSavePath decides where a file the service returned is written.
// The service chooses the reported path, so it is trusted only when the
// command named that exact path as an argument. Any other file is saved under
// remoteBrowserFilesDir by its base name, so a response can never write
// outside the paths the user asked for. taken holds the destinations already
// claimed by this response, including every named path, so no two returned
// files share a destination.
func remoteBrowserSavePath(args []string, reported string, taken map[string]bool) (string, bool, error) {
	if named, ok := remoteBrowserNamedPath(args, reported); ok {
		taken[remoteBrowserPathKey(named)] = true
		return named, false, nil
	}
	base := filepath.Base(filepath.Clean(reported))
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		base = "file"
	}
	dir, err := remoteBrowserFilesDir()
	if err != nil {
		return "", false, err
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	path := filepath.Join(dir, base)
	for n := 2; taken[remoteBrowserPathKey(path)]; n++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, n, ext))
	}
	taken[remoteBrowserPathKey(path)] = true
	return path, true, nil
}

// writeRemoteBrowserFiles saves the files the service wrote (screenshots,
// downloads). A file lands at its reported path only when the command named
// that path; otherwise it is confined to remoteBrowserFilesDir and stderr says
// where it went.
func writeRemoteBrowserFiles(errOut io.Writer, args []string, files map[string]string) error {
	// Sorted so collision suffixes do not depend on map iteration order.
	reportedPaths := make([]string, 0, len(files))
	for reported := range files {
		reportedPaths = append(reportedPaths, reported)
	}
	sort.Strings(reportedPaths)
	// Named destinations are reserved first, so a relocated file never
	// overwrites a file the command asked for by path.
	taken := make(map[string]bool, len(files))
	for _, reported := range reportedPaths {
		if named, ok := remoteBrowserNamedPath(args, reported); ok {
			taken[remoteBrowserPathKey(named)] = true
		}
	}
	for _, reported := range reportedPaths {
		encoded := files[reported]
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("decode remote browser file %s: %w", reported, err)
		}
		path, moved, err := remoteBrowserSavePath(args, reported, taken)
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
	client := http.Client{Timeout: remoteBrowserTimeout}
	if in != nil {
		client = *in
	}
	if client.Timeout != 0 && client.Timeout < remoteBrowserTimeout {
		client.Timeout = remoteBrowserTimeout
	}
	// A 307/308 replays the request body, uploaded files included. Follow a
	// redirect only while it stays on a loopback host, so nothing the command
	// sends can leave this machine.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("remote browser service redirected too many times")
		}
		if !remoteBrowserLoopbackURL(req.URL) {
			return fmt.Errorf("remote browser service redirected off this machine to %s; refusing to follow", req.URL.Host)
		}
		return nil
	}
	return &client
}

// remoteBrowserLoopbackURL reports whether u is a plain http URL on a
// loopback host, the same rule config.ParseAgentBrowserURL applies.
func remoteBrowserLoopbackURL(u *url.URL) bool {
	host := u.Hostname()
	ip := net.ParseIP(host)
	return u.Scheme == "http" && u.User == nil && (strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback())
}
