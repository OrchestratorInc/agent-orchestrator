package domain

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type Principal struct {
	UserID        string
	Provider      string
	ExternalID    string
	Email         string
	DisplayName   string
	ExternalOrgID string
	OrgName       string
	OrgRole       string
	// OrgCapabilities are entitlement flags for the active organization, seeded
	// from WorkOS organization metadata (metadata.capabilities). They gate
	// optional features such as the coder sandbox provider. Empty for personal or
	// local organizations.
	OrgCapabilities []string
	// OrgBilling is the active organization's billing arrangement from WorkOS
	// organization metadata (metadata.billing), normalized by
	// NormalizeOrgBilling. Empty when the metadata does not set it.
	OrgBilling string
	// TeamOrgID is the AO organization ID the token's WorkOS organization maps
	// to, set when the principal is resolved. OrgCapabilities and OrgBilling
	// describe only that organization: a request that targets any other
	// organization (such as the user's personal one) uses the deployment
	// defaults instead. Empty without a WorkOS organization.
	TeamOrgID string
}

type Membership struct {
	OrgID       string
	OrgSlug     string
	DisplayName string
	Role        string
}

type LocalRegistration struct {
	Email        string
	DisplayName  string
	PasswordHash string
	OrgSlug      string
	OrgName      string
}

type Project struct {
	ID                 string
	OrgID              string
	DisplayName        string
	RepositoryURL      string
	DefaultBranch      string
	GitHubRepositoryID *int64
	Config             json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreateProject struct {
	DisplayName   string
	RepositoryURL string
	DefaultBranch string
	Config        json.RawMessage
}

type UpdateProject struct {
	DisplayName   string
	DefaultBranch string
}

type Session struct {
	ID        string
	OrgID     string
	ProjectID string
	Kind      string
	Harness   string
	// ReviewerHarness overrides Harness only for manually-triggered PR review
	// terminals. An empty value deliberately follows the worker harness.
	ReviewerHarness    string
	AutoReviewEnabled  bool
	AutoInjectCI       bool
	AutoInjectReview   bool
	TerminateOnPRMerge bool
	DisplayName        string
	Branch             string
	Mode               string
	Model              string
	DeniedCommands     []string
	Interface          SessionInterface
	ActivityState      contract.ActivityState
	IsTerminated       bool
	RuntimeConnected   bool
	WorkerLastSeenAt   *time.Time
	StartupAttempts    int
	SandboxProvider    string
	DesiredState       string
	ObservedState      string
	RuntimeState       string
	RuntimeError       string
	// StartupErrorCode, StartupErrorMessage and StartupErrorAt are the latest
	// user-facing reason the session's sandbox has not started. Empty once the
	// worker checks in.
	StartupErrorCode    string
	StartupErrorMessage string
	StartupErrorAt      *time.Time
	// StartupErrorDetail is the redacted operator detail behind the startup
	// error (failed stage, provider error, Coder build and agent log tails). It
	// is filled only for bring-your-own compute, whose operators are the
	// organization's own.
	StartupErrorDetail string
	// BYOCoderWorkspace locates the session's workspace on the organization's
	// own (bring-your-own) Coder deployment, as AO recorded it in the sandbox
	// resource profile when it planned the workspace, so it is known from the
	// session's creation (the workspace may not exist yet). Nil for AO's managed
	// Coder (an internal deployment customers have no login on) and for every
	// other provider.
	BYOCoderWorkspace *CoderWorkspaceRef
	// WorkerEpoch is the highest worker epoch the session has minted for its
	// agent terminal. It advances every time a fresh worker connects (a resume
	// from idle-pause, a restore, or any re-provision), so a client can key its
	// terminal on it and re-attach to the live agent instead of clinging to the
	// dead epoch's exited terminal. 0 when no worker has ever connected.
	WorkerEpoch int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CoderWorkspaceRef is the non-secret identity of one session's Coder
// workspace: the deployment origin, the owning Coder user, the project's
// workspace name prefix (empty for the default ao-<id> name) and the
// workspace name AO stored when it created the session (empty for sessions
// created before names were stored, whose name is computed from the prefix).
type CoderWorkspaceRef struct {
	BaseURL    string
	Owner      string
	NamePrefix string
	Name       string
}

// Status derives the session's display status from runtime and pull request facts.
func (s Session) Status(now time.Time, prs []contract.PRFacts) contract.SessionStatus {
	starting := s.DesiredState == "running" && !s.RuntimeConnected && s.WorkerLastSeenAt == nil && s.StartupAttempts == 0 && (s.ObservedState == "requested" || s.ObservedState == "provisioning" ||
		s.ObservedState == "bootstrapping" || s.ObservedState == "restoring" ||
		s.ObservedState == "ready" || s.ObservedState == "running")
	lastSignalAt := s.UpdatedAt
	if s.WorkerLastSeenAt != nil {
		lastSignalAt = *s.WorkerLastSeenAt
	}
	return contract.DeriveStatus(contract.SessionFacts{
		Activity:       s.ActivityState,
		LastActivityAt: lastSignalAt,
		HasSignal:      s.RuntimeConnected,
		SignalExpected: s.DesiredState == "running" && !starting &&
			(s.WorkerLastSeenAt != nil || s.StartupAttempts > 0 || s.ObservedState == "failed"),
		IsTerminated: s.IsTerminated,
	}, prs, now, 2*time.Minute)
}

type CreateSession struct {
	ProjectID       string
	Kind            string
	Harness         string
	DisplayName     string
	Prompt          string
	Mode            string
	Model           string
	ReasoningEffort string
	DeniedCommands  []string
	// Interface is the interface the caller explicitly requested; empty
	// resolves through StartInterface at create.
	Interface SessionInterface
	// DefaultInterface is the user's default session interface the session was
	// created with. It resolves this session's interface (StartInterface) and
	// is stored with the session's agent config (agent_config.defaultInterface)
	// for the workers an orchestrator spawns, which resolve theirs from it as
	// local spawns resolve from the daemon's default. Omitted when empty so the
	// idempotency payload of a create without it is unchanged.
	DefaultInterface SessionInterface `json:",omitempty"`
	// Branch is the session's git branch. Empty means a generated
	// <prefix>/<short id>; a named branch that already exists on the remote is
	// continued from its commits. Omitted when empty so the idempotency payload
	// of every other create is unchanged.
	Branch   string `json:",omitempty"`
	Provider string
	// SandboxConnectionID names a bring-your-own provider credential. It is
	// empty for sandboxes that run on the platform's own account.
	SandboxConnectionID string
	// ResourceProfile and BootstrapContext are the provisioning plan the
	// sandbox row is created with. They are stamped at intent time so a later
	// configuration change cannot disturb an in-flight session.
	ResourceProfile  json.RawMessage
	BootstrapContext json.RawMessage
	Release          string
	// ParentSessionID links a top-level worker to a project's active
	// orchestrator so the orchestrator sees, drives, and receives reports from
	// it exactly as it would a worker it spawned itself. Empty for an
	// orchestrator, a standalone worker, or a worker created for a project that
	// has no active orchestrator.
	ParentSessionID string
	// CoderCapability: the caller's organization holds the coder capability
	// (WorkOS metadata capabilities=coder), so its Coder sessions are not
	// billed. Set by the HTTP layer, which owns the capability rule; a worker
	// an orchestrator spawns inherits its parent's billing instead. It is read
	// from a cache, so it stays out of the create's idempotency payload: a
	// retry after the cache changed still replays the first create.
	CoderCapability bool `json:"-"`
}

// RepoRef is one additional repository (beyond the project's primary repo) to
// clone into a session's workspace, optionally at a specific branch. Configured
// on the project (its Config carries the coder dev-kit config) and inherited by
// every session of that project.
type RepoRef struct {
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
}

// ProjectCoderConfig is the coder dev-kit configuration chosen when the project
// is set up. It is stored under the project's Config as {"coder": {...}} and
// inherited by every coder session of the project. An absent/empty config keeps
// the deployment default template and single-repo behavior.
type ProjectCoderConfig struct {
	TemplateID    string    `json:"templateId,omitempty"`
	Size          string    `json:"size,omitempty"`
	StartupScript string    `json:"startupScript,omitempty"`
	ExtraRepos    []RepoRef `json:"extraRepos,omitempty"`
	// WorkspaceNamePrefix starts new Coder workspace names
	// (<prefix>-o-<id> or <prefix>-w-<word>-<id>, see CoderWorkspaceName).
	// Empty uses "ao".
	WorkspaceNamePrefix string `json:"workspaceNamePrefix,omitempty"`
	// Parameters are the project's values for the chosen template's
	// coder_parameter inputs, keyed by parameter name. Values are strings as
	// Coder takes them: a list(string) value is a JSON array string and a bool
	// is "true" or "false". At workspace create they override the
	// organization connection's parameters and are overridden by AO's own
	// size/startup mapping. See ValidateCoderTemplateParameterValues.
	Parameters map[string]string `json:"parameters,omitempty"`
}

// MaxCoderWorkspaceNamePrefix bounds the prefix so <prefix>-<id> stays inside
// Coder's 32-character workspace name limit with a collision-safe id.
const MaxCoderWorkspaceNamePrefix = 20

var coderWorkspaceNamePrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// ValidateCoderWorkspaceNamePrefix accepts an empty prefix (the default) or a
// lowercase name Coder will accept once "-<id>" is appended: it must start
// with a letter, and may not end with or repeat a hyphen.
func ValidateCoderWorkspaceNamePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if !coderWorkspaceNamePrefixPattern.MatchString(prefix) ||
		strings.HasSuffix(prefix, "-") || strings.Contains(prefix, "--") {
		return errors.New("Coder workspace name prefix must be 1-20 lowercase letters, digits, or single hyphens, starting with a letter")
	}
	return nil
}

// CoderWorkspaceNamePrefixFor derives a workspace name prefix from a project
// name: lower-cased, every run of other characters a single hyphen, starting
// with a letter, at most MaxCoderWorkspaceNamePrefix characters, so it
// always passes ValidateCoderWorkspaceNamePrefix. A name with no letters gives
// "ao", the default prefix.
func CoderWorkspaceNamePrefixFor(projectName string) string {
	var slug strings.Builder
	hyphen := false
	for _, character := range strings.ToLower(projectName) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9' && slug.Len() > 0:
			if hyphen && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			hyphen = false
			slug.WriteRune(character)
		default:
			hyphen = true
		}
		if slug.Len() >= MaxCoderWorkspaceNamePrefix {
			break
		}
	}
	prefix := strings.TrimRight(slug.String(), "-")
	if len(prefix) > MaxCoderWorkspaceNamePrefix {
		prefix = strings.TrimRight(prefix[:MaxCoderWorkspaceNamePrefix], "-")
	}
	if prefix == "" || ValidateCoderWorkspaceNamePrefix(prefix) != nil {
		return "ao"
	}
	return prefix
}

// DecodeProjectCoderConfig extracts the coder dev-kit config from a project's
// Config json. ok is false when the project has no coder config.
func DecodeProjectCoderConfig(config json.RawMessage) (cfg ProjectCoderConfig, ok bool) {
	if len(config) == 0 {
		return ProjectCoderConfig{}, false
	}
	var envelope struct {
		Coder *ProjectCoderConfig `json:"coder"`
	}
	if err := json.Unmarshal(config, &envelope); err != nil || envelope.Coder == nil {
		return ProjectCoderConfig{}, false
	}
	return *envelope.Coder, true
}

// RepositorySandboxSnapshot is a prepared VM snapshot for one repository and
// harness: the harness snapshot (BaseSnapshotID) with the repository cloned.
// It is shared by every project in the organization on that repository and
// outlives them. RepositoryIdentity is the GitHub owner/name it was cloned
// from, which a session's repository must still match to use it.
type RepositorySandboxSnapshot struct {
	OrgID              string
	RepositoryKey      string
	RepositoryIdentity string
	Provider           string
	Harness            string
	SnapshotID         string
	BaseSnapshotID     string
	CreatedAt          time.Time
}

// DecodeProjectSandboxProvider returns the sandbox provider a project was set up
// to run its sessions on, stored in its Config as {"sandboxProvider": "..."}.
// It is empty when the project leaves the choice to the session.
func DecodeProjectSandboxProvider(config json.RawMessage) string {
	if len(config) == 0 {
		return ""
	}
	var envelope struct {
		SandboxProvider string `json:"sandboxProvider"`
	}
	if err := json.Unmarshal(config, &envelope); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(envelope.SandboxProvider))
}

// MergeProjectCoderConfig stores the coder dev-kit config under the "coder" key
// of a project's Config, preserving any other keys the config already carries.
func MergeProjectCoderConfig(config json.RawMessage, coder ProjectCoderConfig) (json.RawMessage, error) {
	merged := map[string]json.RawMessage{}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &merged); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(coder)
	if err != nil {
		return nil, err
	}
	merged["coder"] = encoded
	return json.Marshal(merged)
}

// DefaultOrgCoderDurableRoot is the durable root assumed when an
// organization's Coder config omits one: the workspace user's home directory,
// resolved inside the workspace at bootstrap. A bring-your-own template may run
// as any user, so a fixed /home/coder would point at a directory that does not
// exist there. (Mirrors sandbox.CoderHomeDurableRoot; domain cannot import it.)
const DefaultOrgCoderDurableRoot = "$HOME"

// OrgCoderConfig is one organization's non-secret bring-your-own Coder
// connection contract. It is stored as the config JSONB of the org's
// ao_provider_connections row (provider="coder", label="default"); the API
// token is deliberately NOT part of this struct — it is that row's
// encrypted_secret. An org with this config points its coder sessions at its own
// Coder deployment (URL/owner/template) instead of the deployment default.
// Modeled on ProjectCoderConfig.
type OrgCoderConfig struct {
	BaseURL     string            `json:"baseUrl"`
	Owner       string            `json:"owner"`
	TemplateID  string            `json:"templateId"`
	AgentName   string            `json:"agentName,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	DurableRoot string            `json:"durableRoot,omitempty"`
	// EndpointServiceName and Region are optional PrivateLink coordinates for a
	// Coder that lives in a private VPC. They are non-secret config AO ops reads to
	// provision the VPC endpoint; the control plane does NOT use them at connection
	// time. Both stay blank for a directly reachable Coder.
	EndpointServiceName string `json:"endpointServiceName,omitempty"`
	Region              string `json:"region,omitempty"`
	// RequireMountedDurableRoot keeps the deployment Coder's strict check that
	// the durable root is a mounted volume. Off by default: bring-your-own
	// templates commonly keep home on the root filesystem.
	RequireMountedDurableRoot bool `json:"requireMountedDurableRoot,omitempty"`
	// StartupTimeoutSeconds is how long AO waits for a workspace from this
	// connection to become ready and its worker to check in before giving up.
	// Zero uses the bring-your-own default (20 minutes).
	StartupTimeoutSeconds int `json:"startupTimeoutSeconds,omitempty"`
	// WorkerUser selects which account runs AO's worker and coding agent in
	// the workspace: CoderWorkerUserAOWorker (a dedicated ao-worker user, the
	// historical behavior) or CoderWorkerUserWorkspace (the workspace's own
	// login user, with its real HOME, groups and environment). Normalized to
	// CoderWorkerUserAOWorker when unset.
	WorkerUser string `json:"workerUser,omitempty"`
	// ReadyProbe is an optional shell command AO runs as the workspace user
	// after the Coder agent reports ready and before it launches the worker.
	// AO polls it until it exits 0 or ReadyProbeTimeoutSeconds elapses, for
	// templates whose own setup outlives the Coder agent's startup script.
	ReadyProbe string `json:"readyProbe,omitempty"`
	// ReadyProbeTimeoutSeconds bounds the ReadyProbe wait. Normalized to
	// DefaultCoderReadyProbeTimeoutSeconds when a probe is set without one.
	ReadyProbeTimeoutSeconds int `json:"readyProbeTimeoutSeconds,omitempty"`
	// ReuseExistingCheckout makes the worker use a checkout of the project's
	// repository the template already cloned (under $HOME/work/* or $HOME/*)
	// as the session worktree instead of cloning into ~/repository. It only
	// applies with the workspace worker user, where it defaults to true;
	// normalization pins it to false for the ao-worker user.
	ReuseExistingCheckout *bool `json:"reuseExistingCheckout,omitempty"`
	// WorkingDirectory names the directory a session works in: an absolute
	// path, or one under the workspace user's home ("~/" or "$HOME/"), which
	// may contain {repo} for the repository name, for example ~/work/{repo}.
	// An existing checkout of the session repository there is adopted, a
	// missing directory gets a fresh clone, and anything else fails the
	// checkout. It replaces the ReuseExistingCheckout search and requires the
	// workspace worker user.
	WorkingDirectory string `json:"workingDirectory,omitempty"`
	// ProtectSecretFiles stops the coding agent's own file tools from reading
	// or editing secret environment files (.env, .env.*). It defaults to true;
	// normalization makes the effective value explicit.
	ProtectSecretFiles *bool `json:"protectSecretFiles,omitempty"`
}

// Bounds for OrgCoderConfig.StartupTimeoutSeconds.
const (
	MinOrgCoderStartupTimeoutSeconds = 60
	MaxOrgCoderStartupTimeoutSeconds = 2 * 60 * 60
)

// normalize trims the config's string fields and fills the durable-root default
// so every encode and decode yields the same canonical, provisioning-ready
// value regardless of what the caller supplied.
func (c *OrgCoderConfig) normalize() {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.Owner = strings.TrimSpace(c.Owner)
	c.TemplateID = strings.TrimSpace(c.TemplateID)
	c.AgentName = strings.TrimSpace(c.AgentName)
	c.DurableRoot = strings.TrimSpace(c.DurableRoot)
	if c.DurableRoot == "~" || c.DurableRoot == "${HOME}" {
		c.DurableRoot = DefaultOrgCoderDurableRoot
	}
	c.EndpointServiceName = strings.TrimSpace(c.EndpointServiceName)
	c.Region = strings.TrimSpace(c.Region)
	if c.DurableRoot == "" {
		c.DurableRoot = DefaultOrgCoderDurableRoot
	}
	if len(c.Parameters) == 0 {
		c.Parameters = nil
	}
	c.normalizeDevEnvironment()
}

// DecodeOrgCoderConfig reads an organization's Coder config from the config
// JSONB of its provider connection. Unlike DecodeProjectCoderConfig the config
// is stored directly (not wrapped in a "coder" envelope), because it is the
// whole config column of a dedicated coder connection row.
func DecodeOrgCoderConfig(config json.RawMessage) (OrgCoderConfig, error) {
	if len(config) == 0 {
		return OrgCoderConfig{}, errors.New("organization coder config is empty")
	}
	var cfg OrgCoderConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return OrgCoderConfig{}, err
	}
	cfg.normalize()
	return cfg, nil
}

// EncodeOrgCoderConfig renders an organization's Coder config to the config
// JSONB stored on its provider connection. The token is never part of it.
func EncodeOrgCoderConfig(cfg OrgCoderConfig) (json.RawMessage, error) {
	cfg.normalize()
	return json.Marshal(cfg)
}

type ClientEvent struct {
	SessionID string
	Sequence  int64
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// WorkerTurn is the durable unit of coding-agent work leased to one worker
// epoch. Attempt fences callbacks from an earlier claim even if a request is
// delayed and delivered after the turn has moved on.
type WorkerTurn struct {
	ID                string
	SessionID         string
	Prompt            string
	Model             string
	ReasoningEffort   string
	Mode              string
	ApprovalMode      string
	DeniedCommands    []string
	Harness           string
	Attempt           int
	WorkerEpoch       int64
	CancelRequested   bool
	AgentSessionID    string
	UserEventSequence int64
}

type ChatTurnSettings struct {
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	Mode            string `json:"mode,omitempty"`
	ApprovalMode    string `json:"approvalMode,omitempty"`
}

// WorkerCredential is the encrypted coding-agent credential selected by the
// session harness. Plaintext is produced only at the authenticated HTTP edge.
type WorkerCredential struct {
	Provider        string
	CredentialType  string
	EncryptedSecret []byte
	Nonce           []byte
	OwnerUserID     string
}

// WorkerRequest is a short-lived durable command for the current worker epoch.
// The database owns routing and leasing so any control-plane replica can submit
// or await it without process affinity.
type WorkerRequest struct {
	ID           string
	OrgID        string
	SessionID    string
	WorkerEpoch  int64
	Kind         string
	Payload      json.RawMessage
	Status       string
	Response     json.RawMessage
	ErrorCode    string
	ErrorMessage string
	Attempt      int
	ExpiresAt    time.Time
}

type TerminalSession struct {
	ID                 string
	OrgID              string
	SessionID          string
	WorkerEpoch        int64
	NextOutputSequence int64
	Kind               string
	State              string
	Scopes             []string
	ErrorMessage       string
	ExpiresAt          time.Time
}

type TerminalOutput struct {
	Sequence int64
	Data     []byte
}

type Cursor struct {
	Time time.Time
	ID   string
}
