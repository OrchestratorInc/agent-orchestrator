// Package proxyhost talks to the one detached AO-owned CLIProxyAPI helper.
package proxyhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var errResponse = errors.New("invalid helper response")

var errProtocol = errors.New("account helper protocol is incompatible; update AO after stopping its sessions")

type statusError struct {
	status int
}

func (e statusError) Error() string { return fmt.Sprintf("proxy operation failed (HTTP %d)", e.status) }

type manifest struct {
	Port         int    `json:"port"`
	PID          int    `json:"pid"`
	ControlKey   string `json:"control_key"`
	InferenceKey string `json:"inference_key"`
	TicketKey    string `json:"ticket_key"`
}

// Client communicates with the detached helper using its private control identity.
type Client struct {
	mu           sync.Mutex
	root, binary string
	state        manifest
	http         *http.Client
}

// New opens or creates the private helper identity.
func New(root, binary string) (*Client, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("proxy data directory must be absolute")
	}
	path := filepath.Join(root, "run", "host.json")
	c := &Client{root: root, binary: binary, http: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &c.state); err != nil {
			return nil, fmt.Errorf("read proxy identity: %w", err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// Losing an established identity is not a first launch: replacing its
		// keys/port would strand existing sessions and could start a second host.
		for _, artifact := range []string{"run/routes.json", "config.yaml", "auth"} {
			if _, statErr := os.Stat(filepath.Join(root, artifact)); !errors.Is(statErr, os.ErrNotExist) {
				return nil, errors.New("proxy identity is missing while helper state exists; restore the original identity before continuing")
			}
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			_ = listener.Close()
			return nil, errors.New("invalid loopback listener address")
		}
		c.state.Port = address.Port
		_ = listener.Close()
		for _, destination := range []*string{&c.state.ControlKey, &c.state.InferenceKey, &c.state.TicketKey} {
			var key [32]byte
			if _, err = rand.Read(key[:]); err != nil {
				return nil, err
			}
			*destination = hex.EncodeToString(key[:])
		}
		if err := c.save(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if c.state.Port < 1 || c.state.Port > 65535 || len(c.state.ControlKey) != 64 || len(c.state.InferenceKey) != 64 || len(c.state.TicketKey) != 64 {
		return nil, errors.New("invalid proxy identity")
	}
	for _, value := range []string{c.state.ControlKey, c.state.InferenceKey, c.state.TicketKey} {
		if _, err := hex.DecodeString(value); err != nil {
			return nil, errors.New("invalid proxy identity key")
		}
	}
	if c.state.ControlKey == c.state.InferenceKey || c.state.ControlKey == c.state.TicketKey || c.state.InferenceKey == c.state.TicketKey {
		return nil, errors.New("proxy identity keys must be distinct")
	}
	return c, nil
}

// Endpoint returns the stable loopback inference address.
func (c *Client) Endpoint() string { return "http://127.0.0.1:" + strconv.Itoa(c.state.Port) }

// TicketKey returns the private key used to derive session tickets.
func (c *Client) TicketKey() ([]byte, error) { return hex.DecodeString(c.state.TicketKey) }
func (c *Client) save() error {
	path := filepath.Join(c.root, "run", "host.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c.state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".host-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (c *Client) call(ctx context.Context, method, path string, body, output any, headers map[string]string) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint()+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.state.ControlKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxy helper unavailable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var detail struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&detail)
		if response.StatusCode == http.StatusConflict && detail.Code == "SESSION_BUSY" {
			return ports.ErrProviderAccountBusy
		}
		return statusError{status: response.StatusCode}
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
			return errors.Join(errResponse, err)
		}
	}
	return nil
}
func (c *Client) probe(ctx context.Context) error {
	var status struct {
		ProtocolVersion int `json:"protocol_version"`
	}
	if err := c.call(ctx, http.MethodGet, "/ao/status", nil, &status, nil); err != nil {
		return err
	}
	if status.ProtocolVersion != 2 {
		return errProtocol
	}
	return nil
}

// Ensure reuses the owned helper or starts it after confirming its previous process is gone.
func (c *Client) Ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	probeCtx, done := context.WithTimeout(ctx, time.Second)
	err := c.probe(probeCtx)
	done()
	if err == nil {
		return nil
	}
	var responseError statusError
	if errors.Is(err, errProtocol) || errors.Is(err, errResponse) || errors.As(err, &responseError) {
		return err
	}
	if c.state.PID > 0 {
		dead, err := processDead(c.state.PID)
		if err != nil || !dead {
			return errors.New("proxy helper ownership is unverified; active sessions were left untouched")
		}
	}
	if c.binary == "" {
		return errors.New("proxy helper is not packaged with this AO build")
	}
	if err := os.MkdirAll(filepath.Join(c.root, "logs"), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(c.root, "logs", "host.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(c.binary, "--data-dir", c.root, "--port", strconv.Itoa(c.state.Port))
	cmd.Dir = c.root
	cmd.Env = append(os.Environ(), "AO_PROXY_CONTROL_KEY="+c.state.ControlKey, "AO_PROXY_INFERENCE_KEY="+c.state.InferenceKey, "WRITABLE_PATH="+c.root, "MANAGEMENT_PASSWORD=")
	cmd.Stdout = log
	cmd.Stderr = log
	detach(cmd)
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start proxy helper: %w", err)
	}
	c.state.PID = cmd.Process.Pid
	saveErr := c.save()
	go func() { _ = cmd.Wait() }()
	if saveErr != nil {
		return saveErr
	}
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("proxy helper did not become ready")
		case <-ticker.C:
			probeCtx, done := context.WithTimeout(ctx, time.Second)
			err = c.probe(probeCtx)
			done()
			if err == nil {
				return nil
			}
		}
	}
}

// ApplyRoutes requires acknowledgement of the complete routing snapshot.
func (c *Client) ApplyRoutes(ctx context.Context, snapshot ports.ProviderRouteSnapshot) error {
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	var acknowledged ports.ProviderRouteSnapshot
	if err := c.call(ctx, http.MethodPut, "/ao/routes", snapshot, &acknowledged, nil); err != nil {
		return err
	}
	if acknowledged.Revision != snapshot.Revision || !slices.Equal(acknowledged.Routes, snapshot.Routes) || !slices.Equal(acknowledged.AuthIDs, snapshot.AuthIDs) {
		return errors.New("proxy routing acknowledgement does not match the requested snapshot")
	}
	return nil
}

// FetchAccountModels reads the model catalogue CLIProxyAPI registered for one
// credential. It is deliberately a control-plane call: no session ticket or
// provider token is exposed to AO.
func (c *Client) FetchAccountModels(ctx context.Context, provider, authID string) (ports.AgentModelCatalog, error) {
	if strings.TrimSpace(authID) == "" || (provider != "codex" && provider != "claude") {
		return ports.AgentModelCatalog{}, errors.New("account is not signed in")
	}
	if err := c.Ensure(ctx); err != nil {
		return ports.AgentModelCatalog{}, err
	}
	var response struct {
		Provider string `json:"provider"`
		Models   []struct {
			ID          string   `json:"id"`
			Label       string   `json:"label"`
			Provider    string   `json:"provider"`
			Description string   `json:"description"`
			Efforts     []string `json:"efforts"`
		} `json:"models"`
	}
	if err := c.call(ctx, http.MethodPost, "/ao/account-models", struct {
		AuthID   string `json:"auth_id"`
		Provider string `json:"provider"`
	}{AuthID: authID, Provider: provider}, &response, nil); err != nil {
		return ports.AgentModelCatalog{}, err
	}
	if response.Provider != provider || len(response.Models) == 0 {
		return ports.AgentModelCatalog{}, errors.New("provider model catalogue is unavailable")
	}
	models := make([]ports.AgentModelInfo, 0, len(response.Models))
	for _, model := range response.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" || !chatModel(id) {
			continue
		}
		label := strings.TrimSpace(model.Label)
		if label == "" {
			label = id
		}
		// Reasoning levels come with the model, so the effort choice is offered for
		// a managed account just as it is for the harness's own catalogue.
		var efforts []string
		for _, effort := range model.Efforts {
			if effort = strings.TrimSpace(effort); effort != "" {
				efforts = append(efforts, effort)
			}
		}
		// No model is marked as the default. The catalogue's order says nothing
		// about what an agent runs when it is not told a model, and a model
		// marked default is one AO leaves for the agent to choose.
		models = append(models, ports.AgentModelInfo{ID: id, Label: label, Provider: strings.TrimSpace(model.Provider), Efforts: efforts})
	}
	if len(models) == 0 {
		return ports.AgentModelCatalog{}, errors.New("provider model catalogue is unavailable")
	}
	return ports.AgentModelCatalog{AgentID: map[string]string{"codex": "codex", "claude": "claude-code"}[provider], SelectionMode: ports.ModelSelectionCatalog, Models: models, CustomModelEntry: ports.CustomModelEntryDirect, AllowCustom: true, Source: ports.ModelCatalogSourceManagedAccount, FetchedAt: time.Now().UTC()}, nil
}

// chatModel reports whether a catalogue entry is a model a person picks to talk
// to. CLIProxyAPI also registers the models Codex calls on its own, for image
// generation and for reviewing approvals; they carry nothing that tells them
// apart except their names.
func chatModel(id string) bool {
	return !strings.HasPrefix(id, "gpt-image-") && id != "codex-auto-review"
}

// AccountSignInFailures reads CLIProxy's credential listing. AO relies on
// CLIProxy's verdict (a refused token refresh or a rejected request) instead of
// judging a sign-in itself.
func (c *Client) AccountSignInFailures(ctx context.Context) (map[string]string, error) {
	var listing struct {
		Files []struct {
			ID            string `json:"id"`
			Status        string `json:"status"`
			StatusMessage string `json:"status_message"`
			Disabled      bool   `json:"disabled"`
		} `json:"files"`
	}
	if err := c.management(ctx, http.MethodGet, "/v8/management/credentials", nil, &listing, ""); err != nil {
		return nil, err
	}
	if listing.Files == nil {
		return nil, errResponse
	}
	failures := make(map[string]string)
	for _, file := range listing.Files {
		reason := strings.ToLower(file.StatusMessage)
		if file.Disabled || file.Status == "disabled" || strings.Contains(reason, "unauthorized") || strings.Contains(reason, "invalid grant") || strings.Contains(reason, "invalid_grant") {
			failures[file.ID] = file.StatusMessage
		}
	}
	return failures, nil
}

// ListCredentials reads the sign-ins CLIProxy holds as files. API keys live in
// its configuration instead and are not listed.
func (c *Client) ListCredentials(ctx context.Context) ([]ports.ProviderCredential, error) {
	var listing struct {
		Files []struct {
			Name     string    `json:"name"`
			Provider string    `json:"provider"`
			Source   string    `json:"source"`
			Modified time.Time `json:"modtime"`
		} `json:"files"`
	}
	if err := c.management(ctx, http.MethodGet, "/v8/management/credentials", nil, &listing, ""); err != nil {
		return nil, err
	}
	if listing.Files == nil {
		return nil, errResponse
	}
	credentials := make([]ports.ProviderCredential, 0, len(listing.Files))
	for _, file := range listing.Files {
		if file.Source == "file" && strings.TrimSpace(file.Name) != "" {
			credentials = append(credentials, ports.ProviderCredential{Name: file.Name, Provider: file.Provider, ModifiedAt: file.Modified})
		}
	}
	return credentials, nil
}

// DeleteCredential removes an upstream credential and verifies an already missing file.
func (c *Client) DeleteCredential(ctx context.Context, name string) error {
	if strings.HasPrefix(name, "config-index:") {
		// CLIProxy exposes a stable auth-index in its list response, but its
		// delete endpoint takes the current array position. Resolve that
		// position immediately before deleting so other key changes cannot
		// remove the wrong credential.
		parts := strings.SplitN(name, ":", 3)
		if len(parts) != 3 || (parts[1] != "codex" && parts[1] != "claude") || parts[2] == "" || strings.ContainsAny(parts[2], "/\\") {
			return errors.New("invalid API-key reference")
		}
		field := parts[1] + "-api-key"
		entries, err := c.apiKeys(ctx, field)
		if err != nil {
			return err
		}
		for i, entry := range entries {
			if entry["auth-index"] == parts[2] {
				return c.management(ctx, http.MethodDelete, "/v0/management/"+field+"?index="+strconv.Itoa(i), nil, nil, "")
			}
		}
		return nil
	}
	if strings.HasPrefix(name, "config:") && len(name) > len("config:") && !strings.ContainsAny(name, "/\\") {
		if err := c.Ensure(ctx); err != nil {
			return err
		}
		return c.call(ctx, http.MethodDelete, "/ao/api-key?ref="+url.QueryEscape(name), nil, nil, nil)
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid credential reference")
	}
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	err := c.call(ctx, http.MethodDelete, "/v8/management/credentials?name="+url.QueryEscape(name), nil, nil, nil)
	var code statusError
	if errors.As(err, &code) && code.status == http.StatusNotFound {
		var listing struct {
			Files []struct {
				Name string `json:"name"`
			} `json:"files"`
		}
		if listErr := c.call(ctx, http.MethodGet, "/v8/management/credentials?name="+url.QueryEscape(name), nil, &listing, nil); listErr != nil {
			return listErr
		}
		if listing.Files == nil {
			return errors.New("credential deletion could not be verified")
		}
		for _, file := range listing.Files {
			if file.Name == name {
				return err
			}
		}
		return nil
	}
	return err
}

func (c *Client) apiKeys(ctx context.Context, field string) ([]map[string]any, error) {
	var listing map[string][]map[string]any
	err := c.management(ctx, http.MethodGet, "/v0/management/"+field, nil, &listing, "")
	entries, ok := listing[field]
	if err == nil && !ok {
		err = errResponse
	}
	return entries, err
}

// management is private to AO's login coordinator; raw responses never reach UI.
func (c *Client) management(ctx context.Context, method, path string, body, output any, loginID string) error {
	if err := c.Ensure(ctx); err != nil {
		return err
	}
	headers := map[string]string{}
	if loginID != "" {
		headers["X-AO-Login-ID"] = loginID
	}
	return c.call(ctx, method, path, body, output, headers)
}
