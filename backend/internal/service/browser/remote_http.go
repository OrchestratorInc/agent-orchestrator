package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/approval"
)

// remoteMaxResponseBytes bounds one service response; it matches the CLI's
// former direct client (several base64 files of up to 64 MiB each).
const remoteMaxResponseBytes = 256 << 20

// HTTPRemoteBrowser forwards commands to the host's agent-browser service
// (POST /agent), adding its token. Agents never hold the token: their
// commands reach the service only through the daemon's approval gate.
type HTTPRemoteBrowser struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewHTTPRemoteBrowser returns a forwarder for the service at baseURL.
func NewHTTPRemoteBrowser(baseURL, token string) *HTTPRemoteBrowser {
	return &HTTPRemoteBrowser{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		// The command itself runs inside the daemon's browser command budget.
		client: &http.Client{Timeout: approval.BrowserCommandBudget},
	}
}

// Forward implements RemoteBrowser.
func (h *HTTPRemoteBrowser) Forward(ctx context.Context, args []string, files map[string]string) (RemoteResponse, error) {
	body, err := json.Marshal(struct {
		Args  []string          `json:"args"`
		Files map[string]string `json:"files,omitempty"`
	}{Args: args, Files: files})
	if err != nil {
		return RemoteResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/agent", bytes.NewReader(body))
	if err != nil {
		return RemoteResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.client.Do(req) // #nosec G704 -- the host's configured loopback agent browser.
	if err != nil {
		return RemoteResponse{}, fmt.Errorf("agent browser service is unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return RemoteResponse{}, errors.New("agent browser service rejected the daemon's token")
	}
	if resp.StatusCode != http.StatusOK {
		return RemoteResponse{}, fmt.Errorf("agent browser service answered %s", resp.Status)
	}
	var out RemoteResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, remoteMaxResponseBytes)).Decode(&out); err != nil {
		return RemoteResponse{}, fmt.Errorf("decode agent browser response: %w", err)
	}
	return out, nil
}
