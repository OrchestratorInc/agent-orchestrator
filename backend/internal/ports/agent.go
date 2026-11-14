package ports

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	ErrUnsupportedEffort            = errors.New("unsupported model effort")
	ErrModelCapabilitiesUnavailable = errors.New("model capabilities unavailable")
	ErrAgentAuthRequired            = errors.New("agent requires authentication")
	ErrNativeHistoryUnavailable     = errors.New("agent native history unavailable")
	ErrAgentVersionTooOld           = errors.New("agent CLI is too old for the selected model")
)

var ErrAgentBinaryNotFound = errors.New("agent: binary not found on PATH")

var ErrAgentBinaryIdentityUnknown = errors.New("agent: binary identity unknown")

var ErrAgentModelDiscoverySignInRequired = errors.New("agent: sign-in required to list models")

var ErrAgentModelDiscoveryCredentialRejected = errors.New("agent: credential rejected while listing models")

var ErrAgentModelDiscoveryCredentialExpired = errors.New("agent: stored login expired while listing models")

type AgentAuthStatus string

const (
	AgentAuthStatusAuthorized   AgentAuthStatus = "authorized"
	AgentAuthStatusUnauthorized AgentAuthStatus = "unauthorized"
	AgentAuthStatusUnknown      AgentAuthStatus = "unknown"
	AgentAuthStatusConfigured   AgentAuthStatus = "configured"
)

func (s AgentAuthStatus) HasCredential() bool {
	return s == AgentAuthStatusAuthorized || s == AgentAuthStatusConfigured
}

type AuthEvidence struct {
	mu          sync.Mutex
	identity    domain.AgentAccountIdentity
	diagnostics []domain.AgentAuthDiagnostic
}

type authEvidenceKey struct{}

func WithAuthEvidence(ctx context.Context) (context.Context, *AuthEvidence) {
	evidence := &AuthEvidence{}
	return context.WithValue(ctx, authEvidenceKey{}, evidence), evidence
}

func RecordAuthEvidence(ctx context.Context, identity domain.AgentAccountIdentity) {
	evidence, ok := ctx.Value(authEvidenceKey{}).(*AuthEvidence)
	if !ok || evidence == nil {
		return
	}
	evidence.mu.Lock()
	evidence.identity = identity
	evidence.mu.Unlock()
}

func AddAuthEvidence(ctx context.Context, identity domain.AgentAccountIdentity) {
	evidence, ok := ctx.Value(authEvidenceKey{}).(*AuthEvidence)
	if !ok || evidence == nil {
		return
	}
	evidence.mu.Lock()
	evidence.identity = evidence.identity.Merge(identity)
	evidence.mu.Unlock()
}

func (e *AuthEvidence) Identity() domain.AgentAccountIdentity {
	if e == nil {
		return domain.AgentAccountIdentity{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.identity
}

type authEnvironmentKey struct{}

func WithAuthEnvironment(ctx context.Context, overrides map[string]string) context.Context {
	if len(overrides) == 0 {
		return ctx
	}
	return context.WithValue(ctx, authEnvironmentKey{}, overrides)
}

func AuthEnvironmentOverride(ctx context.Context, name string) (string, bool) {
	overrides, _ := ctx.Value(authEnvironmentKey{}).(map[string]string)
	value, ok := overrides[name]
	return value, ok
}

func AuthEnvironmentOverrides(ctx context.Context) map[string]string {
	overrides, _ := ctx.Value(authEnvironmentKey{}).(map[string]string)
	return overrides
}

func RecordAuthDiagnostic(ctx context.Context, diagnostic domain.AgentAuthDiagnostic) {
	evidence, ok := ctx.Value(authEvidenceKey{}).(*AuthEvidence)
	if !ok || evidence == nil {
		return
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	if len(evidence.diagnostics) < 64 {
		evidence.diagnostics = append(evidence.diagnostics, diagnostic)
	}
}

func (e *AuthEvidence) Diagnostics() []domain.AgentAuthDiagnostic {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]domain.AgentAuthDiagnostic(nil), e.diagnostics...)
}

type AgentAuthDiagnosticsChecker interface {
	AuthStatusWithDiagnostics(context.Context) (AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error)
}

type AgentRuntimeAuthChecker interface {
	AuthStatusForRuntime(ctx context.Context, dataDir string) (AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error)
}

type AgentRuntimeCredentialEnv interface {
	RuntimeCredentialEnv(dataDir string, base map[string]string) map[string]string
}

type AgentAuthCacheInvalidator interface {
	InvalidateAuthCache()
}

type Agent interface {
	GetConfigSpec(ctx context.Context) (ConfigSpec, error)

	GetLaunchCommand(ctx context.Context, cfg LaunchConfig) (cmd []string, err error)

	GetPromptDeliveryStrategy(ctx context.Context, cfg LaunchConfig) (PromptDeliveryStrategy, error)

	GetAgentHooks(ctx context.Context, cfg WorkspaceHookConfig) error

	GetRestoreCommand(ctx context.Context, cfg RestoreConfig) (cmd []string, ok bool, err error)

	SessionInfo(ctx context.Context, session SessionRef) (info SessionInfo, ok bool, err error)
}

type AgentNativeRestoreCommand interface {
	GetRestoreCommandWithSessionID(ctx context.Context, cfg RestoreConfig) (cmd []string, sessionID string, ok bool, err error)
}

type AgentAuthChecker interface {
	AuthStatus(ctx context.Context) (AgentAuthStatus, error)
}

type AgentLaunchAuthValidator interface {
	ValidateLaunchAuth(ctx context.Context, workingDir string, env map[string]string) (AgentAuthStatus, error)
}

type AgentBinaryResolver interface {
	ResolveBinary(ctx context.Context) (path string, err error)
}

type AgentRuntimeLaunchEnv interface {
	AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, launchID string)
}

type AgentTerminalVersionRefresher interface {
	SupportsTerminalVersionRefresh() bool
}

type AgentNativeSessionResolver interface {
	ResolveNativeSessionID(ctx context.Context, cfg NativeSessionResolveConfig) (sessionID string, ok bool, err error)
}

type NativeSessionResolveConfig struct {
	DataDir   string
	SessionID domain.SessionID
	LaunchID  string
	// Env is the session's runtime environment, as the runtime layers it over
	// the daemon's for the agent process, for adapters whose lookup runs the
	// agent CLI against a store that project-level settings or credentials
	// select. nil means the caller has none.
	Env map[string]string
}

type AgentBinaryResolutionInvalidator interface {
	InvalidateBinaryResolution()
}

type AgentBinaryPresenceResolver interface {
	ResolveBinaryPresence(ctx context.Context) (path string, err error)
}

type AgentReadinessProvider interface {
	EnsureAgentReadiness(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error)
	InvalidateAgentInstallation(agentID string)
	InvalidateAgentAuthentication(agentID string)
	RecheckAgent(agentID string)
}

type AgentNativeSessionTerminator interface {
	TerminateNativeSession(ctx context.Context, session SessionRef) error
}

type AgentInterfaceHandoff interface {
	NativeConversationID(
		ctx context.Context,
		session SessionRef,
		currentMode domain.SessionMode,
		providerConversationID string,
	) (id string, ok bool, err error)
}

type AgentInterfaceHandoffHistoryProbe interface {
	NativeConversationExists(
		ctx context.Context,
		session SessionRef,
		nativeConversationID string,
		env map[string]string,
	) (bool, error)
}

type AgentInterfaceHandoffStorageBridge interface {
	PrepareInterfaceHandoffStorage(
		ctx context.Context,
		session SessionRef,
		from, to domain.SessionMode,
		nativeConversationID string,
		env map[string]string,
	) error
}

type ModelSelectionMode string

const (
	ModelSelectionCatalog  ModelSelectionMode = "catalog"
	ModelSelectionText     ModelSelectionMode = "text"
	ModelSelectionModeList ModelSelectionMode = "mode"
)

type CustomModelEntryMode string

const (
	CustomModelEntryNone       CustomModelEntryMode = "none"
	CustomModelEntryDirect     CustomModelEntryMode = "direct"
	CustomModelEntryConfigured CustomModelEntryMode = "configured"
)

type AgentModelInfo struct {
	ContextWindow     int64      `json:"contextWindow,omitempty"`
	Inputs            []string   `json:"inputs,omitempty"`
	ID                string     `json:"id"`
	Label             string     `json:"label"`
	Provider          string     `json:"provider,omitempty"`
	IsDefault         bool       `json:"isDefault,omitempty"`
	Efforts           []string   `json:"efforts,omitempty"`
	DefaultEffort     string     `json:"defaultEffort,omitempty"`
	SupportsFast      bool       `json:"supportsFast,omitempty"`
	LastUsedAt        *time.Time `json:"lastUsedAt,omitempty"`
	RequiresVersion   string     `json:"requiresVersion,omitempty"`
	UnavailableReason string     `json:"unavailableReason,omitempty"`
}

type AgentModelCatalog struct {
	Providers           []string             `json:"providers,omitempty"`
	ConfiguredProviders []string             `json:"configuredProviders,omitempty"`
	RunnableModels      []string             `json:"runnableModels,omitempty"`
	AgentID             string               `json:"agentId"`
	SelectionMode       ModelSelectionMode   `json:"selectionMode" enum:"catalog,text,mode"`
	Models              []AgentModelInfo     `json:"models"`
	CustomModelEntry    CustomModelEntryMode `json:"customModelEntry" enum:"none,direct,configured"`
	AllowCustom         bool                 `json:"allowCustom"`
	Source              string               `json:"source"`
	Metadata            map[string]string    `json:"metadata,omitempty"`
	InputFingerprint    string               `json:"inputFingerprint,omitempty"`
	BinaryVersion       string               `json:"binaryVersion,omitempty"`
	FetchedAt           time.Time            `json:"fetchedAt"`
	ValidatedAt         time.Time            `json:"validatedAt,omitempty"`
	LastSuccessAt       *time.Time           `json:"lastSuccessAt,omitempty"`
	RefreshState        string               `json:"refreshState,omitempty" enum:"idle,queued,refreshing,error"`
	RefreshError        string               `json:"refreshError,omitempty"`
	RetryAt             *time.Time           `json:"retryAt,omitempty"`
	RefreshRecommended  bool                 `json:"refreshRecommended,omitempty"`
	Stale               bool                 `json:"stale"`
	Warning             string               `json:"warning,omitempty"`
	WarningCode         string               `json:"warningCode,omitempty" enum:"auth_required,auth_expired"`
}

const (
	ModelCatalogWarningAuthRequired = "auth_required"
	ModelCatalogWarningAuthExpired  = "auth_expired"
)

type CachedAgentModelCatalog struct {
	AgentID          string
	ProjectID        string
	BinaryVersion    string
	CatalogJSON      string
	Source           string
	FetchedAt        time.Time
	MetadataJSON     string
	InputFingerprint string
	LastSuccessAt    time.Time
	RefreshState     string
	RefreshError     string
	RetryCount       int64
	RetryAt          time.Time
	Generation       int64
}

type AgentModelCatalogCache interface {
	GetAgentModelCatalog(ctx context.Context, agentID, projectID string) (CachedAgentModelCatalog, bool, error)
	ListAgentModelCatalogsByAgent(ctx context.Context, agentID string) ([]CachedAgentModelCatalog, error)
	UpsertAgentModelCatalog(ctx context.Context, record CachedAgentModelCatalog) error
}

type AgentModelCatalogScopeCache interface {
	ListAgentModelCatalogs(ctx context.Context) ([]CachedAgentModelCatalog, error)
}

type AgentModelDiscoveryRequest struct {
	AgentID        string
	Binary         string
	WorkingDir     string
	Env            map[string]string
	CredentialType string
}

type AgentModelDiscoverer interface {
	Discover(ctx context.Context, request AgentModelDiscoveryRequest) (AgentModelCatalog, error)
	CatalogFingerprint(ctx context.Context, request AgentModelDiscoveryRequest) string
	Manual(agentID string) AgentModelCatalog
}

type AgentExitDetectionMode string

const (
	AgentExitDetectionSupervisor AgentExitDetectionMode = "supervisor"
)

type AgentExitDetector interface {
	ExitDetectionMode() AgentExitDetectionMode
}

type AgentPromptReadinessProvider interface {
	PromptReadinessHints(ctx context.Context, cfg LaunchConfig) (PromptReadinessHints, error)
}

type AgentAfterStartPromptBuilder interface {
	BuildAfterStartPrompt(ctx context.Context, cfg LaunchConfig) (string, error)
}

type AgentAfterStartCommand interface {
	AfterStartCommand(ctx context.Context, cfg LaunchConfig) (string, error)
	AfterStartCommandApplied(cfg LaunchConfig, output string) bool
}

type TerminalActivityDetector interface {
	DetectTerminalActivity(output string) (domain.ActivityState, bool)
}

type TurnFailure struct {
	At          time.Time
	Message     string
	Interrupted bool
}

type TranscriptTurnFailureDetector interface {
	LastTurnFailure(ctx context.Context, transcriptPath string) (failure TurnFailure, ok bool, err error)
}

type TerminalActivityOptions struct {
	PreferStyledOutput bool
	SupportsBlocked    bool
}

type TerminalActivityOptionsProvider interface {
	TerminalActivityOptions() TerminalActivityOptions
}

type EmptyComposerDetector interface {
	ComposerIsEmpty(output string) bool
}

type WaitingInputComposerReadiness interface {
	EmptyComposerProvesWaitingInputReady() bool
}

type ContinuousTerminalActivityDetector interface {
	TerminalActivityDetector
	ContinuouslyDetectTerminalActivity() bool
}

type WaitingTerminalActivityDetector interface {
	TerminalActivityDetector
	ContinuouslyDetectTerminalActivityWhileWaiting() bool
}

type PromptReadinessHints struct {
	InitialDelay     time.Duration
	Patterns         []string
	PollInterval     time.Duration
	Timeout          time.Duration
	Lines            int
	RenderedScreen   bool
	BlockingPatterns []string
}

type AgentResolver interface {
	Agent(harness domain.AgentHarness) (Agent, bool)
}

type SubmitActivitySignaler interface {
	EmitsSubmitActivity() bool
}

type SemanticMessageAcceptanceSignaler interface {
	EmitsSemanticMessageAcceptance() bool
}

type BlockedActivitySignaler interface {
	EmitsBlockedActivity() bool
}

type StartupInputReadinessSignaler interface {
	FirstSignalProvesInputReady() bool
}

type ActiveTurnSteerer interface {
	SteersActiveTurn() bool
}

const (
	MetadataKeyAgentSessionID       = "agentSessionId"
	MetadataKeyNativeTranscriptPath = "nativeTranscriptPath"
)

const MetadataKeySessionMode = "sessionMode"

const (
	MetadataKeyTitle   = "title"
	MetadataKeySummary = "summary"
)

type AgentConfig = domain.AgentConfig

type ConfigSpec struct {
	Fields []ConfigField
}

type ConfigField struct {
	Key         string
	Type        ConfigFieldType
	Description string
	Required    bool
	Default     any
	Enum        []string
}

type ConfigFieldType string

const (
	ConfigFieldString     ConfigFieldType = "string"
	ConfigFieldBool       ConfigFieldType = "bool"
	ConfigFieldNumber     ConfigFieldType = "number"
	ConfigFieldStringList ConfigFieldType = "string_list"
	ConfigFieldEnum       ConfigFieldType = "enum"
)

type LaunchConfig struct {
	Config           AgentConfig
	DataDir          string
	IssueID          string
	Kind             domain.SessionKind
	Permissions      PermissionMode
	Prompt           string
	SessionID        string
	NativeSessionID  string
	AllowedTools     []string
	DisallowedTools  []string
	SystemPrompt     string
	SystemPromptFile string
	WorkspacePath    string
	Env              map[string]string
}

type WorkspaceHookConfig struct {
	Config           AgentConfig
	DataDir          string
	Env              map[string]string
	SessionID        string
	SystemPrompt     string
	SystemPromptFile string
	WorkspacePath    string
	NativeSessionID  string
	ImportConfigDir  string
}

type RestoreConfig struct {
	Config           AgentConfig
	DataDir          string
	Kind             domain.SessionKind
	Permissions      PermissionMode
	AllowedTools     []string
	DisallowedTools  []string
	Session          SessionRef
	Prompt           string
	SystemPrompt     string
	SystemPromptFile string
	Env              map[string]string
}

type SessionRef struct {
	ID            string
	Metadata      map[string]string
	WorkspacePath string
	DataDir       string
}

type SessionInfo struct {
	AgentSessionID string
	Metadata       map[string]string
	Title          string
	Summary        string
}

type PermissionMode = domain.PermissionMode

const (
	PermissionModeDefault           = domain.PermissionModeDefault
	PermissionModeAcceptEdits       = domain.PermissionModeAcceptEdits
	PermissionModeAuto              = domain.PermissionModeAuto
	PermissionModeBypassPermissions = domain.PermissionModeBypassPermissions
)

func NormalizePermissionMode(mode PermissionMode) PermissionMode {
	switch mode {
	case PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModeAuto,
		PermissionModeBypassPermissions:
		return mode
	default:
		return PermissionModeDefault
	}
}

type PromptDeliveryStrategy string

const (
	PromptDeliveryInCommand   PromptDeliveryStrategy = "in_command"
	PromptDeliveryAfterStart  PromptDeliveryStrategy = "after_start"
	PromptDeliveryCustomAgent PromptDeliveryStrategy = "custom_agent"
)

type AgentInteractiveNativeRestorer interface {
	PrepareNativeRestore(context.Context, RestoreConfig) (NativeRestoreInitialization, error)
}

type NativeRestoreInitialization interface {
	Argv() []string
	LaunchEnv() map[string]string
	ResumeInput() string
	TargetID() string
	Ready(context.Context, string, string) (bool, error)
	Restored(context.Context, string, string) (bool, error)
}
