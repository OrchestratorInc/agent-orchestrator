package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const (
	ProviderDocker  = "docker"
	ProviderDaytona = "daytona"
	ProviderECS     = "ecs"
	ProviderNodeOps = "nodeops"
	ProviderCoder   = "coder"

	DefaultProvider       = ProviderDocker
	DefaultWorkerTokenTTL = 15 * time.Minute
)

// ErrCoderTemplateRequired is returned when a Coder session resolves to no
// template at all — the organization set no default template and the project
// chose none. A bring-your-own-Coder org picks its template per project, so this
// is a user-fixable condition the HTTP edge surfaces as "choose a template"
// rather than a deployment misconfiguration.
var ErrCoderTemplateRequired = errors.New("a Coder template must be selected for this session")

type NodeOpsConfig struct {
	BaseURL       string
	APIKey        string
	DefaultShape  string
	DefaultRootFS string
	// RootFSByHarness maps a coding-agent harness (e.g. "claude-code") to a
	// slimmer template that bakes only that agent. A session whose harness has
	// a mapping provisions from it; anything unmapped falls back to
	// DefaultRootFS. Smaller templates shrink the provider's cold-host image
	// pull, which dominates worst-case sandbox creation time.
	RootFSByHarness  map[string]string
	Ingress          string
	SSHKeyPath       string
	WorkerTokenTTL   time.Duration
	AutoPauseSeconds int
}

// rootFSForHarness resolves the template one session provisions from.
func (c NodeOpsConfig) rootFSForHarness(harness string) string {
	if rootFS := strings.TrimSpace(c.RootFSByHarness[strings.TrimSpace(harness)]); rootFS != "" {
		return rootFS
	}
	return strings.TrimSpace(c.DefaultRootFS)
}

type DockerConfig struct {
	Host           string
	WorkerImage    string
	Network        string
	Namespace      string
	WorkerTokenTTL time.Duration
}

type CoderConfig struct {
	BaseURL        string
	Owner          string
	TemplateID     string
	AgentName      string
	Parameters     map[string]string
	DurableRoot    string
	WorkerTokenTTL time.Duration
}

// CoderSessionProfile is the non-secret provider contract stamped onto one
// session. Reconciliation reads these values from the durable sandbox row; a
// later deployment configuration change must not move or recreate that session.
type CoderSessionProfile struct {
	BaseURL     string            `json:"baseUrl"`
	Owner       string            `json:"owner"`
	TemplateID  string            `json:"templateId"`
	AgentName   string            `json:"agentName"`
	Parameters  map[string]string `json:"parameters"`
	DurableRoot string            `json:"durableRoot"`
	// WorkspaceNamePrefix replaces the default "ao" workspace name prefix.
	WorkspaceNamePrefix string `json:"workspaceNamePrefix,omitempty"`
	// RequireMountedDurableRoot is nil on rows written before the field
	// existed; RequiresMountedDurableRoot supplies the historical default.
	RequireMountedDurableRoot *bool `json:"requireMountedDurableRoot,omitempty"`
	// StartupTimeoutSeconds overrides the deployment startup budget for this
	// session's ready wait and startup ceiling. Zero keeps the deployment value.
	StartupTimeoutSeconds int `json:"startupTimeoutSeconds,omitempty"`
}

// RequiresMountedDurableRoot reports whether bootstrap must find a mounted
// volume at the durable root. Profiles written before the flag existed keep
// the strict check for the deployment Coder and drop it for a bring-your-own
// connection, whose templates commonly keep home on the root filesystem.
func (p CoderSessionProfile) RequiresMountedDurableRoot(byoConnection bool) bool {
	if p.RequireMountedDurableRoot != nil {
		return *p.RequireMountedDurableRoot
	}
	return !byoConnection
}

// CoderSessionOptions are the per-session Coder choices a client may make when
// creating a session (template picker + its curated form). All fields are
// optional; an empty TemplateID means "use the deployment default template with
// its default parameters" — i.e. exactly the pre-existing behavior. Size and
// StartupScript are only applied when a non-default template is chosen, because
// the default template does not declare those rich parameters and Coder rejects
// values for parameters a template does not define.
type CoderSessionOptions struct {
	TemplateID    string
	Size          string
	StartupScript string
	// WorkspaceNamePrefix names the session's workspace <prefix>-<id> instead
	// of the default ao-<id>. It applies with or without a picked template.
	WorkspaceNamePrefix string
}

// CoderDeploymentOverride redirects a session's Coder provisioning to a specific
// (per-organization, bring-your-own) Coder deployment in place of the
// deployment-default config (ProvisioningDefaults.Coder). Every field replaces
// its deployment-default counterpart; the worker-token TTL is deployment policy
// and is not overridable here. A nil override keeps the deployment default, so
// existing deployment-level Coder sessions are unchanged.
type CoderDeploymentOverride struct {
	BaseURL     string
	Owner       string
	TemplateID  string
	AgentName   string
	Parameters  map[string]string
	DurableRoot string
	// RequireMountedDurableRoot opts a bring-your-own connection back into the
	// deployment's strict mounted-volume check. Off by default.
	RequireMountedDurableRoot bool
	// StartupTimeoutSeconds is the connection's startup budget. Zero selects
	// DefaultBYOCoderStartupTimeout.
	StartupTimeoutSeconds int
}

// DefaultBYOCoderStartupTimeout is the startup budget for a bring-your-own
// Coder connection that does not set one. Customer templates routinely clone
// several repositories in a blocking startup script before accepting terminals.
const DefaultBYOCoderStartupTimeout = 20 * time.Minute

// CoderHomeDurableRoot resolves the durable root to the workspace user's home
// directory at bootstrap time. A bring-your-own template may not run as the
// "coder" user, so AO cannot know the absolute path in advance.
const CoderHomeDurableRoot = "$HOME"

// NormalizeCoderDurableRoot maps the accepted spellings of the home-directory
// root ("$HOME", "~") onto CoderHomeDurableRoot and trims anything else.
func NormalizeCoderDurableRoot(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case CoderHomeDurableRoot, "~", "${HOME}":
		return CoderHomeDurableRoot
	}
	return value
}

// CoderWorkspaceLayout is the provider-specific filesystem contract between AO
// and a Coder template. DurableRoot must be the template's persistent volume
// mount point; every path AO must retain across stop/start is derived beneath it.
type CoderWorkspaceLayout struct {
	DurableRoot     string
	Repository      string
	WorkerData      string
	Home            string
	ClaudeConfig    string
	CodexHome       string
	DurableIdentity string
}

// NewCoderWorkspaceLayout validates and expands the configured Coder volume
// mount. Bootstrap separately verifies that DurableRoot is an actual mount point
// inside the workspace before AO writes anything beneath it.
//
// CoderHomeDurableRoot is accepted as a symbolic root: every derived path keeps
// the "$HOME/" prefix and the provider substitutes the workspace user's real
// home directory before it writes anything.
func NewCoderWorkspaceLayout(durableRoot string) (CoderWorkspaceLayout, error) {
	durableRoot = NormalizeCoderDurableRoot(durableRoot)
	if durableRoot == "" {
		return CoderWorkspaceLayout{}, errors.New("AO_CLOUD_CODER_DURABLE_ROOT is required")
	}
	if durableRoot != CoderHomeDurableRoot && !SafeCoderDurableRoot(durableRoot) {
		return CoderWorkspaceLayout{}, errors.New(
			"AO_CLOUD_CODER_DURABLE_ROOT must be a safe absolute non-root path",
		)
	}
	aoRoot := path.Join(durableRoot, ".ao")
	home := path.Join(aoRoot, "home")
	return CoderWorkspaceLayout{
		DurableRoot:     durableRoot,
		Repository:      path.Join(durableRoot, "repository"),
		WorkerData:      path.Join(aoRoot, "worker"),
		Home:            home,
		ClaudeConfig:    path.Join(home, ".claude"),
		CodexHome:       path.Join(home, ".codex"),
		DurableIdentity: path.Join(aoRoot, "durable-session-id"),
	}, nil
}

// SafeCoderDurableRoot reports whether an absolute durable root is safe to
// splice into the bootstrap shell and derive workspace paths from.
func SafeCoderDurableRoot(durableRoot string) bool {
	return len(durableRoot) <= 1024 && strings.HasPrefix(durableRoot, "/") &&
		path.Clean(durableRoot) == durableRoot && durableRoot != "/" &&
		strings.IndexFunc(durableRoot, func(character rune) bool {
			return character < ' ' || character == 0x7f
		}) < 0
}

// DecodeCoderSessionProfile reads and validates the Coder contract stored in a
// sandbox resource profile. Connection credentials deliberately remain outside
// this profile and come from the configured provider connection.
func DecodeCoderSessionProfile(raw json.RawMessage) (CoderSessionProfile, error) {
	var resource struct {
		Coder *CoderSessionProfile `json:"coder"`
	}
	if len(raw) == 0 {
		return CoderSessionProfile{}, errors.New("Coder session resource profile is required")
	}
	if err := json.Unmarshal(raw, &resource); err != nil {
		return CoderSessionProfile{}, fmt.Errorf("decode Coder session resource profile: %w", err)
	}
	if resource.Coder == nil {
		return CoderSessionProfile{}, errors.New("Coder session resource profile is required")
	}
	profile := *resource.Coder
	baseURL, err := normalizedCoderBaseURL(profile.BaseURL)
	if err != nil {
		return CoderSessionProfile{}, err
	}
	profile.BaseURL = baseURL
	profile.Owner = strings.TrimSpace(profile.Owner)
	profile.TemplateID = strings.TrimSpace(profile.TemplateID)
	profile.AgentName = strings.TrimSpace(profile.AgentName)
	if profile.Owner == "" {
		return CoderSessionProfile{}, errors.New("Coder session owner is required")
	}
	if profile.TemplateID == "" {
		return CoderSessionProfile{}, errors.New("Coder session template ID is required")
	}
	parameters, err := normalizedCoderParameters(profile.Parameters)
	if err != nil {
		return CoderSessionProfile{}, err
	}
	profile.Parameters = parameters
	layout, err := NewCoderWorkspaceLayout(profile.DurableRoot)
	if err != nil {
		return CoderSessionProfile{}, err
	}
	profile.DurableRoot = layout.DurableRoot
	profile.WorkspaceNamePrefix = strings.TrimSpace(profile.WorkspaceNamePrefix)
	if err := domain.ValidateCoderWorkspaceNamePrefix(profile.WorkspaceNamePrefix); err != nil {
		return CoderSessionProfile{}, err
	}
	if profile.StartupTimeoutSeconds < 0 {
		return CoderSessionProfile{}, errors.New("Coder session startup timeout must not be negative")
	}
	return profile, nil
}

func normalizedCoderBaseURL(value string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(value))
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", errors.New("Coder session base URL must be an absolute http or https origin")
	}
	return strings.TrimRight(endpoint.String(), "/"), nil
}

func normalizedCoderParameters(parameters map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(parameters))
	for name, value := range parameters {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("Coder template parameter name must not be empty")
		}
		if _, exists := normalized[name]; exists {
			return nil, fmt.Errorf("Coder template parameter %q is duplicated", name)
		}
		normalized[name] = value
	}
	return normalized, nil
}

func (c CoderConfig) Validate() error {
	endpoint, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("AO_CLOUD_CODER_URL must be an absolute http or https origin")
	}
	if strings.TrimSpace(c.Owner) == "" {
		return errors.New("AO_CLOUD_CODER_OWNER is required")
	}
	if strings.TrimSpace(c.TemplateID) == "" {
		return errors.New("AO_CLOUD_CODER_TEMPLATE_ID is required")
	}
	if _, err := NewCoderWorkspaceLayout(c.DurableRoot); err != nil {
		return err
	}
	if _, err := normalizedCoderParameters(c.Parameters); err != nil {
		return err
	}
	if c.WorkerTokenTTL <= 0 {
		return errors.New("AO_CLOUD_CODER_WORKER_TOKEN_TTL must be positive")
	}
	return nil
}

func (c DockerConfig) Validate() error {
	if !strings.HasPrefix(strings.TrimSpace(c.Host), "unix:///") {
		return errors.New("AO_CLOUD_DOCKER_HOST must be an absolute unix:// path")
	}
	if strings.TrimSpace(c.WorkerImage) == "" {
		return errors.New("AO_CLOUD_DOCKER_WORKER_IMAGE is required")
	}
	if strings.TrimSpace(c.Namespace) == "" {
		return errors.New("AO_CLOUD_DOCKER_NAMESPACE is required")
	}
	if c.WorkerTokenTTL <= 0 {
		return errors.New("AO_CLOUD_DOCKER_WORKER_TOKEN_TTL must be positive")
	}
	return nil
}

func (c NodeOpsConfig) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("AO_CLOUD_NODEOPS_BASE_URL is required")
	}
	if _, err := url.ParseRequestURI(c.BaseURL); err != nil {
		return fmt.Errorf("AO_CLOUD_NODEOPS_BASE_URL must be a valid URL: %w", err)
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("AO_CLOUD_NODEOPS_API_KEY is required")
	}
	if strings.TrimSpace(c.DefaultShape) == "" {
		return errors.New("AO_CLOUD_NODEOPS_DEFAULT_SHAPE is required")
	}
	if strings.TrimSpace(c.DefaultRootFS) == "" {
		return errors.New("AO_CLOUD_NODEOPS_DEFAULT_ROOTFS is required")
	}
	if c.WorkerTokenTTL <= 0 {
		return errors.New("AO_CLOUD_NODEOPS_WORKER_TOKEN_TTL must be positive")
	}
	if c.AutoPauseSeconds < 0 {
		return errors.New("AO_CLOUD_NODEOPS_AUTO_PAUSE_SECONDS must not be negative")
	}
	return nil
}

type ProvisioningDefaults struct {
	Provider string
	Release  string
	NodeOps  NodeOpsConfig
	Docker   DockerConfig
	Coder    CoderConfig
}

type Plan struct {
	Provider         string
	ResourceProfile  json.RawMessage
	BootstrapContext json.RawMessage
}

// SessionPlan stamps the provisioning plan one session's sandbox runs from.
// The harness selects the rootfs template when a per-harness mapping exists
// (see NodeOpsConfig.RootFSByHarness); the plan is stored on the sandbox row,
// so the choice sticks for the session's whole life, including recreates.
func (d ProvisioningDefaults) SessionPlan(harness string) (Plan, error) {
	return d.SessionPlanForProvider(harness, d.Provider)
}

// SessionPlanForProvider is SessionPlan with an explicit provider override, used
// when a client selects a provider for a session on a control plane configured
// with more than one. An empty override falls back to the deployment default.
// The caller is responsible for confirming the provider is one the control
// plane offers before calling; this method only builds the plan.
func (d ProvisioningDefaults) SessionPlanForProvider(harness, providerOverride string) (Plan, error) {
	return d.SessionPlanForProviderWithCoder(harness, providerOverride, nil, nil)
}

// SessionPlanForProviderWithCoder is SessionPlanForProvider with optional
// per-session Coder options (chosen template + its size/startup form) and an
// optional per-organization Coder deployment override. A nil coder argument, or
// an empty TemplateID, yields exactly the default-template plan; a nil override
// keeps the deployment-default Coder connection — so existing callers, the
// "Default" picker choice, and deployment-level Coder are all unchanged.
func (d ProvisioningDefaults) SessionPlanForProviderWithCoder(harness, providerOverride string, coder *CoderSessionOptions, override *CoderDeploymentOverride) (Plan, error) {
	provider := normalizeProvider(providerOverride)
	if provider == "" {
		provider = normalizeProvider(d.Provider)
	}
	if provider == "" {
		provider = DefaultProvider
	}
	release := strings.TrimSpace(d.Release)
	if release == "" {
		release = "dev"
	}
	resourceProfile := map[string]any{
		"provider": provider,
		"release":  release,
	}
	bootstrapContext := map[string]any{
		"provider": provider,
		"release":  release,
	}
	if provider == ProviderNodeOps {
		if err := d.NodeOps.Validate(); err != nil {
			return Plan{}, err
		}
		rootFS := d.NodeOps.rootFSForHarness(harness)
		resourceProfile["nodeOps"] = map[string]any{
			"baseUrl":               strings.TrimSpace(d.NodeOps.BaseURL),
			"defaultShape":          strings.TrimSpace(d.NodeOps.DefaultShape),
			"defaultRootFs":         rootFS,
			"ingress":               strings.TrimSpace(d.NodeOps.Ingress),
			"sshKeyPath":            strings.TrimSpace(d.NodeOps.SSHKeyPath),
			"workerTokenTtlSeconds": int64(d.NodeOps.WorkerTokenTTL / time.Second),
			"autoPauseSeconds":      d.NodeOps.AutoPauseSeconds,
		}
		bootstrapContext["nodeOps"] = map[string]any{
			"baseUrl":               strings.TrimSpace(d.NodeOps.BaseURL),
			"defaultShape":          strings.TrimSpace(d.NodeOps.DefaultShape),
			"defaultRootFs":         rootFS,
			"ingress":               strings.TrimSpace(d.NodeOps.Ingress),
			"sshKeyPath":            strings.TrimSpace(d.NodeOps.SSHKeyPath),
			"workerTokenTtlSeconds": int64(d.NodeOps.WorkerTokenTTL / time.Second),
			"autoPauseSeconds":      d.NodeOps.AutoPauseSeconds,
		}
	} else if provider == ProviderDocker {
		if err := d.Docker.Validate(); err != nil {
			return Plan{}, err
		}
		resourceProfile["docker"] = map[string]any{
			"workerImage":           strings.TrimSpace(d.Docker.WorkerImage),
			"network":               strings.TrimSpace(d.Docker.Network),
			"namespace":             strings.TrimSpace(d.Docker.Namespace),
			"workerTokenTtlSeconds": int64(d.Docker.WorkerTokenTTL / time.Second),
		}
		bootstrapContext["docker"] = map[string]any{
			"workerImage": strings.TrimSpace(d.Docker.WorkerImage),
			"network":     strings.TrimSpace(d.Docker.Network),
			"namespace":   strings.TrimSpace(d.Docker.Namespace),
		}
	} else if provider == ProviderCoder {
		// A per-organization override replaces the deployment-default connection's
		// non-secret fields (URL/owner/template/agent/params/durable root). The
		// worker-token TTL stays deployment policy; a bring-your-own-only
		// deployment may leave the deployment default unset, so borrow a sane TTL
		// when the override supplies one and the default did not.
		coderCfg := d.Coder
		// The deployment Coder is AO-operated: its templates mount a dedicated
		// volume at the durable root and boot inside the deployment budget. A
		// bring-your-own connection relaxes the mount check (unless it opts back
		// in) and carries its own, longer startup budget.
		requireMountedDurableRoot := true
		startupTimeoutSeconds := 0
		if override != nil {
			coderCfg.BaseURL = override.BaseURL
			coderCfg.Owner = override.Owner
			coderCfg.TemplateID = override.TemplateID
			coderCfg.AgentName = override.AgentName
			coderCfg.Parameters = override.Parameters
			coderCfg.DurableRoot = override.DurableRoot
			if coderCfg.WorkerTokenTTL <= 0 {
				coderCfg.WorkerTokenTTL = DefaultWorkerTokenTTL
			}
			requireMountedDurableRoot = override.RequireMountedDurableRoot
			startupTimeoutSeconds = override.StartupTimeoutSeconds
			if startupTimeoutSeconds <= 0 {
				startupTimeoutSeconds = int(DefaultBYOCoderStartupTimeout / time.Second)
			}
		}
		workspaceNamePrefix := ""
		if coder != nil {
			workspaceNamePrefix = strings.TrimSpace(coder.WorkspaceNamePrefix)
		}
		if err := domain.ValidateCoderWorkspaceNamePrefix(workspaceNamePrefix); err != nil {
			return Plan{}, err
		}
		// Resolve the effective template first: a per-project pick wins, otherwise
		// the org/deployment default. A bring-your-own-Coder org leaves its default
		// empty and chooses the template per project, so a session with neither is a
		// user error surfaced as ErrCoderTemplateRequired — not a deployment misconfig
		// buried deep in reconcile. The resolved value is folded back into the config
		// so the shared Validate (which requires a template) sees it.
		pickedTemplate := coder != nil && strings.TrimSpace(coder.TemplateID) != ""
		templateID := strings.TrimSpace(coderCfg.TemplateID)
		if pickedTemplate {
			templateID = strings.TrimSpace(coder.TemplateID)
		}
		if templateID == "" {
			return Plan{}, ErrCoderTemplateRequired
		}
		coderCfg.TemplateID = templateID
		if err := coderCfg.Validate(); err != nil {
			return Plan{}, err
		}
		parameters, err := normalizedCoderParameters(coderCfg.Parameters)
		if err != nil {
			return Plan{}, err
		}
		// Layer on the picked template's size/startup form values only when the
		// client explicitly picked a non-default template — the default template
		// does not declare those rich parameters, so sending them would make Coder
		// reject the build.
		if pickedTemplate {
			if size := strings.TrimSpace(coder.Size); size != "" {
				parameters["size"] = size
			}
			if startup := coder.StartupScript; strings.TrimSpace(startup) != "" {
				parameters["startup_script"] = startup
			}
		}
		durableRoot := NormalizeCoderDurableRoot(coderCfg.DurableRoot)
		coderProfile := map[string]any{
			"baseUrl":                   strings.TrimRight(strings.TrimSpace(coderCfg.BaseURL), "/"),
			"owner":                     strings.TrimSpace(coderCfg.Owner),
			"templateId":                templateID,
			"agentName":                 strings.TrimSpace(coderCfg.AgentName),
			"parameters":                parameters,
			"durableRoot":               durableRoot,
			"workerTokenTtlSeconds":     int64(coderCfg.WorkerTokenTTL / time.Second),
			"requireMountedDurableRoot": requireMountedDurableRoot,
		}
		if workspaceNamePrefix != "" {
			coderProfile["workspaceNamePrefix"] = workspaceNamePrefix
		}
		if startupTimeoutSeconds > 0 {
			coderProfile["startupTimeoutSeconds"] = startupTimeoutSeconds
		}
		resourceProfile["coder"] = coderProfile
		bootstrapContext["coder"] = map[string]any{
			"owner":       strings.TrimSpace(coderCfg.Owner),
			"templateId":  templateID,
			"agentName":   strings.TrimSpace(coderCfg.AgentName),
			"durableRoot": durableRoot,
		}
	}
	resourceJSON, err := json.Marshal(resourceProfile)
	if err != nil {
		return Plan{}, err
	}
	bootstrapJSON, err := json.Marshal(bootstrapContext)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Provider:         provider,
		ResourceProfile:  resourceJSON,
		BootstrapContext: bootstrapJSON,
	}, nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
