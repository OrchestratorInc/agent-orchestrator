// Package freestyle implements AO's provider-neutral sandbox lifecycle against
// the Freestyle VM API (https://docs.freestyle.sh) — full Linux VMs booted
// from snapshots that capture memory as well as disk, so pause/start keeps the
// worker's processes. No Freestyle type escapes this package.
//
// This is a prototype that stands in for the CreateOS client behind the nodeops
// provider slot, so the reconciler's NodeOps layout (/workspace, the ao-worker
// user, baked /usr/local/bin binaries) applies unchanged. The snapshot named by
// the session's rootfs must provide that layout.
package freestyle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

const (
	// DefaultBaseURL is Freestyle's public API.
	DefaultBaseURL = "https://api.freestyle.sh"
	// defaultTimeout bounds a single API call. Creation waits are handled by the
	// reconciler's retry loop, not by a long HTTP request.
	defaultTimeout = 2 * time.Minute
	// maxResponseBody bounds decoding so a broken response cannot exhaust
	// control-plane memory.
	maxResponseBody = 1 << 20
	maxErrorBody    = 64 << 10
	slugPrefix      = "ao-"
	// bootstrapTimeout bounds the launch exec; the launch backgrounds the worker
	// so the command itself returns in well under a second.
	bootstrapTimeout = 60 * time.Second
	// recreateDeleteWait bounds how long Recreate waits for the old VM to be
	// gone before creating its replacement under the same slug.
	recreateDeleteWait = 30 * time.Second
	recreateDeletePoll = 250 * time.Millisecond
)

// HTTPError is a non-2xx response from the Freestyle API. The API key is only
// ever sent as a header, so it never appears in Body.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("freestyle api returned %d", e.StatusCode)
	}
	return fmt.Sprintf("freestyle api returned %d: %s", e.StatusCode, e.Body)
}

// Client talks to the Freestyle API with one API key.
type Client struct {
	baseURL         string
	apiKey          string
	defaultSnapshot string
	http            *http.Client
	log             *slog.Logger
}

var (
	_ sandbox.Provider     = (*Client)(nil)
	_ sandbox.Bootstrapper = (*Client)(nil)
	_ sandbox.Recreator    = (*Client)(nil)

	_ sandbox.DuplicateRejectingCreator = (*Client)(nil)
)

// CreateRejectsDuplicates reports that Create fails with ErrAlreadyExists when
// the session's slug is taken: Freestyle answers 409 for a slug in use.
func (c *Client) CreateRejectsDuplicates() {}

// Config configures a Freestyle client.
type Config struct {
	BaseURL string
	APIKey  string
	// DefaultSnapshot boots a session whose spec names no rootfs.
	DefaultSnapshot string
	HTTPClient      *http.Client
	Logger          *slog.Logger
}

// New creates a Freestyle sandbox provider.
func New(config Config) *Client {
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:         baseURL,
		apiKey:          strings.TrimSpace(config.APIKey),
		defaultSnapshot: strings.TrimSpace(config.DefaultSnapshot),
		http:            httpClient,
		log:             logger,
	}
}

// SlugFor is the account-unique slug AO gives a session's VM, which is also how
// FindBySession recovers it.
func SlugFor(sessionID string) string {
	return slugPrefix + strings.ToLower(strings.TrimSpace(sessionID))
}

type vmView struct {
	ID       string            `json:"id"`
	Slug     string            `json:"slug"`
	State    string            `json:"state"`
	Metadata map[string]string `json:"metadata"`
}

type firewallRule struct {
	Action      string         `json:"action"`
	Source      map[string]any `json:"source"`
	Destination map[string]any `json:"destination"`
}

type createVMRequest struct {
	SnapshotID         string                    `json:"snapshotId,omitempty"`
	Slug               string                    `json:"slug,omitempty"`
	Metadata           map[string]string         `json:"metadata,omitempty"`
	Firewall           map[string][]firewallRule `json:"firewall"`
	IdleTimeoutSeconds *int                      `json:"idleTimeoutSeconds,omitempty"`
}

// Create boots one VM for a session from its snapshot. The worker is launched
// separately by BootstrapWorker, which carries the one-time bootstrap ticket.
func (c *Client) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Environment, error) {
	snapshot := firstNonEmpty(spec.RootFS, c.defaultSnapshot)
	if snapshot == "" {
		return sandbox.Environment{}, errors.New("freestyle: no snapshot configured for this sandbox")
	}
	body := createVMRequest{
		SnapshotID: snapshot,
		Metadata:   spec.Labels,
		// The worker only dials out (control plane, GitHub, model APIs), so the
		// VM needs public egress and no inbound rule.
		Firewall: map[string][]firewallRule{"rules": {{
			Action:      "allow",
			Source:      map[string]any{},
			Destination: map[string]any{"public": true},
		}}},
	}
	if spec.SessionID != "" {
		body.Slug = SlugFor(spec.SessionID)
	}
	if spec.AutoPauseSeconds > 0 {
		idle := spec.AutoPauseSeconds
		body.IdleTimeoutSeconds = &idle
	}
	var view vmView
	if err := c.do(ctx, http.MethodPost, "/v5/vms", body, &view); err != nil {
		var httpErr *HTTPError
		if body.Slug != "" && errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict &&
			strings.Contains(httpErr.Body, "slug") {
			return sandbox.Environment{}, fmt.Errorf("%w: %s", sandbox.ErrAlreadyExists, body.Slug)
		}
		return sandbox.Environment{}, err
	}
	return toEnvironment(view), nil
}

// Get returns the current provider view of one VM.
func (c *Client) Get(ctx context.Context, id sandbox.ID) (sandbox.Environment, error) {
	var view vmView
	if err := c.do(ctx, http.MethodGet, "/v5/vms/"+url.PathEscape(string(id)), nil, &view); err != nil {
		return sandbox.Environment{}, err
	}
	return toEnvironment(view), nil
}

// FindBySession recovers a session's VM by the slug Create assigned it.
func (c *Client) FindBySession(ctx context.Context, sessionID string) (sandbox.Environment, bool, error) {
	environment, err := c.Get(ctx, sandbox.ID(SlugFor(sessionID)))
	if errors.Is(err, sandbox.ErrNotFound) {
		return sandbox.Environment{}, false, nil
	}
	if err != nil {
		return sandbox.Environment{}, false, err
	}
	return environment, true, nil
}

// Start resumes a paused VM, or boots a stopped one.
func (c *Client) Start(ctx context.Context, id sandbox.ID) error {
	return c.do(ctx, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/start", nil, nil)
}

// Stop pauses the VM: Freestyle keeps memory and disk, and bills storage only.
func (c *Client) Stop(ctx context.Context, id sandbox.ID) error {
	return c.Pause(ctx, id)
}

// Pause freezes the VM and saves its memory.
func (c *Client) Pause(ctx context.Context, id sandbox.ID) error {
	return c.do(ctx, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/pause", nil, nil)
}

// Resume restores a paused VM in place with its processes intact.
func (c *Client) Resume(ctx context.Context, id sandbox.ID) error {
	return c.Start(ctx, id)
}

// Delete reclaims a VM. An already-deleted VM is success.
func (c *Client) Delete(ctx context.Context, id sandbox.ID) error {
	err := c.do(ctx, http.MethodDelete, "/v5/vms/"+url.PathEscape(string(id)), nil, nil)
	if errors.Is(err, sandbox.ErrNotFound) {
		return nil
	}
	return err
}

// Recreate replaces a VM that cannot be restored with a fresh one from the
// session's snapshot. A session's VM is found again by its slug, so the old VM
// must be gone before the replacement can take the slug. Like a NodeOps
// recreate, uncommitted work on the old VM is lost.
func (c *Client) Recreate(ctx context.Context, id sandbox.ID, spec sandbox.Spec) (sandbox.Environment, error) {
	if err := c.Delete(ctx, id); err != nil {
		return sandbox.Environment{}, err
	}
	deadline := time.Now().Add(recreateDeleteWait)
	for {
		_, err := c.Get(ctx, id)
		if errors.Is(err, sandbox.ErrNotFound) {
			break
		}
		if err != nil {
			return sandbox.Environment{}, err
		}
		if !time.Now().Before(deadline) {
			return sandbox.Environment{}, fmt.Errorf("freestyle: VM %s still exists %s after its delete", id, recreateDeleteWait)
		}
		select {
		case <-ctx.Done():
			return sandbox.Environment{}, ctx.Err()
		case <-time.After(recreateDeletePoll):
		}
	}
	return c.Create(ctx, spec)
}

type execRequest struct {
	Command   string            `json:"command"`
	LinuxUser string            `json:"linuxUser,omitempty"`
	TimeoutMs int               `json:"timeoutMs,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type execResult struct {
	StatusCode *int   `json:"statusCode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
}

// BootstrapWorker launches the snapshot-baked worker in one exec. Unlike
// CreateOS there is no upload fallback yet: a snapshot without the worker is a
// mis-baked snapshot, reported as an error. A stale bake is fine, because the
// worker self-updates from the control plane.
func (c *Client) BootstrapWorker(
	ctx context.Context,
	id sandbox.ID,
	bootstrap sandbox.WorkerBootstrap,
) error {
	destination := strings.TrimSpace(bootstrap.Destination)
	if destination == "" || !strings.HasPrefix(destination, "/") {
		return fmt.Errorf("freestyle: worker destination %q must be an absolute path", bootstrap.Destination)
	}
	var script strings.Builder
	script.WriteString("set -e; ")
	script.WriteString("if ! [ -x " + shellQuote(destination) + " ]; then echo AO_WORKER_ABSENT; exit 0; fi; ")
	// Anchored so the pattern cannot match this shell, whose arguments contain
	// the destination; "|| true" tolerates the normal first-boot no-match.
	script.WriteString("{ pkill -f " + shellQuote("^"+destination+"( |$)") + " || true; }; ")
	// Repair issues a new one-time ticket, so pass this bootstrap's environment
	// rather than anything stored at creation. Freestyle restores a paused VM's
	// memory, so the agent is kept alive across the worker restart a wake does.
	environment := make(map[string]string, len(bootstrap.Environment)+1)
	for key, value := range bootstrap.Environment {
		environment[key] = value
	}
	environment["AO_WORKER_PERSIST_AGENT"] = "1"
	command := launchEnvironment(environment) + shellQuote(destination)
	if user := strings.TrimSpace(bootstrap.User); user != "" {
		quotedUser := shellQuote(user)
		script.WriteString("id -u " + quotedUser + " >/dev/null 2>&1 || " +
			"useradd --create-home --home-dir /workspace/.ao/home --shell /bin/bash " +
			quotedUser + "; ")
		// The snapshot bake and the project-snapshot clone already leave
		// /workspace owned by the worker user. A recursive chown over a cloned
		// repository costs seconds on every boot, so run it only when the
		// ownership is actually wrong (a snapshot from an older bake).
		script.WriteString("mkdir -p /workspace; [ \"$(stat -c %U /workspace)\" = " + quotedUser +
			" ] || chown -R " + quotedUser + ":" + quotedUser + " /workspace; ")
		command = "runuser --user " + quotedUser + " -- " + command
	}
	script.WriteString("setsid nohup " + command + " >> /var/log/ao-worker.log 2>&1 < /dev/null & ")
	script.WriteString("echo AO_WORKER_LAUNCHED")

	var result execResult
	if err := c.do(ctx, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/exec-await", execRequest{
		Command:   script.String(),
		LinuxUser: "root",
		TimeoutMs: int(bootstrapTimeout / time.Millisecond),
	}, &result); err != nil {
		return err
	}
	if result.StatusCode != nil && *result.StatusCode != 0 {
		return fmt.Errorf("freestyle: worker launch exited %d: %s",
			*result.StatusCode, truncate(strings.TrimSpace(result.Stderr), 512))
	}
	if strings.Contains(result.Stdout, "AO_WORKER_ABSENT") {
		return fmt.Errorf("freestyle: snapshot has no worker at %s", destination)
	}
	if !strings.Contains(result.Stdout, "AO_WORKER_LAUNCHED") {
		return fmt.Errorf("freestyle: worker launch did not confirm: %s", truncate(strings.TrimSpace(result.Stdout), 512))
	}
	c.log.Info("freestyle launched baked worker", "provider_id", id)
	return nil
}

// ExecRoot runs a shell command as root and returns its stdout, failing on a
// nonzero exit. env reaches only this command's process environment; it is not
// written into the VM, which keeps short-lived secrets out of any snapshot.
func (c *Client) ExecRoot(
	ctx context.Context,
	id sandbox.ID,
	command string,
	env map[string]string,
	timeout time.Duration,
) (string, error) {
	var result execResult
	if err := c.do(ctx, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/exec-await", execRequest{
		Command:   command,
		LinuxUser: "root",
		TimeoutMs: int(timeout / time.Millisecond),
		Env:       env,
	}, &result); err != nil {
		return "", err
	}
	if result.StatusCode != nil && *result.StatusCode != 0 {
		return result.Stdout, fmt.Errorf("freestyle: command exited %d: %s",
			*result.StatusCode, truncate(strings.TrimSpace(result.Stderr), 512))
	}
	return result.Stdout, nil
}

type snapshotResponse struct {
	SnapshotID string `json:"snapshotId"`
}

// Snapshot captures a VM's memory and disk under slug and returns the
// snapshot id, once it is ready to boot.
func (c *Client) Snapshot(ctx context.Context, id sandbox.ID, slug string) (string, error) {
	var response snapshotResponse
	body := map[string]string{}
	if slug != "" {
		body["slug"] = slug
	}
	if err := c.do(ctx, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/snapshot", body, &response); err != nil {
		return "", err
	}
	if response.SnapshotID == "" {
		return "", errors.New("freestyle: snapshot returned no id")
	}
	return response.SnapshotID, nil
}

// DeleteSnapshot removes a snapshot. A missing snapshot is success. VMs booted
// from it keep running: they do not depend on their source snapshot.
func (c *Client) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	err := c.do(ctx, http.MethodDelete, "/v5/snapshots/"+url.PathEscape(snapshotID), nil, nil)
	if errors.Is(err, sandbox.ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("freestyle: encode %s request: %w", path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("freestyle: %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return sandbox.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return &HTTPError{StatusCode: response.StatusCode, Body: truncate(strings.TrimSpace(string(raw)), 512)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("freestyle: decode %s response: %w", path, err)
	}
	return nil
}

func normalizeState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return sandbox.StateRunning
	case "paused":
		return sandbox.StatePaused
	case "stopped":
		return sandbox.StateStopped
	default:
		// starting, pausing, and anything new: never report running before the
		// worker has checked in.
		return sandbox.StateProvisioning
	}
}

func toEnvironment(view vmView) sandbox.Environment {
	return sandbox.Environment{
		ID:       sandbox.ID(view.ID),
		Name:     view.Slug,
		State:    normalizeState(view.State),
		Resource: domain.ResourceProfile{},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func launchEnvironment(environment map[string]string) string {
	if len(environment) == 0 {
		return ""
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, "env")
	for _, key := range keys {
		parts = append(parts, shellQuote(key+"="+environment[key]))
	}
	return strings.Join(parts, " ") + " "
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
