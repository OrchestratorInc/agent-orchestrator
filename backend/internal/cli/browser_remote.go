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
	envAgentBrowserURL            = "AO_AGENT_BROWSER_URL"
	envAgentBrowserToken          = "AO_AGENT_BROWSER_TOKEN"
	remoteBrowserTimeout          = 2 * time.Minute
	remoteBrowserMaxResponseBytes = 256 << 20
)

type remoteBrowserConfig struct {
	baseURL string
	token   string
}

func remoteBrowserForward(getenv func(string) string, args []string) (*remoteBrowserConfig, []string, error) {
	if len(args) == 0 || args[0] != "browser" {
		return nil, nil, nil
	}
	baseURL := strings.TrimSpace(getenv(envAgentBrowserURL))
	if baseURL == "" {
		return nil, nil, nil
	}
	token := strings.TrimSpace(getenv(envAgentBrowserToken))
	if token == "" {
		return nil, nil, fmt.Errorf("%s is set but %s is empty: the remote browser refuses every request without its token", envAgentBrowserURL, envAgentBrowserToken)
	}
	return &remoteBrowserConfig{baseURL: strings.TrimRight(baseURL, "/"), token: token}, args[1:], nil
}

type remoteBrowserRequest struct {
	Args  []string          `json:"args"`
	Files map[string]string `json:"files,omitempty"`
}

type remoteBrowserResponse struct {
	Output string            `json:"output"`
	Error  string            `json:"error,omitempty"`
	Files  map[string]string `json:"files,omitempty"`
}

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
