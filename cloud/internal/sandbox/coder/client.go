// Package coder implements AO's provider-neutral sandbox lifecycle against a
// customer-operated Coder deployment. It uses only Coder's authenticated HTTP
// and workspace-agent PTY APIs; AO never needs the customer's cloud account.
package coder

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// OrgConnectionLabel is the single bring-your-own Coder connection label an
// organization stores its credential under. The HTTP edge that seals the token
// and every service that later unseals it MUST pass the identical label so the
// AES-GCM associated data lines up.
const OrgConnectionLabel = "default"

const (
	defaultTimeout      = 2 * time.Minute
	maxResponseBody     = 4 << 20
	maxErrorBody        = 64 << 10
	maxPTYOutput        = 1 << 20
	workspaceNamePrefix = "ao-"
	bootstrapReady      = "__AO_BOOTSTRAP_READY__"
	bootstrapOK         = "__AO_BOOTSTRAP_OK__"
	bootstrapFailed     = "__AO_BOOTSTRAP_FAILED__"
	bootstrapUploadACK  = "__AO_UPLOAD_ACK__"
	bootstrapUploadDone = "__AO_UPLOAD_DONE__"
	// Coder rejects deadline extension requests less than 30 minutes in the
	// future. Keep the provider contract here rather than leaking it into the
	// provider-neutral reconciler.
	coderMinimumDeadlineLeadTime = 30 * time.Minute
	// Leave enough room for clock skew and request transit after AO computes the
	// deadline but before Coder validates it.
	coderDeadlineRequestMargin = time.Minute
	preinstalledMiss           = "__AO_PREINSTALLED_MISS__"
	bootstrapResultWait        = 2 * time.Minute
	// architectureMismatch is printed (with the workspace's uname -m) when the
	// CPU differs from the build AO chose from the agent's declared arch.
	architectureMismatch = "__AO_ARCH_MISMATCH__"
	workspaceProbe       = "__AO_WORKSPACE_PROBE__"
	// Coder workspace names are at most 32 characters.
	maxWorkspaceNameLength = 32
	// workspaceNameIDLength is the session-id suffix length of a prefixed name.
	workspaceNameIDLength = 12
)

// errTerminalNotReady marks a failure to obtain a usable workspace terminal
// before any bootstrap work began: the PTY could not be opened, closed (EOF),
// or stayed silent. Nothing was installed, so it is classified as retry-later
// rather than a failed install. The cause is not established (an agent that
// is not connected yet or reconnecting, or template-side gating), which is why
// every such attempt records the agent's status and lifecycle.
var errTerminalNotReady = errors.New("coder: workspace terminal is not ready")

// architectureMismatchError carries the workspace's uname -m when the in-script
// guard rejects the build AO selected.
type architectureMismatchError struct{ machine string }

func (e *architectureMismatchError) Error() string {
	return fmt.Sprintf("coder: workspace CPU architecture %q does not match the selected worker build", e.machine)
}

var (
	userPattern                       = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	errPreinstalledWorkerDoesNotMatch = errors.New("coder: preinstalled AO worker does not match")
)

// Config describes one Coder deployment and the template AO is allowed to use.
type Config struct {
	BaseURL    string
	Token      string
	Owner      string
	TemplateID string
	AgentName  string
	Parameters map[string]string
	HTTPClient *http.Client
}

// Client manages AO-owned workspaces through one dedicated Coder user.
type Client struct {
	baseURL               string
	token                 string
	owner                 string
	templateID            string
	agentName             string
	parameters            map[string]string
	workspaceNamePrefix   string
	expectedWorkspaceName string
	http                  *http.Client
}

var (
	_ sandbox.Provider         = (*Client)(nil)
	_ sandbox.Bootstrapper     = (*Client)(nil)
	_ sandbox.DeadlineExtender = (*Client)(nil)
)

// New creates a fail-closed Coder provider client.
func New(config Config) (*Client, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("coder: base URL must be an absolute http or https origin")
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("coder: API token is required")
	}
	// Owner and template are optional at construction so a client can be built
	// from only a base URL and token — the shape a bring-your-own-Coder org first
	// saves, where the owner is derived from the token and the template is chosen
	// per project. They are required at the point they are used: Create guards both,
	// and a real session always carries them on its immutable profile (see
	// ForSandbox). A template that IS supplied must still be a UUID.
	owner := strings.TrimSpace(config.Owner)
	templateID := strings.TrimSpace(config.TemplateID)
	if templateID != "" {
		parsed, err := uuid.Parse(templateID)
		if err != nil {
			return nil, errors.New("coder: template ID must be a UUID")
		}
		templateID = parsed.String()
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	parameters := make(map[string]string, len(config.Parameters))
	for name, value := range config.Parameters {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("coder: template parameter name must not be empty")
		}
		parameters[name] = value
	}
	return &Client{
		baseURL:    strings.TrimRight(endpoint.String(), "/"),
		token:      strings.TrimSpace(config.Token),
		owner:      owner,
		templateID: templateID,
		agentName:  strings.TrimSpace(config.AgentName),
		parameters: parameters,
		http:       httpClient,
	}, nil
}

// NewForOrg decrypts an organization's stored bring-your-own Coder connection
// token and builds a client bound to the supplied non-secret contract. It is the
// single place that turns an encrypted per-organization connection into a live
// client, so the AES-GCM associated-data construction and the token zeroing live
// in exactly one spot — shared by the sandbox resolver (which provisions a
// session's workspace) and the HTTP template-list edge (which reads the org's
// templates). The decrypted token is zeroed before return; New has already copied
// it into the returned client, so the client remains usable afterwards.
func NewForOrg(cipher *secrets.Cipher, orgID string, encrypted, nonce []byte, config Config) (*Client, error) {
	if cipher == nil {
		return nil, errors.New("coder: secrets cipher is required for a per-organization connection")
	}
	token, err := cipher.Decrypt(
		encrypted, nonce,
		secrets.ProviderConnectionAssociatedData(orgID, sandbox.ProviderCoder, OrgConnectionLabel),
	)
	if err != nil {
		return nil, fmt.Errorf("coder: decrypt per-organization token: %w", err)
	}
	defer clear(token)
	config.Token = string(token)
	return New(config)
}

// ForSandbox binds a connection credential to the non-secret Coder contract
// stored on one session. The returned client is safe to use only for that
// session's deterministic workspace identity. It serves both the shared,
// env-configured deployment client and a fresh per-organization client the
// resolver builds for a bring-your-own-Coder session — the latter is constructed
// with the session profile's own BaseURL, so it clears the equality guard below
// by construction. The guard is retained because it still protects the shared
// deployment client: its token must never be sent to a deployment other than the
// one it was configured for.
func (c *Client) ForSandbox(record domain.Sandbox) (sandbox.Provider, error) {
	if strings.TrimSpace(record.SessionID) == "" {
		return nil, errors.New("coder: durable session ID is required")
	}
	profile, err := sandbox.DecodeCoderSessionProfile(record.ResourceProfile)
	if err != nil {
		return nil, fmt.Errorf("coder: resolve durable session profile: %w", err)
	}
	if profile.BaseURL != c.baseURL {
		return nil, fmt.Errorf(
			"coder: durable session deployment %q does not match configured deployment %q",
			profile.BaseURL, c.baseURL,
		)
	}
	templateID, err := uuid.Parse(profile.TemplateID)
	if err != nil {
		return nil, errors.New("coder: durable session template ID must be a UUID")
	}
	parameters := make(map[string]string, len(profile.Parameters))
	for name, value := range profile.Parameters {
		parameters[name] = value
	}
	sessionClient := *c
	sessionClient.owner = profile.Owner
	sessionClient.templateID = templateID.String()
	sessionClient.agentName = profile.AgentName
	sessionClient.parameters = parameters
	sessionClient.workspaceNamePrefix = profile.WorkspaceNamePrefix
	sessionClient.expectedWorkspaceName = WorkspaceNameWithPrefix(profile.WorkspaceNamePrefix, record.SessionID)
	return &sessionClient, nil
}

// CurrentUser returns the username of the Coder account the client's API token
// authenticates as (GET /api/v2/users/me). A bring-your-own-Coder organization
// saves only a base URL and token; the workspace owner is this user, derived once
// at save time instead of being pasted. The call needs neither an owner nor a
// template, so a minimal client (base URL + token) can make it.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/users/me", nil, &me); err != nil {
		return "", fmt.Errorf("coder: resolve current user: %w", err)
	}
	username := strings.TrimSpace(me.Username)
	if username == "" {
		return "", errors.New("coder: current user has no username")
	}
	return username, nil
}

// Template is a non-secret summary of a Coder template a client may pick from.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	// Parameters is the set of per-workspace coder_parameter names the template's
	// active version declares (e.g. "size", "startup_script"). The picker uses it
	// to offer only the controls a template can actually accept: sending a rich
	// parameter a template does not declare makes Coder reject the build.
	Parameters []string `json:"parameters"`
}

// ListTemplates returns the templates the configured Coder user can see, for a
// client-facing template picker. It is read-only and does not affect the
// deployment's default template (which still governs any session that does not
// explicitly choose one). Each template is annotated with the parameter names
// its active version declares; a template whose parameters cannot be read is
// still returned, with an empty parameter set, so a transient read does not hide
// it from the picker.
func (c *Client) ListTemplates(ctx context.Context) ([]Template, error) {
	var raw []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		DisplayName     string `json:"display_name"`
		Description     string `json:"description"`
		Icon            string `json:"icon"`
		ActiveVersionID string `json:"active_version_id"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/templates", nil, &raw); err != nil {
		return nil, fmt.Errorf("coder: list templates: %w", err)
	}
	templates := make([]Template, 0, len(raw))
	for _, t := range raw {
		params, err := c.templateVersionParameterNames(ctx, t.ActiveVersionID)
		if err != nil {
			// Best-effort: keep the template selectable even if its parameter
			// list is momentarily unreadable. The picker degrades to hiding the
			// size/startup controls for it, which is the safe default.
			params = nil
		}
		templates = append(templates, Template{
			ID:          t.ID,
			Name:        t.Name,
			DisplayName: t.DisplayName,
			Description: t.Description,
			Icon:        t.Icon,
			Parameters:  params,
		})
	}
	return templates, nil
}

// templateVersionParameterNames returns the coder_parameter names declared by a
// template version. It is used to gate the client picker so it only offers
// controls the chosen template can accept.
func (c *Client) templateVersionParameterNames(ctx context.Context, versionID string) ([]string, error) {
	if versionID == "" {
		return nil, nil
	}
	var raw []struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/templateversions/"+url.PathEscape(versionID)+"/rich-parameters", nil, &raw); err != nil {
		return nil, fmt.Errorf("coder: template version parameters: %w", err)
	}
	names := make([]string, 0, len(raw))
	for _, p := range raw {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names, nil
}

type workspace struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	OwnerName   string          `json:"owner_name"`
	TemplateID  string          `json:"template_id"`
	LatestBuild workspaceBuild  `json:"latest_build"`
	Health      workspaceHealth `json:"health"`
}

type workspaceHealth struct {
	Healthy bool `json:"healthy"`
}

type workspaceBuild struct {
	Status    string              `json:"status"`
	Reason    string              `json:"reason"`
	Deadline  *time.Time          `json:"deadline"`
	Resources []workspaceResource `json:"resources"`
}

type workspaceResource struct {
	Agents []workspaceAgent `json:"agents"`
}

type workspaceAgent struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	LifecycleState string          `json:"lifecycle_state"`
	Architecture   string          `json:"architecture"`
	Health         workspaceHealth `json:"health"`
}

// agentReady reports whether a Coder agent can take a worker bootstrap. The
// agent must be connected and its startup must have finished: "created" and
// "starting" mean the template's startup script is still running, and the
// workspace (repositories, tools) is not yet in the state the template
// promises, so AO waits instead of racing it. A script that failed
// ("start_error") or overran its timeout ("start_timeout") leaves a usable
// workspace, so AO bootstraps it rather than waiting forever. Coder reports
// those two states as unhealthy, which is why health is not consulted.
func agentReady(agent workspaceAgent) bool {
	if agent.ID == "" || agent.Status != "connected" {
		return false
	}
	switch agent.LifecycleState {
	case "ready", "start_error", "start_timeout":
		return true
	default:
		return false
	}
}

type buildParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type createWorkspaceRequest struct {
	TemplateID          string           `json:"template_id"`
	Name                string           `json:"name"`
	RichParameterValues []buildParameter `json:"rich_parameter_values,omitempty"`
	AutomaticUpdates    string           `json:"automatic_updates"`
}

// WorkspaceName is the stable default Coder workspace name for one AO session.
func WorkspaceName(sessionID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID)))
	return workspaceNamePrefix + hex.EncodeToString(sum[:])[:24]
}

// WorkspaceNameWithPrefix is the stable Coder workspace name for one AO session
// whose project chose a workspace name prefix: <prefix>-<short session id>. The
// id is the leading hex of the session UUID (random for v4 ids), so the name is
// recognizable from the session yet unique per owner. An empty prefix keeps the
// default ao-<id> name exactly. The result always fits Coder's 32-character
// limit because prefixes are validated to at most 20 characters.
func WorkspaceNameWithPrefix(prefix, sessionID string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return WorkspaceName(sessionID)
	}
	idLength := min(workspaceNameIDLength, maxWorkspaceNameLength-len(prefix)-1)
	var compact strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(sessionID)) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			compact.WriteRune(character)
		}
	}
	id := compact.String()
	if len(id) < idLength {
		// Not a UUID-shaped id: fall back to the same hash the default uses.
		sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID)))
		id = hex.EncodeToString(sum[:])
	}
	return prefix + "-" + id[:idLength]
}

func (c *Client) workspaceName(sessionID string) string {
	return WorkspaceNameWithPrefix(c.workspaceNamePrefix, sessionID)
}

// Create provisions a Coder workspace from the configured template.
func (c *Client) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Environment, error) {
	name := strings.TrimSpace(spec.Name)
	if spec.SessionID != "" {
		name = c.workspaceName(spec.SessionID)
	}
	if name == "" {
		return sandbox.Environment{}, errors.New("coder: workspace name is required")
	}
	if c.expectedWorkspaceName != "" && name != c.expectedWorkspaceName {
		return sandbox.Environment{}, fmt.Errorf(
			"coder: session workspace name mismatch: got %q, want %q",
			name, c.expectedWorkspaceName,
		)
	}
	// Owner and template are optional on a freshly-connected client but required to
	// create a workspace. A real session always supplies both via its immutable
	// profile (ForSandbox); failing here gives a clear message if one is ever missing.
	if strings.TrimSpace(c.owner) == "" {
		return sandbox.Environment{}, errors.New("coder: workspace owner is required")
	}
	if strings.TrimSpace(c.templateID) == "" {
		return sandbox.Environment{}, errors.New("coder: a template must be selected")
	}
	parameterNames := make([]string, 0, len(c.parameters))
	for parameterName := range c.parameters {
		parameterNames = append(parameterNames, parameterName)
	}
	sort.Strings(parameterNames)
	parameters := make([]buildParameter, 0, len(parameterNames))
	for _, parameterName := range parameterNames {
		parameters = append(parameters, buildParameter{Name: parameterName, Value: c.parameters[parameterName]})
	}
	body := createWorkspaceRequest{
		TemplateID: c.templateID, Name: name,
		RichParameterValues: parameters, AutomaticUpdates: "never",
	}
	var view workspace
	if err := c.do(ctx, http.MethodPost, "/api/v2/users/"+url.PathEscape(c.owner)+"/workspaces", body, &view); err != nil {
		return sandbox.Environment{}, err
	}
	if err := c.validateWorkspaceIdentity(view, name); err != nil {
		return sandbox.Environment{}, err
	}
	return c.toEnvironment(view), nil
}

// Get returns the current provider view of one Coder workspace.
func (c *Client) Get(ctx context.Context, id sandbox.ID) (sandbox.Environment, error) {
	var view workspace
	if err := c.do(ctx, http.MethodGet, "/api/v2/workspaces/"+url.PathEscape(string(id)), nil, &view); err != nil {
		return sandbox.Environment{}, err
	}
	if c.expectedWorkspaceName != "" {
		if err := c.validateWorkspaceIdentity(view, c.expectedWorkspaceName); err != nil {
			return sandbox.Environment{}, err
		}
	}
	return c.toEnvironment(view), nil
}

// FindBySession recovers a workspace after a control-plane crash between
// provider creation and persistence of the returned Coder workspace ID.
func (c *Client) FindBySession(ctx context.Context, sessionID string) (sandbox.Environment, bool, error) {
	expectedName := c.workspaceName(sessionID)
	if c.expectedWorkspaceName != "" && expectedName != c.expectedWorkspaceName {
		return sandbox.Environment{}, false, fmt.Errorf(
			"coder: session workspace name mismatch: got %q, want %q",
			expectedName, c.expectedWorkspaceName,
		)
	}
	var view workspace
	requestPath := "/api/v2/users/" + url.PathEscape(c.owner) + "/workspace/" +
		url.PathEscape(expectedName)
	err := c.do(ctx, http.MethodGet, requestPath, nil, &view)
	if errors.Is(err, sandbox.ErrNotFound) {
		return sandbox.Environment{}, false, nil
	}
	if err != nil {
		return sandbox.Environment{}, false, err
	}
	if err := c.validateWorkspaceIdentity(view, expectedName); err != nil {
		return sandbox.Environment{}, false, err
	}
	if normalizeState(view.LatestBuild.Status) == sandbox.StateDeleted {
		return sandbox.Environment{}, false, nil
	}
	return c.toEnvironment(view), true, nil
}

func (c *Client) validateWorkspaceIdentity(view workspace, expectedName string) error {
	if view.Name != expectedName {
		return fmt.Errorf(
			"coder: workspace name mismatch: got %q, want %q", view.Name, expectedName,
		)
	}
	if view.OwnerName != c.owner {
		return fmt.Errorf(
			"coder: workspace owner mismatch: got %q, want %q", view.OwnerName, c.owner,
		)
	}
	if view.TemplateID != c.templateID {
		return fmt.Errorf(
			"coder: workspace template mismatch: got %q, want %q", view.TemplateID, c.templateID,
		)
	}
	return nil
}

func (c *Client) Start(ctx context.Context, id sandbox.ID) error {
	return c.transition(ctx, id, "start")
}

func (c *Client) Stop(ctx context.Context, id sandbox.ID) error {
	return c.transition(ctx, id, "stop")
}

func (c *Client) Pause(ctx context.Context, id sandbox.ID) error {
	return c.Stop(ctx, id)
}

func (c *Client) Resume(ctx context.Context, id sandbox.ID) error {
	return c.Start(ctx, id)
}

// ExtendDeadline keeps a Coder workspace alive while AO has durable active
// work or recent user interaction. Coder applies template maximum-runtime
// policy to this request.
func (c *Client) ExtendDeadline(ctx context.Context, id sandbox.ID, deadline time.Time) error {
	minimumDeadline := time.Now().UTC().Add(coderMinimumDeadlineLeadTime + coderDeadlineRequestMargin)
	if deadline.Before(minimumDeadline) {
		deadline = minimumDeadline
	}
	return c.do(ctx, http.MethodPut,
		"/api/v2/workspaces/"+url.PathEscape(string(id))+"/extend",
		struct {
			Deadline time.Time `json:"deadline"`
		}{Deadline: deadline.UTC()}, nil)
}

func (c *Client) Delete(ctx context.Context, id sandbox.ID) error {
	err := c.transition(ctx, id, "delete")
	if errors.Is(err, sandbox.ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) transition(ctx context.Context, id sandbox.ID, transition string) error {
	return c.do(ctx, http.MethodPost, "/api/v2/workspaces/"+url.PathEscape(string(id))+"/builds",
		map[string]string{"transition": transition}, nil)
}

func (c *Client) toEnvironment(view workspace) sandbox.Environment {
	agent, _ := c.selectAgent(view)
	state := normalizeState(view.LatestBuild.Status)
	if state == sandbox.StateRunning && !agentReady(agent) {
		state = sandbox.StateProvisioning
	}
	environment := sandbox.Environment{
		ID: sandbox.ID(view.ID), Name: view.Name, State: state, Target: agent.ID,
		Resource: domain.ResourceProfile{}, Deadline: view.LatestBuild.Deadline,
	}
	if state == sandbox.StateStopped {
		switch strings.ToLower(strings.TrimSpace(view.LatestBuild.Reason)) {
		case "autostop", "dormancy":
			environment.StopCause = sandbox.StopCauseExternalIdle
		}
	}
	return environment
}

func normalizeState(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return sandbox.StateRunning
	case "stopped":
		return sandbox.StateStopped
	case "deleting":
		return sandbox.StateDeleting
	case "deleted":
		return sandbox.StateDeleted
	default:
		return sandbox.StateProvisioning
	}
}

func (c *Client) selectAgent(view workspace) (workspaceAgent, bool) {
	for _, resource := range view.LatestBuild.Resources {
		for _, agent := range resource.Agents {
			if c.agentName == "" || agent.Name == c.agentName {
				return agent, true
			}
		}
	}
	return workspaceAgent{}, false
}

// BootstrapWorker installs and starts AO through the Coder agent PTY. The
// bootstrap archive travels as terminal input, so worker credentials never
// appear in the Coder request URL, process arguments, or control-plane logs.
//
// The archive still carries the worker binary because the Coder workspace
// template is customer-operated and external: this repo cannot bake ao-worker
// into it. The launch environment written here already includes
// AO_WORKER_EXPECTED_SHA256, so a baked coder worker self-heals to the control
// plane's exact build. Eliminating the multi-megabyte stream (mirroring the
// createos launch-baked path) is a fast-follow gated on the customer template
// baking ao-worker at Destination; until then the stream remains the delivery
// channel.
func (c *Client) BootstrapWorker(ctx context.Context, id sandbox.ID, bootstrap sandbox.WorkerBootstrap) error {
	if err := validateBootstrap(bootstrap); err != nil {
		return err
	}
	var view workspace
	if err := c.do(ctx, http.MethodGet, "/api/v2/workspaces/"+url.PathEscape(string(id)), nil, &view); err != nil {
		return err
	}
	expectedName := c.expectedWorkspaceName
	if expectedName == "" {
		expectedName = c.workspaceName(bootstrap.DurableIdentity)
	}
	if err := c.validateWorkspaceIdentity(view, expectedName); err != nil {
		return err
	}
	agent, ok := c.selectAgent(view)
	if !ok || !agentReady(agent) {
		// Never bootstrap while the agent is still starting: the template's
		// startup script has not finished preparing the workspace yet.
		return fmt.Errorf("coder: workspace agent is not ready (status %q, lifecycle %q): %w",
			agent.Status, agent.LifecycleState, sandbox.ErrWorkspaceNotReady)
	}
	ptyURL, err := url.Parse(c.baseURL + "/api/v2/workspaceagents/" + url.PathEscape(agent.ID) + "/pty")
	if err != nil {
		return fmt.Errorf("coder: build PTY URL: %w", err)
	}

	// The agent's declared architecture (the coder_agent arch the template must
	// set for its own agent binary to run) picks the worker build. The bootstrap
	// script re-checks it with uname -m before anything is installed. A probe is
	// needed only to resolve a $HOME durable root, and it reports uname -m too.
	arch := normalizeArchitecture(agent.Architecture)
	if arch == "" && strings.TrimSpace(agent.Architecture) != "" {
		return unsupportedArchitectureError(agent.Architecture)
	}
	if arch == "" {
		arch = sandbox.ArchAMD64
	}
	home := ""
	if sandbox.NormalizeCoderDurableRoot(bootstrap.DurableRoot) == sandbox.CoderHomeDurableRoot {
		probe, err := c.probeWorkspace(ctx, id, ptyURL)
		if err != nil {
			return classifyBootstrapError(err, bootstrap.DurableRoot)
		}
		arch = normalizeArchitecture(probe.machine)
		if arch == "" {
			return unsupportedArchitectureError(probe.machine)
		}
		home = probe.home
	}
	resolved, err := resolveWorkerBootstrap(bootstrap, arch, home)
	if err != nil {
		return err
	}
	err = c.bootstrapResolvedWorker(ctx, id, ptyURL, resolved, arch)
	var mismatch *architectureMismatchError
	if errors.As(err, &mismatch) {
		detected := normalizeArchitecture(mismatch.machine)
		if detected == "" || detected == arch {
			return unsupportedArchitectureError(mismatch.machine)
		}
		if resolved, err = resolveWorkerBootstrap(bootstrap, detected, home); err != nil {
			return err
		}
		err = c.bootstrapResolvedWorker(ctx, id, ptyURL, resolved, detected)
		if errors.As(err, &mismatch) {
			return unsupportedArchitectureError(mismatch.machine)
		}
	}
	if err != nil {
		return classifyBootstrapError(err, resolved.DurableRoot)
	}
	return nil
}

// bootstrapResolvedWorker installs one architecture's worker. Fast path: an
// approved template can bake the exact worker and helper from the control-plane
// image. Verify both hashes inside the workspace, then send only the small
// launch environment through the PTY. A stale or unmodified customer template
// explicitly falls through to the full binary upload.
func (c *Client) bootstrapResolvedWorker(
	ctx context.Context,
	id sandbox.ID,
	ptyURL *url.URL,
	bootstrap sandbox.WorkerBootstrap,
	arch string,
) error {
	launchPayload, err := bootstrapLaunchArchive(bootstrap)
	if err != nil {
		return err
	}
	if err := c.bootstrapWorkerThroughPTY(ctx, id, ptyURL, bootstrap, arch, launchPayload, true); err == nil {
		return nil
	} else if !errors.Is(err, errPreinstalledWorkerDoesNotMatch) {
		return fmt.Errorf("coder: launch preinstalled worker: %w", err)
	}

	payload, err := bootstrapArchive(bootstrap)
	if err != nil {
		return err
	}
	if err := c.bootstrapWorkerThroughPTY(ctx, id, ptyURL, bootstrap, arch, payload, false); err != nil {
		return fmt.Errorf("coder: bootstrap worker after PTY retries: %w", err)
	}
	return nil
}

// normalizeArchitecture maps a Coder agent architecture or a uname -m machine
// name onto the GOARCH of a worker build AO ships, or "" when there is none.
func normalizeArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x86_64", "x64":
		return sandbox.ArchAMD64
	case "arm64", "aarch64", "armv8", "armv8l":
		return sandbox.ArchARM64
	default:
		return ""
	}
}

func unsupportedArchitectureError(machine string) error {
	machine = strings.TrimSpace(machine)
	if machine == "" {
		machine = "unknown"
	}
	return &sandbox.StartupError{
		Code:    sandbox.StartupErrorUnsupportedArchitecture,
		Message: fmt.Sprintf("This workspace's CPU architecture (%s) isn't supported. AO workers run on x86_64 (amd64) and arm64 workspaces.", machine),
		Err:     fmt.Errorf("coder: no worker build for architecture %q", machine),
	}
}

// resolveWorkerBootstrap specializes a bootstrap for one workspace: it selects
// the worker build for arch, advertises that build's hashes (so the worker's
// self-update never "heals" it to another architecture's binary), and, when
// the durable root is the symbolic $HOME, substitutes the workspace user's
// real home directory into the root and every derived environment path.
func resolveWorkerBootstrap(bootstrap sandbox.WorkerBootstrap, arch, home string) (sandbox.WorkerBootstrap, error) {
	build, ok := bootstrap.Builds[arch]
	if !ok && arch == sandbox.ArchAMD64 {
		build, ok = sandbox.WorkerBuild{Binary: bootstrap.Binary, HelperBinary: bootstrap.HelperBinary}, true
	}
	if !ok || len(build.Binary) == 0 {
		return sandbox.WorkerBootstrap{}, unsupportedArchitectureError(arch)
	}
	resolved := bootstrap
	resolved.Binary = build.Binary
	resolved.HelperBinary = build.HelperBinary
	resolved.Environment = make(map[string]string, len(bootstrap.Environment)+1)
	for key, value := range bootstrap.Environment {
		resolved.Environment[key] = value
	}
	if _, advertised := resolved.Environment["AO_WORKER_EXPECTED_SHA256"]; advertised {
		resolved.Environment["AO_WORKER_EXPECTED_SHA256"] = sha256Hex(build.Binary)
	}
	if _, advertised := resolved.Environment["AO_WORKER_HELPER_EXPECTED_SHA256"]; advertised {
		if len(build.HelperBinary) > 0 {
			resolved.Environment["AO_WORKER_HELPER_EXPECTED_SHA256"] = sha256Hex(build.HelperBinary)
		} else {
			delete(resolved.Environment, "AO_WORKER_HELPER_EXPECTED_SHA256")
		}
	}
	resolved.Environment["AO_WORKER_EXPECTED_ARCH"] = arch

	if sandbox.NormalizeCoderDurableRoot(bootstrap.DurableRoot) == sandbox.CoderHomeDurableRoot {
		home = strings.TrimSpace(home)
		if !sandbox.SafeCoderDurableRoot(home) {
			return sandbox.WorkerBootstrap{}, &sandbox.StartupError{
				Code:    sandbox.StartupErrorDurableRootUnavailable,
				Message: fmt.Sprintf("AO couldn't use the workspace user's home directory (%q) to store session data.", home),
				Err:     errors.New("coder: workspace home directory is not a safe absolute path"),
			}
		}
		resolved.DurableRoot = home
		for key, value := range resolved.Environment {
			if value == sandbox.CoderHomeDurableRoot {
				resolved.Environment[key] = home
			} else if rest, found := strings.CutPrefix(value, sandbox.CoderHomeDurableRoot+"/"); found {
				resolved.Environment[key] = path.Join(home, rest)
			}
		}
	}
	return resolved, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// classifyBootstrapError attaches the user-facing startup error a failed
// bootstrap should surface. Errors already classified pass through.
func classifyBootstrapError(err error, durableRoot string) error {
	var startupErr *sandbox.StartupError
	if err == nil || errors.As(err, &startupErr) || errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, errTerminalNotReady) {
		return &sandbox.StartupError{
			Code:    sandbox.StartupErrorTerminalUnavailable,
			Message: "AO couldn't open a terminal in the workspace yet; it may still be starting up. AO will keep retrying.",
			Err:     errors.Join(sandbox.ErrWorkspaceNotReady, err),
		}
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "is not a mounted directory"):
		return &sandbox.StartupError{
			Code:    sandbox.StartupErrorDurableRootUnavailable,
			Message: fmt.Sprintf("The workspace directory AO stores session data in (%s) isn't a mounted volume, which this Coder connection requires.", durableRoot),
			Err:     err,
		}
	case strings.Contains(text, "belongs to a different AO session"),
		strings.Contains(text, "did not survive workspace stop/start"),
		strings.Contains(text, "must not be a symbolic link"):
		return &sandbox.StartupError{
			Code:    sandbox.StartupErrorDurableRootUnavailable,
			Message: fmt.Sprintf("AO couldn't safely reuse the session data in the workspace directory %s.", durableRoot),
			Err:     err,
		}
	case strings.Contains(text, "sudo:") && strings.Contains(text, "password"):
		return &sandbox.StartupError{
			Code:    sandbox.StartupErrorBootstrapFailed,
			Message: "AO needs passwordless sudo in the workspace to install its worker.",
			Err:     err,
		}
	default:
		return &sandbox.StartupError{
			Code:    sandbox.StartupErrorBootstrapFailed,
			Message: "AO couldn't start its worker in the workspace.",
			Err:     err,
		}
	}
}

type workspaceProbeResult struct {
	machine string
	home    string
}

// probeWorkspace runs a one-line command in the workspace to learn its CPU
// (uname -m) and the agent user's home directory. Terminal failures are
// retried briefly, then reported as errTerminalNotReady.
func (c *Client) probeWorkspace(ctx context.Context, id sandbox.ID, ptyURL *url.URL) (workspaceProbeResult, error) {
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("width", "120")
	query.Set("height", "40")
	query.Set("command", "sh -c "+shellQuote(`printf '%s:%s:%s\n' `+workspaceProbe+` "$(uname -m)" "$HOME"`))
	query.Set("backend_type", "buffered")
	attemptURL.RawQuery = query.Encode()
	const probeAttempts = 3
	var attempts terminalAttempts
	for attempt := 0; attempt < probeAttempts; attempt++ {
		result, err := c.probeWorkspaceOnce(ctx, &attemptURL)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return workspaceProbeResult{}, ctx.Err()
		}
		attempts.record(ctx, c, id, attempt, err)
		if attempt+1 < probeAttempts {
			if err := sleepPTYBackoff(ctx, attempt, err); err != nil {
				return workspaceProbeResult{}, err
			}
		}
	}
	return workspaceProbeResult{}, attempts.err()
}

// ptyRetryBaseDelay and ptyRetryMaxDelay bound the exponential backoff between
// workspace terminal attempts (1s, 2s, 4s, 8s, ...).
const (
	ptyRetryBaseDelay = time.Second
	ptyRetryMaxDelay  = 8 * time.Second
)

// sleepPTYBackoff waits before the next terminal attempt: exponentially after a
// terminal that closed or stayed silent (the agent may still be connecting),
// and the historical fixed second after any other failure.
func sleepPTYBackoff(ctx context.Context, attempt int, cause error) error {
	delay := ptyRetryBaseDelay
	if errors.Is(cause, errTerminalNotReady) {
		delay = min(ptyRetryBaseDelay<<attempt, ptyRetryMaxDelay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// terminalAttempts accumulates failed workspace terminal attempts. A terminal
// failure (EOF, close, silence) is annotated with the Coder agent's status and
// lifecycle at that moment, so the logged error shows what the agent was doing
// each time: the cause of such EOFs is not established.
type terminalAttempts struct {
	last  error
	notes []string
}

func (a *terminalAttempts) record(ctx context.Context, c *Client, id sandbox.ID, attempt int, err error) {
	a.last = err
	note := fmt.Sprintf("attempt %d: %v", attempt+1, err)
	if errors.Is(err, errTerminalNotReady) {
		note += " [" + c.describeAgent(ctx, id) + "]"
	}
	a.notes = append(a.notes, note)
}

func (a *terminalAttempts) err() error {
	if a.last == nil || len(a.notes) < 2 && !errors.Is(a.last, errTerminalNotReady) {
		return a.last
	}
	return fmt.Errorf("%w (%s)", a.last, strings.Join(a.notes, "; "))
}

// describeAgent reports the selected agent's status and lifecycle for a
// diagnostic note. It never fails the caller.
func (c *Client) describeAgent(ctx context.Context, id sandbox.ID) string {
	var view workspace
	if err := c.do(ctx, http.MethodGet, "/api/v2/workspaces/"+url.PathEscape(string(id)), nil, &view); err != nil {
		return "agent state unavailable: " + err.Error()
	}
	agent, ok := c.selectAgent(view)
	if !ok {
		return "agent not found"
	}
	return fmt.Sprintf("agent status=%q lifecycle=%q", agent.Status, agent.LifecycleState)
}

func (c *Client) probeWorkspaceOnce(ctx context.Context, ptyURL *url.URL) (workspaceProbeResult, error) {
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("reconnect", uuid.NewString())
	attemptURL.RawQuery = query.Encode()
	conn, response, err := websocket.Dial(ctx, attemptURL.String(), &websocket.DialOptions{
		HTTPClient: c.http, HTTPHeader: http.Header{"Coder-Session-Token": []string{c.token}},
	})
	if err != nil {
		if response != nil {
			response.Body.Close()
			return workspaceProbeResult{}, fmt.Errorf("%w: open workspace PTY returned %d", errTerminalNotReady, response.StatusCode)
		}
		return workspaceProbeResult{}, fmt.Errorf("%w: open workspace PTY: %v", errTerminalNotReady, err)
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	netConn := websocket.NetConn(streamCtx, conn, websocket.MessageBinary)
	output, outputDone := streamPTYOutput(streamCtx, netConn)
	defer func() {
		stopStream()
		_ = conn.CloseNow()
		<-outputDone
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return workspaceProbeResult{}, ctx.Err()
		case <-timer.C:
			return workspaceProbeResult{}, fmt.Errorf("%w: workspace probe produced no output", errTerminalNotReady)
		case line, ok := <-output:
			if !ok {
				return workspaceProbeResult{}, fmt.Errorf("%w: workspace PTY closed before the probe reported", errTerminalNotReady)
			}
			if _, rest, found := strings.Cut(line.data, workspaceProbe+":"); found {
				machine, home, _ := strings.Cut(strings.TrimRight(rest, "\r\n"), ":")
				return workspaceProbeResult{machine: strings.TrimSpace(machine), home: strings.TrimSpace(home)}, nil
			}
			if line.err != nil {
				return workspaceProbeResult{}, fmt.Errorf("%w: read workspace probe: %v", errTerminalNotReady, line.err)
			}
		}
	}
}

func (c *Client) bootstrapWorkerThroughPTY(
	ctx context.Context,
	id sandbox.ID,
	ptyURL *url.URL,
	bootstrap sandbox.WorkerBootstrap,
	arch string,
	payload []byte,
	preinstalled bool,
) error {
	encoded := base64.StdEncoding.EncodeToString(payload)
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("width", "120")
	query.Set("height", "40")
	query.Set("command", bootstrapCommandForArchive(bootstrap, arch, len(encoded), preinstalled))
	// Bootstrap is a short-lived, non-interactive command. The buffered backend
	// preserves the final result after the upload while AO keeps the PTY open.
	query.Set("backend_type", "buffered")
	attemptURL.RawQuery = query.Encode()
	const bootstrapAttempts = 5
	var attempts terminalAttempts
	for attempt := 0; attempt < bootstrapAttempts; attempt++ {
		err := c.bootstrapThroughPTY(ctx, &attemptURL, encoded)
		if err == nil {
			return nil
		}
		var mismatch *architectureMismatchError
		if errors.Is(err, errPreinstalledWorkerDoesNotMatch) || errors.As(err, &mismatch) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempts.record(ctx, c, id, attempt, err)
		if attempt+1 < bootstrapAttempts {
			if err := sleepPTYBackoff(ctx, attempt, err); err != nil {
				return err
			}
		}
	}
	return attempts.err()
}

func (c *Client) bootstrapThroughPTY(ctx context.Context, ptyURL *url.URL, encoded string) error {
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("reconnect", uuid.NewString())
	attemptURL.RawQuery = query.Encode()

	headers := http.Header{"Coder-Session-Token": []string{c.token}}
	conn, response, err := websocket.Dial(ctx, attemptURL.String(), &websocket.DialOptions{
		HTTPClient: c.http, HTTPHeader: headers,
	})
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			snippet, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
			return fmt.Errorf("%w: open workspace PTY returned %d: %s", errTerminalNotReady, response.StatusCode,
				strings.TrimSpace(string(snippet)))
		}
		return fmt.Errorf("%w: open workspace PTY: %v", errTerminalNotReady, err)
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	netConn := websocket.NetConn(streamCtx, conn, websocket.MessageBinary)
	output, outputDone := streamPTYOutput(streamCtx, netConn)
	defer func() {
		stopStream()
		_ = conn.CloseNow()
		<-outputDone
	}()

	if err := waitForBootstrapReady(ctx, output); err != nil {
		return err
	}
	encoder := json.NewEncoder(netConn)
	// Coder's reconnecting PTY writes each decoded Data field to the OS PTY but
	// does not retry a short write. Frame the archive as canonical terminal lines
	// with sequence and length metadata. The receiver acknowledges only a
	// complete frame for the expected sequence; retrying an unacknowledged frame
	// makes Coder's unreported short writes recoverable without tripling the
	// shell work and exceeding the reconnecting-PTY lifetime.
	const (
		chunkSize    = 3_000 // below the canonical terminal's 4 KiB line ceiling
		uploadWindow = 8     // bound PTY buffering while amortizing WAN round trips
	)
	sequence := 0
	for offset := 0; offset < len(encoded); {
		frames := make([]string, 0, uploadWindow)
		for len(frames) < uploadWindow && offset < len(encoded) {
			end := min(offset+chunkSize, len(encoded))
			chunk := encoded[offset:end]
			frames = append(frames, fmt.Sprintf("data:%d:%d:%s\n", sequence, len(chunk), chunk))
			sequence++
			offset = end
		}
		wanted := fmt.Sprintf("%s:%d", bootstrapUploadACK, sequence)
		if err := sendBootstrapWindow(ctx, encoder, output, frames, wanted); err != nil {
			return err
		}
	}
	if err := sendBootstrapWindow(ctx, encoder, output,
		[]string{fmt.Sprintf("done:%d:0:\n", sequence)}, bootstrapUploadDone); err != nil {
		return err
	}
	result, err := readBootstrapResult(ctx, output, bootstrapResultWait)
	if err != nil {
		return err
	}
	if strings.Contains(result, bootstrapOK) {
		return nil
	}
	return fmt.Errorf("coder: worker bootstrap failed: %s", sanitizePTYOutput(result))
}

func waitForBootstrapReady(ctx context.Context, output <-chan ptyOutput) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("%w: workspace PTY did not become ready for worker upload", errTerminalNotReady)
		case response, ok := <-output:
			if !ok {
				return fmt.Errorf("%w: workspace PTY closed before worker upload was ready", errTerminalNotReady)
			}
			if strings.Contains(response.data, bootstrapReady) {
				return nil
			}
			if strings.Contains(response.data, preinstalledMiss) {
				return errPreinstalledWorkerDoesNotMatch
			}
			if _, machine, found := strings.Cut(response.data, architectureMismatch+":"); found {
				return &architectureMismatchError{machine: strings.TrimSpace(machine)}
			}
			if strings.Contains(response.data, bootstrapFailed) {
				return fmt.Errorf("coder: worker bootstrap failed before upload: %s", sanitizePTYOutput(response.data))
			}
			if response.err != nil {
				return fmt.Errorf("%w: read workspace PTY before worker upload: %v", errTerminalNotReady, response.err)
			}
		}
	}
}

// sendBootstrapWindow pipelines a bounded group of canonical PTY lines, then
// waits for the receiver's cumulative acknowledgement. If Coder short-writes a
// frame, replaying the whole window is safe: the shell ignores already-accepted
// sequence numbers, accepts the first missing one, and then continues through
// the replayed suffix. This retains the upload's loss recovery without paying
// one WAN round trip per 3 KiB frame.
func sendBootstrapWindow(
	ctx context.Context,
	encoder *json.Encoder,
	output <-chan ptyOutput,
	lines []string,
	wanted string,
) error {
	const (
		maxAttempts = 12
		ackTimeout  = 2 * time.Second
	)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		for _, line := range lines {
			if err := encoder.Encode(struct {
				Data string `json:"data"`
			}{Data: line}); err != nil {
				return fmt.Errorf("coder: upload worker through PTY: %w", err)
			}
		}
		timer := time.NewTimer(ackTimeout)
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				goto retry
			case response, ok := <-output:
				if !ok {
					timer.Stop()
					if err := ctx.Err(); err != nil {
						return err
					}
					return errors.New("coder: workspace PTY closed during worker upload")
				}
				if strings.TrimSpace(response.data) == wanted {
					timer.Stop()
					return nil
				}
				if strings.Contains(response.data, bootstrapFailed) {
					timer.Stop()
					return fmt.Errorf("coder: worker bootstrap failed during upload: %s", sanitizePTYOutput(response.data))
				}
				if response.err != nil {
					timer.Stop()
					return fmt.Errorf("coder: read workspace PTY upload acknowledgement: %w", response.err)
				}
			}
		}
	retry:
	}
	return errors.New("coder: workspace PTY repeatedly dropped an upload window")
}

func validateBootstrap(bootstrap sandbox.WorkerBootstrap) error {
	if len(bootstrap.Binary) == 0 {
		return errors.New("coder: worker binary is empty")
	}
	if !safeAbsolutePath(bootstrap.Destination) {
		return fmt.Errorf("coder: worker destination %q must be a safe absolute path", bootstrap.Destination)
	}
	if len(bootstrap.HelperBinary) > 0 && !safeAbsolutePath(bootstrap.HelperDestination) {
		return fmt.Errorf("coder: helper destination %q must be a safe absolute path", bootstrap.HelperDestination)
	}
	if !userPattern.MatchString(strings.TrimSpace(bootstrap.User)) {
		return fmt.Errorf("coder: worker user %q is invalid", bootstrap.User)
	}
	if _, err := sandbox.NewCoderWorkspaceLayout(bootstrap.DurableRoot); err != nil {
		return fmt.Errorf("coder: durable workspace root: %w", err)
	}
	identity := strings.TrimSpace(bootstrap.DurableIdentity)
	if identity == "" || len(identity) > 200 || strings.IndexFunc(identity, func(character rune) bool {
		return character < ' ' || character == 0x7f
	}) >= 0 {
		return errors.New("coder: durable workspace identity is invalid")
	}
	for key := range bootstrap.Environment {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
			return fmt.Errorf("coder: environment key %q is invalid", key)
		}
	}
	return nil
}

func safeAbsolutePath(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "/") && path.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func bootstrapArchive(bootstrap sandbox.WorkerBootstrap) ([]byte, error) {
	return buildBootstrapArchive(bootstrap, true)
}

func bootstrapLaunchArchive(bootstrap sandbox.WorkerBootstrap) ([]byte, error) {
	return buildBootstrapArchive(bootstrap, false)
}

func buildBootstrapArchive(bootstrap sandbox.WorkerBootstrap, includeBinaries bool) ([]byte, error) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	files := []struct {
		name string
		mode int64
		data []byte
	}{
		{name: "worker.env", mode: 0o600, data: []byte(environmentFile(bootstrap.Environment))},
		{name: "launch.sh", mode: 0o700, data: []byte("#!/bin/sh\nset -eu\nprintf '%s\\n' \"$$\" >\"$3\"\nset -a\n. \"$1\"\nset +a\nrm -f \"$1\"\nexec \"$2\"\n")},
	}
	if includeBinaries {
		files = append(files, struct {
			name string
			mode int64
			data []byte
		}{name: "ao-worker", mode: 0o700, data: bootstrap.Binary})
	}
	if includeBinaries && len(bootstrap.HelperBinary) > 0 {
		files = append(files, struct {
			name string
			mode int64
			data []byte
		}{name: "ao", mode: 0o700, data: bootstrap.HelperBinary})
	}
	for _, file := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data))}); err != nil {
			return nil, fmt.Errorf("coder: build worker archive: %w", err)
		}
		if _, err := tarWriter.Write(file.data); err != nil {
			return nil, fmt.Errorf("coder: build worker archive: %w", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("coder: close worker archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("coder: compress worker archive: %w", err)
	}
	return compressed.Bytes(), nil
}

func environmentFile(environment map[string]string) string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	for _, key := range keys {
		content.WriteString(key)
		content.WriteByte('=')
		content.WriteString(shellQuote(environment[key]))
		content.WriteByte('\n')
	}
	return content.String()
}

func bootstrapCommand(bootstrap sandbox.WorkerBootstrap, encodedLength int) string {
	return bootstrapCommandForArchive(bootstrap, "", encodedLength, false)
}

// architectureGuardScript rejects a workspace whose uname -m does not match
// the worker build AO selected, before anything is installed. It prints the
// machine name so the caller can retry with the matching build or explain an
// unsupported CPU. An empty arch disables the guard.
func architectureGuardScript(arch string) string {
	if arch == "" {
		return ""
	}
	return "ao_machine=$(uname -m 2>/dev/null || echo unknown)\n" +
		"case \"$ao_machine\" in x86_64|amd64) ao_arch=amd64 ;; aarch64|arm64|armv8|armv8l) ao_arch=arm64 ;; *) ao_arch=unknown ;; esac\n" +
		"if [ \"$ao_arch\" != " + shellQuote(arch) + " ]; then echo " + architectureMismatch + ":\"$ao_machine\"; exit 0; fi\n"
}

func bootstrapCommandForArchive(
	bootstrap sandbox.WorkerBootstrap,
	arch string,
	encodedLength int,
	preinstalled bool,
) string {
	workerUser := strings.TrimSpace(bootstrap.User)
	workerDestination := strings.TrimSpace(bootstrap.Destination)
	layout, _ := sandbox.NewCoderWorkspaceLayout(bootstrap.DurableRoot)
	requireIdentity := "0"
	if bootstrap.RequireDurableIdentity {
		requireIdentity = "1"
	}
	binaryPreparation := "sudo -n install -m 0755 \"$stage/ao-worker\" " + shellQuote(workerDestination) + "\n"
	if len(bootstrap.HelperBinary) > 0 {
		binaryPreparation += "sudo -n install -m 0755 \"$stage/ao\" " + shellQuote(bootstrap.HelperDestination) + "\n"
	}
	preinstalledCheck := ""
	if preinstalled {
		binaryPreparation = ""
		preinstalledCheck = preinstalledHealScript(bootstrap, workerDestination)
	}
	workerEnvironment := path.Join(layout.WorkerData, "worker.env")
	workerLauncher := path.Join(layout.WorkerData, "launch.sh")
	workerLog := path.Join(layout.WorkerData, "worker.log")
	workerPID := path.Join(layout.WorkerData, "worker.pid")
	script := "set -eu\n" + architectureGuardScript(arch) + preinstalledCheck +
		"stage=$(mktemp -d)\nencoded=\"$stage/payload.b64\"\n" +
		"trap 'code=$?; stty echo icanon 2>/dev/null || true; echo " + bootstrapFailed + ":$code' EXIT\n" +
		"target=" + strconv.Itoa(encodedLength) + "\nexpected=0\nreceived=0\n: >\"$encoded\"\nstty -echo icanon 2>/dev/null || true\necho " + bootstrapReady + "\n" +
		"while IFS=: read -r kind sequence declared chunk; do\n" +
		"  case \"$sequence\" in ''|*[!0-9]*) continue ;; esac\n" +
		"  case \"$declared\" in ''|*[!0-9]*) continue ;; esac\n" +
		"  if [ \"$kind\" = data ] && [ \"$sequence\" -eq \"$expected\" ] && [ \"${#chunk}\" -eq \"$declared\" ] && [ $((received + declared)) -le \"$target\" ]; then\n" +
		"    printf %s \"$chunk\" >>\"$encoded\"\n    received=$((received + declared))\n    expected=$((expected + 1))\n    echo " + bootstrapUploadACK + ":$expected\n" +
		"  elif [ \"$kind\" = done ] && [ \"$received\" -eq \"$target\" ]; then\n    echo " + bootstrapUploadDone + "\n    break\n  fi\ndone\nstty echo icanon 2>/dev/null || true\n" +
		"base64 -d \"$encoded\" | gzip -d | tar -xf - -C \"$stage\"\n" +
		"sudo -n id -u " + shellQuote(workerUser) + " >/dev/null 2>&1 || sudo -n useradd -m " + shellQuote(workerUser) + "\n" +
		durableRootScript(layout.DurableRoot, bootstrap.RequireMountedDurableRoot) +
		"sudo -n chmod o+x \"$durable_root\"\n" +
		"sudo -n mkdir -p " + shellQuote(layout.Repository) + " " + shellQuote(layout.WorkerData) + " " +
		shellQuote(layout.Home) + " " + shellQuote(layout.ClaudeConfig) + " " + shellQuote(layout.CodexHome) + "\n" +
		"identity_file=" + shellQuote(layout.DurableIdentity) + "\n" +
		"if sudo -n test -f \"$identity_file\"; then\n" +
		"  existing_identity=$(sudo -n cat \"$identity_file\")\n" +
		"  if [ \"$existing_identity\" != " + shellQuote(strings.TrimSpace(bootstrap.DurableIdentity)) + " ]; then\n" +
		"    echo 'Coder durable root belongs to a different AO session' >&2\n    exit 1\n  fi\n" +
		"elif [ " + requireIdentity + " -eq 1 ]; then\n" +
		"  echo 'Coder durable state did not survive workspace stop/start' >&2\n  exit 1\n" +
		"else\n" +
		"  printf '%s\\n' " + shellQuote(strings.TrimSpace(bootstrap.DurableIdentity)) + " | sudo -n tee \"$identity_file\" >/dev/null\nfi\n" +
		"sudo -n chown -R " + shellQuote(workerUser+":"+workerUser) + " " + shellQuote(layout.Repository) + " " + shellQuote(path.Dir(layout.WorkerData)) + "\n" +
		// The dev-kit clones each extra repository as a sibling of the primary
		// checkout (…/repository -> …/<name>), so the worker user must be able to
		// create new entries directly in the durable root. Coder owns the root as
		// the coder user and leaves it group-unwritable, which is why extra-repo
		// clones failed with "could not create work tree dir: Permission denied".
		// Grant the worker's group only write+traverse (g+wx, deliberately not
		// read): a clone must create and enter <root>/<name>, never list the root.
		// Applied to the root entry itself, non-recursively so Coder's own home
		// entries keep their existing modes, and without transferring ownership so
		// Coder (the owner) keeps full access. This widens the root from
		// traversal-only to group-writable for the worker; see
		// cloud/docs/coder-sandbox-provider.md. Note directory write inherently
		// permits unlink/rename of the root's top-level entries (no sticky bit);
		// isolating the worker to a dedicated sub-root would need a larger change.
		"sudo -n chgrp " + shellQuote(workerUser) + " \"$durable_root\"\n" +
		"sudo -n chmod g+wx \"$durable_root\"\n" +
		binaryPreparation +
		"sudo -n install -o " + shellQuote(workerUser) + " -g " + shellQuote(workerUser) + " -m 0600 \"$stage/worker.env\" " + shellQuote(workerEnvironment) + "\n" +
		"sudo -n install -o " + shellQuote(workerUser) + " -g " + shellQuote(workerUser) + " -m 0700 \"$stage/launch.sh\" " + shellQuote(workerLauncher) + "\n" +
		"sudo -n pkill -u " + shellQuote(workerUser) + " -f " + shellQuote(workerDestination) + " 2>/dev/null || true\n" +
		"sudo -n install -o " + shellQuote(workerUser) + " -g " + shellQuote(workerUser) + " -m 0600 /dev/null " + shellQuote(workerLog) + "\n" +
		"sudo -n install -o " + shellQuote(workerUser) + " -g " + shellQuote(workerUser) + " -m 0600 /dev/null " + shellQuote(workerPID) + "\n" +
		// Start the worker in a new session with no controlling terminal. With
		// sudoers "Defaults use_pty" (Ubuntu's default) sudo gives the command a
		// fresh pty and leaves the worker in a background process group on it, so
		// any descendant that touches /dev/tty (a git credential prompt during the
		// checkpoint push) gets SIGTTIN and stops the whole group: the worker
		// freezes after connecting and never heartbeats again. Under setsid that
		// open fails with ENXIO instead.
		"ao_setsid=\nif command -v setsid >/dev/null 2>&1; then ao_setsid=setsid; fi\n" +
		"sudo -n -b -u " + shellQuote(workerUser) + " $ao_setsid sh -c " + shellQuote("exec nohup "+shellQuote(workerLauncher)+" "+shellQuote(workerEnvironment)+" "+shellQuote(workerDestination)+" "+shellQuote(workerPID)+" >"+shellQuote(workerLog)+" 2>&1 </dev/null") + "\n" +
		"attempt=0\nworker_pid=\nwhile [ \"$attempt\" -lt 5 ]; do\n" +
		"  if sudo -n test -s " + shellQuote(workerPID) + "; then worker_pid=$(sudo -n cat " + shellQuote(workerPID) + "); fi\n" +
		"  case \"$worker_pid\" in ''|*[!0-9]*) ;; *) if sudo -n -u " + shellQuote(workerUser) + " kill -0 \"$worker_pid\" 2>/dev/null; then break; fi ;; esac\n" +
		"  worker_pid=\nattempt=$((attempt + 1))\nsleep 1\ndone\n" +
		"case \"$worker_pid\" in ''|*[!0-9]*) echo 'AO worker did not start' >&2; exit 1 ;; esac\n" +
		"sleep 1\nsudo -n -u " + shellQuote(workerUser) + " kill -0 \"$worker_pid\" 2>/dev/null || { echo 'AO worker exited during startup' >&2; exit 1; }\n" +
		"rm -rf \"$stage\"\ntrap - EXIT\necho " + bootstrapOK + "\n"
	return "sh -lc " + shellQuote(script)
}

// durableRootScript prepares the durable root before anything is written
// beneath it. AO-operated templates mount a dedicated volume there and the check
// stays strict. A bring-your-own template may keep home on the root filesystem:
// the root is created if needed (owned by the workspace user) and the identity
// marker that follows refuses another session's state. A symbolic link is
// always refused so the root cannot be redirected.
func durableRootScript(durableRoot string, requireMount bool) string {
	required := "0"
	if requireMount {
		required = "1"
	}
	return "durable_root=" + shellQuote(durableRoot) + "\n" +
		"if [ -L \"$durable_root\" ]; then\n" +
		"  echo 'configured Coder durable root must not be a symbolic link' >&2\n  exit 1\nfi\n" +
		"if [ " + required + " -eq 1 ]; then\n" +
		"  if [ ! -d \"$durable_root\" ] || ! mountpoint -q \"$durable_root\"; then\n" +
		"    echo 'configured Coder durable root is not a mounted directory' >&2\n    exit 1\n  fi\n" +
		"elif [ ! -d \"$durable_root\" ]; then\n" +
		"  sudo -n mkdir -p \"$durable_root\"\n" +
		"  sudo -n chown \"$(id -u):$(id -g)\" \"$durable_root\"\nfi\n"
}

// preinstalledHealScript emits the shell that runs before a launch-only bootstrap
// upload. For each baked binary it checks whether the copy at its destination
// already matches the exact hash the control plane runs. A stale or missing copy
// first tries a fast HTTP pull of the correct build from the control plane
// (content-addressed and unauthenticated, like /worker/bootstrap), verifies the
// sha256, and installs it in place. Only if that self-heal fails does the
// workspace emit preinstalledMiss and exit 0, so the caller falls back to the
// slow PTY binary upload. AO_CLOUD_PUBLIC_URL is not yet sourced from worker.env
// at this point, so the origin is embedded here as a shell literal.
func preinstalledHealScript(bootstrap sandbox.WorkerBootstrap, workerDestination string) string {
	publicURL := strings.TrimRight(strings.TrimSpace(bootstrap.Environment["AO_CLOUD_PUBLIC_URL"]), "/")
	workerHash := sha256.Sum256(bootstrap.Binary)
	var script strings.Builder
	script.WriteString("ao_public_url=" + shellQuote(publicURL) + "\n")
	// ao_http_heal <dest> <sha256>: pull the content-addressed binary from the
	// control plane, verify its hash, and install it. Any failure returns non-zero
	// so the caller signals a miss and the PTY upload takes over.
	script.WriteString("ao_http_heal() {\n")
	script.WriteString("  ao_dest=$1; ao_want=$2\n")
	script.WriteString("  [ -n \"$ao_public_url\" ] || return 1\n")
	script.WriteString("  ao_tmp=$(mktemp) || return 1\n")
	script.WriteString("  ao_url=\"$ao_public_url/api/cloud/v1/worker/binary/$ao_want\"\n")
	script.WriteString("  if command -v curl >/dev/null 2>&1; then\n")
	script.WriteString("    curl -fsSL \"$ao_url\" -o \"$ao_tmp\" || { rm -f \"$ao_tmp\"; return 1; }\n")
	script.WriteString("  elif command -v wget >/dev/null 2>&1; then\n")
	script.WriteString("    wget -qO \"$ao_tmp\" \"$ao_url\" || { rm -f \"$ao_tmp\"; return 1; }\n")
	script.WriteString("  else\n    rm -f \"$ao_tmp\"; return 1\n  fi\n")
	script.WriteString("  [ \"$(sha256sum \"$ao_tmp\" | cut -d' ' -f1)\" = \"$ao_want\" ] || { rm -f \"$ao_tmp\"; return 1; }\n")
	script.WriteString("  sudo -n install -m 0755 \"$ao_tmp\" \"$ao_dest\" || { rm -f \"$ao_tmp\"; return 1; }\n")
	script.WriteString("  rm -f \"$ao_tmp\"\n")
	script.WriteString("}\n")
	script.WriteString(preinstalledHealBlock(workerDestination, hex.EncodeToString(workerHash[:])))
	if len(bootstrap.HelperBinary) > 0 {
		helperHash := sha256.Sum256(bootstrap.HelperBinary)
		script.WriteString(preinstalledHealBlock(bootstrap.HelperDestination, hex.EncodeToString(helperHash[:])))
	}
	return script.String()
}

// preinstalledHealBlock guards one baked binary: if the copy at dest is missing
// or does not match the expected sha256, attempt the HTTP self-heal, and only on
// its failure emit the miss marker so the caller falls back to the PTY upload.
func preinstalledHealBlock(dest, expectedHex string) string {
	quotedDest := shellQuote(dest)
	return "if ! { [ -x " + quotedDest + " ] && [ \"$(sha256sum " + quotedDest + " | cut -d' ' -f1)\" = " +
		shellQuote(expectedHex) + " ]; }; then\n" +
		"  ao_http_heal " + quotedDest + " " + shellQuote(expectedHex) +
		" || { echo " + preinstalledMiss + "; exit 0; }\nfi\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type ptyOutput struct {
	data string
	err  error
}

func streamPTYOutput(ctx context.Context, reader io.Reader) (<-chan ptyOutput, <-chan struct{}) {
	result := make(chan ptyOutput)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(result)
		buffered := bufio.NewReader(reader)
		total := 0
		for total < maxPTYOutput {
			line, err := buffered.ReadString('\n')
			total += len(line)
			if line != "" || err != nil {
				select {
				case result <- ptyOutput{data: line, err: err}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
		select {
		case result <- ptyOutput{err: errors.New("coder: PTY output exceeded limit")}:
		case <-ctx.Done():
		}
	}()
	return result, done
}

func readBootstrapResult(ctx context.Context, output <-chan ptyOutput, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var result strings.Builder
	for {
		select {
		case <-ctx.Done():
			return result.String(), ctx.Err()
		case <-timer.C:
			return result.String(), errors.New("coder: workspace PTY did not report the worker bootstrap result")
		case value, ok := <-output:
			if !ok {
				if err := ctx.Err(); err != nil {
					return result.String(), err
				}
				return result.String(), io.EOF
			}
			result.WriteString(value.data)
			text := result.String()
			if strings.Contains(text, bootstrapOK) || strings.Contains(text, bootstrapFailed) {
				return text, nil
			}
			if value.err != nil {
				if err := ctx.Err(); err != nil {
					return text, err
				}
				return text, fmt.Errorf("coder: read workspace PTY: %w", value.err)
			}
		}
	}
}

func sanitizePTYOutput(output string) string {
	output = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || character >= ' ' {
			return character
		}
		return -1
	}, output)
	if len(output) > 1024 {
		output = output[len(output)-1024:]
	}
	return strings.TrimSpace(output)
}

func (c *Client) do(ctx context.Context, method, requestPath string, body, output any) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("coder: encode request: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+requestPath, requestBody)
	if err != nil {
		return fmt.Errorf("coder: create request: %w", err)
	}
	request.Header.Set("Coder-Session-Token", c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("coder: %s %s: %w", method, requestPath, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return sandbox.ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return fmt.Errorf("coder: API returned %d: %s", response.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(output); err != nil {
		return fmt.Errorf("coder: decode API response: %w", err)
	}
	return nil
}
