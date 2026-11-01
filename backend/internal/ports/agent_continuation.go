package ports

import "context"

// FreshNativeSessionIDMode describes who chooses a new provider conversation's
// native identity.
type FreshNativeSessionIDMode string

const (
	// FreshNativeSessionIDProviderAssigned means the provider chooses the id and
	// AO learns it from the provider's lifecycle hooks.
	FreshNativeSessionIDProviderAssigned FreshNativeSessionIDMode = "provider_assigned"
	// FreshNativeSessionIDCallerAssigned means the adapter accepts
	// LaunchConfig.NativeSessionID for a fresh conversation.
	FreshNativeSessionIDCallerAssigned FreshNativeSessionIDMode = "caller_assigned"
)

// ContinuationCapabilities is the static fresh-conversation behavior AO needs
// before it activates an agent as part of a session switch. Runtime evidence
// such as whether one particular native session still exists is reported by
// AgentNativeSessionProber instead.
type ContinuationCapabilities struct {
	FreshNativeSessionID FreshNativeSessionIDMode
}

// AgentContinuationCapabilityProvider is implemented by adapters whose
// fresh native-session behavior has been verified. Its absence is
// intentionally treated as unsupported rather than assuming a provider
// accepts caller-selected native ids.
type AgentContinuationCapabilityProvider interface {
	ContinuationCapabilities() ContinuationCapabilities
}

// AgentFreshNativeSessionIDProvider keeps provider-specific id formats out of
// switch orchestration. AO persists the returned id and passes it back through
// LaunchConfig.NativeSessionID.
type AgentFreshNativeSessionIDProvider interface {
	NewNativeSessionID() string
}

// NativeSessionRef identifies provider-owned local session state without
// conflating it with the stable AO session id. ConfigDir is the exact native
// configuration/state root used by that invocation (for example CODEX_HOME).
type NativeSessionRef struct {
	NativeSessionID string
	ConfigDir       string
}

// AgentNativeSessionConfigProvider resolves the provider state root used for
// a launch. AO stores the returned path with the native-session binding so a
// later probe does not accidentally inspect another account/config home.
type AgentNativeSessionConfigProvider interface {
	NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error)
}

// NativeSessionAvailability is a tri-state result. Unknown is materially
// different from unavailable: some providers can resume remote conversations
// even when AO cannot find authoritative local state.
type NativeSessionAvailability string

const (
	// NativeSessionAvailabilityAvailable means authoritative backing state exists.
	NativeSessionAvailabilityAvailable NativeSessionAvailability = "available"
	// NativeSessionAvailabilityUnavailable means authoritative state is absent.
	NativeSessionAvailabilityUnavailable NativeSessionAvailability = "unavailable"
	// NativeSessionAvailabilityUnknown means the adapter cannot prove either case.
	NativeSessionAvailabilityUnknown NativeSessionAvailability = "unknown"
)

// AgentNativeSessionProber is an optional adapter capability for checking
// whether a stored provider-native resume handle still has usable backing
// state. A probe must return Unknown when its evidence is not authoritative.
type AgentNativeSessionProber interface {
	ProbeNativeSession(ctx context.Context, ref NativeSessionRef) (NativeSessionAvailability, error)
}

// AgentTranscriptLocator is an optional adapter capability for finding the
// provider-owned transcript associated with a native session. ok=false means
// no readable transcript was found; it does not by itself prove the native
// conversation cannot be resumed.
type AgentTranscriptLocator interface {
	LocateTranscript(ctx context.Context, ref NativeSessionRef) (path string, ok bool, err error)
}

// ImportedSessionRelocation describes an imported native session that AO is
// about to resume for the first time, from a different directory than the one
// the provider recorded it in.
type ImportedSessionRelocation struct {
	NativeSessionID string
	// ConfigDir is the provider state root that holds the imported session.
	ConfigDir string
	// TranscriptPath is the provider file the import read, when known.
	TranscriptPath string
	// SourceCWD is the directory the provider recorded for the session.
	SourceCWD string
	// LaunchCWD is the directory AO starts the provider in. Adapters apply the
	// provider's own path normalization, such as resolving symlinks.
	LaunchCWD string
}

// AgentImportedSessionRelocator is implemented by adapters whose provider finds
// a native session by the directory it runs in, not by id alone. Before AO
// first resumes an imported session, it calls RelocateImportedSession so the
// provider can load the same native id from LaunchCWD. AO then launches every
// later open of that session in LaunchCWD (SessionImportSource.ResumeCwd), so
// it is called once per import and never has to move a copy that already holds
// AO's turns. Providers that find a session by id from any directory do not
// implement it.
//
// Implementations must leave the source session untouched, write atomically
// with private permissions, and keep a copy that already exists: once AO has
// resumed, that copy holds AO's later turns.
type AgentImportedSessionRelocator interface {
	RelocateImportedSession(ctx context.Context, r ImportedSessionRelocation) error
}
